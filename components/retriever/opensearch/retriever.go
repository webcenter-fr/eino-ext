package opensearch

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/embedding"
	"github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/schema"
	opensearchv4 "github.com/disaster37/opensearch/v4"
	"github.com/disaster37/opensearch/v4/api"
	"github.com/disaster37/opensearch/v4/querydsl"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/osclient"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
	"k8s.io/utils/ptr"
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

	// Embedding, when set, enables kNN vector search. If Hybrid is also true,
	// the vector search is combined with a BM25 match on ContentField via a
	// bool "should" query (the OpenSearch search pipeline, if configured, then
	// combines/normalizes the two sub-scores).
	Embedding embedding.Embedder `validate:"-" jsonschema:"-"`

	// VectorField is the knn_vector field to search. Required when Embedding is set.
	VectorField string `validate:"omitempty" jsonschema:"description=knn_vector field name for kNN search"`

	// ContentField is the text field used for the BM25 side of a hybrid query
	// (also the field defaultResultParser reads as Content). Defaults to "content".
	ContentField string `validate:"omitempty" jsonschema:"description=Text field for BM25 match, defaults to content"`

	// Hybrid combines the kNN query with a BM25 match on ContentField (requires
	// Embedding and VectorField). If false and Embedding is set, pure kNN only.
	Hybrid bool `validate:"omitempty" jsonschema:"description=Combine kNN with BM25 on ContentField"`

	// K is the number of nearest neighbors requested from the kNN query.
	// Defaults to TopK (see Retrieve) when zero.
	K int `validate:"omitempty" jsonschema:"description=Number of nearest neighbors for kNN"`

	// Index is the OpenSearch index to search. A per-call
	// retriever.WithIndex option overrides this default.
	Index string `validate:"required" jsonschema:"description=OpenSearch index to search"`

	// EnsureSearchPipeline, when true, idempotently creates SearchPipeline
	// on the cluster during NewRetriever if it does not already exist.
	// Failures are not fatal: the retriever still returns successfully and
	// falls back to un-fused hybrid scoring. Requires SearchPipeline.
	EnsureSearchPipeline bool `validate:"omitempty" jsonschema:"description=Idempotently create SearchPipeline on startup"`
}

// Retriever implements retriever.Retriever backed by OpenSearch.
type Retriever struct {
	client       opensearchv4.Client
	resultParser ResultParser
	config       Config
}

// Compile-time check that Retriever implements retriever.Retriever.
var _ retriever.Retriever = (*Retriever)(nil)

// NewRetriever creates a new OpenSearch retriever.
func NewRetriever(ctx context.Context, config *Config) (*Retriever, error) {
	if config == nil {
		config = &Config{}
	}
	if config.ContentField == "" {
		config.ContentField = "content"
	}
	if err := validate.Struct(config); err != nil {
		return nil, err
	}

	if config.Embedding != nil && config.VectorField == "" {
		return nil, errors.New("VectorField is required when Embedding is set")
	}

	client, err := osclient.New(ctx, osclient.Config{
		URLs:          config.URLs,
		Username:      config.Username,
		Password:      config.Password,
		TLSSkipVerify: config.TLSSkipVerify,
	}, 0)
	if err != nil {
		return nil, err
	}

	rp := config.ResultParser
	if rp == nil {
		rp = defaultResultParser
	}

	r := &Retriever{
		client:       client,
		resultParser: rp,
		config:       *config,
	}

	if config.EnsureSearchPipeline && config.SearchPipeline != "" {
		if _, ensureErr := EnsureRRFPipeline(ctx, r.client, config.SearchPipeline); ensureErr != nil {
			fmt.Printf("[WARN] opensearch retriever: failed to ensure RRF search pipeline %q (hybrid search runs without pipeline-based score fusion): %v\n", config.SearchPipeline, ensureErr)
		}
	}

	return r, nil
}

// GetType returns the component type identifier.
func (r *Retriever) GetType() string {
	return typ
}

// IsCallbacksEnabled reports that this retriever supports callbacks.
func (r *Retriever) IsCallbacksEnabled() bool {
	return true
}

// Client returns the underlying OpenSearch client.
func (r *Retriever) Client() opensearchv4.Client {
	return r.client
}

// Retrieve performs a search against OpenSearch and returns matching documents.
func (r *Retriever) Retrieve(ctx context.Context, query string, opts ...retriever.Option) ([]*schema.Document, error) {
	commonOpts := retriever.GetCommonOptions(&retriever.Options{
		Index: ptr.To(r.config.Index),
		TopK:  ptr.To(defaultTopK),
	}, opts...)

	topK := defaultTopK
	if commonOpts.TopK != nil {
		topK = *commonOpts.TopK
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var vectors [][]float64
	if r.config.Embedding != nil {
		var embedErr error
		vectors, embedErr = r.config.Embedding.EmbedStrings(ctx, []string{query})
		if embedErr != nil {
			return nil, errors.Wrap(embedErr, "failed to embed query")
		}
	}

	body := r.buildSearchBody(query, topK, vectors)

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

// buildSearchBody constructs the OpenSearch request body based on the
// configured search mode (pure BM25, pure kNN, or hybrid).
func (r *Retriever) buildSearchBody(query string, topK int, vectors [][]float64) map[string]any {
	var queryPart map[string]any

	if r.config.Embedding == nil {
		queryPart = map[string]any{
			"match": map[string]any{
				r.config.ContentField: map[string]any{
					"query":    query,
					"operator": "and",
				},
			},
		}
	} else {
		k := r.config.K
		if k == 0 {
			k = topK
		}
		knn := map[string]any{
			"knn": map[string]any{
				r.config.VectorField: map[string]any{
					"vector": vectors[0],
					"k":      k,
				},
			},
		}
		if r.config.Hybrid {
			queryPart = map[string]any{
				"bool": map[string]any{
					"should": []any{
						knn,
						map[string]any{
							"match": map[string]any{r.config.ContentField: query},
						},
					},
				},
			}
		} else {
			queryPart = knn
		}
	}

	body := map[string]any{
		"query": queryPart,
		"size":  topK,
	}
	if r.config.SearchPipeline != "" {
		body["search_pipeline"] = r.config.SearchPipeline
	}
	return body
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
