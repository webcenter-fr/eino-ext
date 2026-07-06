package opensearch

import (
	"context"
	"encoding/json"
	"time"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/schema"
	opensearchv4 "github.com/disaster37/opensearch/v4"
	"github.com/disaster37/opensearch/v4/api"
	"github.com/disaster37/opensearch/v4/querydsl"
	"github.com/sirupsen/logrus"
)

const (
	defaultTopK = 10
	typ         = "OpenSearch3"
)

// ResultParser converts a raw OpenSearch search hit into a
// schema.Document. When nil, defaultResultParser is used (reads
// the "content" field).
type ResultParser func(ctx context.Context, hit map[string]any) (*schema.Document, error)

// Config wraps the upstream retriever configuration with an optional
// search pipeline name.
type Config struct {
	// URLs is the list of OpenSearch cluster URLs.
	URLs []string `validate:"required,min=1" jsonschema:"description=OpenSearch cluster URLs"`

	// Username for basic authentication.
	Username string `validate:"omitempty" jsonschema:"description=Username for basic authentication"`

	// Password for basic authentication.
	Password string `validate:"omitempty" jsonschema:"description=Password for basic authentication"`

	// TLSSkipVerify controls whether TLS certificate verification is skipped.
	TLSSkipVerify bool `validate:"omitempty" jsonschema:"description=Skip TLS certificate verification"`

	// ResultParser is an optional hook that converts a raw search hit
	// into a schema.Document. If nil, defaultResultParser is used.
	ResultParser ResultParser `validate:"-" jsonschema:"-"`

	// SearchPipeline is an optional search pipeline name applied to every
	// search request (added via the request body).
	SearchPipeline string `validate:"omitempty" jsonschema:"description=Optional search pipeline name"`
}

// Retriever implements retriever.Retriever backed by OpenSearch.
type Retriever struct {
	client         opensearchv4.Client
	resultParser   ResultParser
	searchPipeline string
}

// NewRetriever creates a new OpenSearch retriever.
func NewRetriever(ctx context.Context, config *Config) (*Retriever, error) {
	if config == nil {
		return nil, errors.New("config is required")
	}
	if len(config.URLs) == 0 {
		return nil, errors.New("at least one URL is required")
	}

	opensearchCfg := &opensearchv4.Config{
		URL:           config.URLs[0],
		Username:      config.Username,
		Password:      config.Password,
		TLSSkipVerify: config.TLSSkipVerify,
	}

	logger := logrus.NewEntry(logrus.StandardLogger())
	client, err := opensearchv4.New(opensearchCfg, logger)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create OpenSearch client")
	}

	rp := config.ResultParser
	if rp == nil {
		rp = defaultResultParser
	}

	return &Retriever{
		client:         client,
		resultParser:   rp,
		searchPipeline: config.SearchPipeline,
	}, nil
}

// GetType returns the component type identifier.
func (r *Retriever) GetType() string {
	return typ
}

// IsCallbacksEnabled reports that this retriever supports callbacks.
func (r *Retriever) IsCallbacksEnabled() bool {
	return true
}

// Retrieve performs a search against OpenSearch and returns matching documents.
func (r *Retriever) Retrieve(ctx context.Context, query string, opts ...retriever.Option) ([]*schema.Document, error) {
	commonOpts := retriever.GetCommonOptions(&retriever.Options{
		Index: stringPtr(""),
		TopK:  intPtr(defaultTopK),
	}, opts...)

	topK := defaultTopK
	if commonOpts.TopK != nil {
		topK = *commonOpts.TopK
	}
	if *commonOpts.Index == "" {
		return nil, errors.New("index is required: use retriever.WithIndex(\"your-index-name\")")
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	body := map[string]any{
		"query": map[string]any{
			"query_string": map[string]any{
				"query":            query,
				"default_operator": "AND",
			},
		},
		"size": topK,
	}

	if r.searchPipeline != "" {
		body["search_pipeline"] = r.searchPipeline
	}

	req := &api.SearchRequest{
		Indices: []string{*commonOpts.Index},
		Body:    body,
	}

	result, err := r.client.Search().Search(ctx, req)
	if err != nil {
		return nil, errors.Wrap(err, "failed to search OpenSearch")
	}

	return r.parseSearchResult(ctx, result)
}

// parseSearchResult converts OpenSearch search hits into schema.Document
// instances using the configured result parser.
func (r *Retriever) parseSearchResult(ctx context.Context, result *querydsl.SearchResult) ([]*schema.Document, error) {
	if result.Hits == nil || len(result.Hits.Hits) == 0 {
		return nil, nil
	}

	docs := make([]*schema.Document, 0, len(result.Hits.Hits))
	for _, hit := range result.Hits.Hits {
		hitMap, err := searchHitToMap(hit)
		if err != nil {
			return nil, err
		}
		doc, err := r.resultParser(ctx, hitMap)
		if err != nil {
			return nil, err
		}
		docs = append(docs, doc)
	}

	return docs, nil
}

// searchHitToMap converts a search hit to a map representation, adding
// metadata fields (_id, _index, _score, _version).
func searchHitToMap(hit *querydsl.SearchHit) (map[string]any, error) {
	src := map[string]any{}
	if err := json.Unmarshal(hit.Source, &src); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal search hit source")
	}
	src["_id"] = hit.Id
	src["_index"] = hit.Index
	if hit.Score != nil {
		src["_score"] = *hit.Score
	}
	src["_version"] = hit.Version
	return src, nil
}

// defaultResultParser extracts content and metadata from a raw OpenSearch hit.
// It reads the "content" field as the document content and puts all remaining
// fields into metadata.
func defaultResultParser(_ context.Context, hit map[string]any) (*schema.Document, error) {
	content, _ := hit["content"].(string)
	meta := make(map[string]any, len(hit))
	for k, v := range hit {
		if k == "content" {
			continue
		}
		meta[k] = v
	}
	return &schema.Document{
		Content:  content,
		MetaData: meta,
	}, nil
}

func stringPtr(s string) *string { return &s }
func intPtr(i int) *int          { return &i }
