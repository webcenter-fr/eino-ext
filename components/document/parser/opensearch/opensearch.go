// Package opensearch provides a document parser that converts OpenSearch search
// responses into eino schema.Document values.
package opensearch

import (
	"context"
	"encoding/json"
	"io"
	"strings"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/document/parser"
	"github.com/cloudwego/eino/schema"
	"github.com/elastic/go-ucfg"
	"github.com/webcenter-fr/eino-ext/libs/docid"
)

const (
	// MetaKeyId is the metadata key for the document identifier.
	MetaKeyId    = "id"
	MetaKeyIndex = "index"
	MetaScore    = "score"
	MetaVersion  = "version"

	// DefaultSourceIDField / DefaultSourceHashField are the metadata keys
	// under which the source record id and the content hash are written when
	// Config.SourceIDField / Config.SourceHashField are left empty.
	DefaultSourceIDField   = "source_id"
	DefaultSourceHashField = "source_hash"
)

// Config holds the parser configuration.
type Config struct {
	// FieldSelectors is a list of field selectors used to extract content
	// from each search hit (e.g. "content", "fields.title").
	FieldSelectors []string `validate:"omitempty" jsonschema:"description=List of field selectors to extract content"`

	// FieldIgnores is the list of fields to exclude from metadata.
	FieldIgnores []string `validate:"omitempty" jsonschema:"description=List of fields to ignore"`

	// SourceIDField is the metadata key receiving the source record `_id`.
	// Defaults to DefaultSourceIDField ("source_id").
	SourceIDField string `validate:"omitempty" jsonschema:"description=Metadata key for source record _id,default=source_id"`

	// SourceHashField is the metadata key receiving the content hash.
	// Defaults to DefaultSourceHashField ("source_hash").
	SourceHashField string `validate:"omitempty" jsonschema:"description=Metadata key for content hash,default=source_hash"`
}

// Parser implements parser.Parser for OpenSearch search results.
type Parser struct {
	conf *Config
}

// Compile-time interface check.
var _ parser.Parser = (*Parser)(nil)

// NewParser creates a new OpenSearch document parser.
func NewParser(ctx context.Context, conf *Config) (*Parser, error) {
	if conf == nil {
		conf = &Config{}
	}
	if conf.SourceIDField == "" {
		conf.SourceIDField = DefaultSourceIDField
	}
	if conf.SourceHashField == "" {
		conf.SourceHashField = DefaultSourceHashField
	}
	return &Parser{conf: conf}, nil
}

// Parse reads OpenSearch search results from the reader and converts each hit
// into a schema.Document. The search response is expected to be JSON in the
// format returned by OpenSearch `_search` API.
func (p *Parser) Parse(ctx context.Context, reader io.Reader, opts ...parser.Option) ([]*schema.Document, error) {
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read search results")
	}

	var searchResp searchResponse
	if err := json.Unmarshal(data, &searchResp); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal search response")
	}

	if searchResp.Hits == nil || len(searchResp.Hits.Hits) == 0 {
		return nil, nil
	}

	docs := make([]*schema.Document, 0, len(searchResp.Hits.Hits))
	for _, hit := range searchResp.Hits.Hits {
		doc, err := p.ConvertHit(hit.Source, hit.ID, hit.Index, hit.Score, hit.Version)
		if err != nil {
			return nil, err
		}
		if doc != nil {
			docs = append(docs, doc)
		}
	}

	return docs, nil
}

// ConvertHit converts a raw OpenSearch source map plus its hit metadata into a
// schema.Document. It applies the configured field selectors, ignores, source
// id/hash metadata keys, and content hash computation. This is the single
// conversion path shared by Parse (JSON reader) and direct hit-based callers
// such as the OpenSearch loader.
//
// source is the _source map. hitID, hitIndex, hitScore, hitVersion correspond
// to the OpenSearch _id, _index, _score, _version fields.
func (p *Parser) ConvertHit(source map[string]any, hitID, hitIndex string, hitScore *float64, hitVersion *int64) (*schema.Document, error) {
	if source == nil {
		return nil, nil
	}

	// Convert source to ucfg config for field selection
	cfg, err := ucfg.NewFrom(source, ucfg.PathSep("."))
	if err != nil {
		return nil, errors.Wrap(err, "failed to create ucfg config from hit source")
	}

	content := p.extractContent(cfg, source)
	if content == "" {
		return nil, nil
	}

	meta := p.buildMeta(source, hitID, hitIndex, hitScore, hitVersion)

	// Compute content hash
	b, err := json.Marshal(source)
	if err != nil {
		return nil, errors.Wrap(err, "failed to marshal hit source for content hash")
	}
	meta[p.conf.SourceHashField] = docid.ComputeContentHash(b)
	meta[p.conf.SourceIDField] = hitID

	return &schema.Document{
		ID:       hitID,
		Content:  content,
		MetaData: meta,
	}, nil
}

// searchResponse mirrors the top-level structure of an OpenSearch search result.
type searchResponse struct {
	Hits *searchHits `json:"hits"`
}

// searchHits mirrors the hits container.
type searchHits struct {
	Hits []searchHit `json:"hits"`
}

// searchHit mirrors a single hit.
type searchHit struct {
	ID      string         `json:"_id"`
	Index   string         `json:"_index"`
	Score   *float64       `json:"_score"`
	Version *int64         `json:"_version"`
	Source  map[string]any `json:"_source"`
}

// extractContent applies the configured field selectors to extract text
// content from the hit source. When no selectors are configured, it
// serializes the complete source as JSON.
func (p *Parser) extractContent(cfg *ucfg.Config, source map[string]any) string {
	if len(p.conf.FieldSelectors) == 0 {
		// No selectors: return full source as JSON string
		b, err := json.Marshal(source)
		if err != nil {
			return ""
		}
		return string(b)
	}

	var parts []string
	for _, selector := range p.conf.FieldSelectors {
		if val, err := cfg.String(selector, -1, ucfg.PathSep(".")); err == nil {
			parts = append(parts, val)
		}
	}

	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "\n")
}

// buildMeta constructs the metadata map from source fields and OpenSearch
// metadata, excluding configured ignored fields.
func (p *Parser) buildMeta(source map[string]any, hitID, hitIndex string, hitScore *float64, hitVersion *int64) map[string]any {
	meta := make(map[string]any)

	// Standard OpenSearch metadata
	meta[MetaKeyId] = hitID
	meta[MetaKeyIndex] = hitIndex
	if hitScore != nil {
		meta[MetaScore] = *hitScore
	}
	if hitVersion != nil {
		meta[MetaVersion] = *hitVersion
	}

	// Copy source fields to metadata, excluding ignored fields
	ignoreSet := make(map[string]bool, len(p.conf.FieldIgnores))
	for _, f := range p.conf.FieldIgnores {
		ignoreSet[f] = true
	}

	for k, v := range source {
		if ignoreSet[k] {
			continue
		}
		meta[k] = v
	}

	return meta
}
