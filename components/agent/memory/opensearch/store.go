// Package opensearch provides an OpenSearch-backed implementation of the
// MemoryStore interface for persistent, production-grade agent memory storage
// with BM25 text search and optional kNN vector search via eino's
// indexer/opensearch and retriever/opensearch components.
package opensearch

import (
	"context"
	"time"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/embedding"
	"github.com/cloudwego/eino/components/indexer"
	"github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/schema"
	opensearchv4 "github.com/disaster37/opensearch/v4"
	"github.com/disaster37/opensearch/v4/api"
	"github.com/disaster37/opensearch/v4/querydsl"
	"github.com/goccy/go-json"

	memoryagent "github.com/webcenter-fr/eino-ext/components/agent/memory"
	indexeropensearch "github.com/webcenter-fr/eino-ext/components/indexer/opensearch"
	retrieveropensearch "github.com/webcenter-fr/eino-ext/components/retriever/opensearch"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/osclient"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
)

type Config struct {
	URLs           []string           `validate:"required,min=1" jsonschema:"description=OpenSearch cluster URLs"`
	Username       string             `validate:"omitempty" jsonschema:"description=Username for basic authentication"`
	Password       string             `validate:"omitempty" jsonschema:"description=Password for basic authentication"`
	TLSSkipVerify  bool               `validate:"omitempty" jsonschema:"description=Skip TLS certificate verification"`
	IndexName      string             `validate:"omitempty" jsonschema:"description=OpenSearch index name,default=eino_agent_memory"`
	Embedding      embedding.Embedder `validate:"-" jsonschema:"-"`
	VectorField    string             `validate:"omitempty" jsonschema:"description=knn_vector field name,default=vector"`
	ContentField   string             `validate:"omitempty" jsonschema:"description=Text field for document content,default=content"`
	Hybrid         bool               `validate:"omitempty" jsonschema:"description=Combine kNN with BM25 on ContentField"`
	K              int                `validate:"omitempty" jsonschema:"description=Number of nearest neighbors for kNN"`
	BatchSize      int                `validate:"omitempty,gte=1" jsonschema:"description=Max documents per bulk request,default=100"`
	SearchPipeline string             `validate:"omitempty" jsonschema:"description=Optional search pipeline name"`
}

type Store struct {
	client       opensearchv4.Client
	indexer      indexer.Indexer
	retriever    retriever.Retriever
	indexName    string
	contentField string
}

var _ memoryagent.MemoryStore = (*Store)(nil)

var _ indexer.Indexer = (*Store)(nil)

var _ retriever.Retriever = (*Store)(nil)

func NewStore(ctx context.Context, cfg *Config) (*Store, error) {
	if cfg == nil {
		cfg = &Config{}
	}
	if cfg.IndexName == "" {
		cfg.IndexName = "eino_agent_memory"
	}
	if cfg.VectorField == "" {
		cfg.VectorField = "vector"
	}
	if cfg.ContentField == "" {
		cfg.ContentField = "content"
	}
	if cfg.BatchSize == 0 {
		cfg.BatchSize = 100
	}
	if err := validate.Struct(cfg); err != nil {
		return nil, err
	}

	client, err := osclient.New(ctx, osclient.Config{
		URLs:          cfg.URLs,
		Username:      cfg.Username,
		Password:      cfg.Password,
		TLSSkipVerify: cfg.TLSSkipVerify,
	}, 30*time.Second)
	if err != nil {
		return nil, err
	}

	if err := ensureIndex(ctx, client, cfg); err != nil {
		return nil, err
	}

	idx, err := indexeropensearch.NewIndexer(ctx, &indexeropensearch.Config{
		URLs:          cfg.URLs,
		Username:      cfg.Username,
		Password:      cfg.Password,
		TLSSkipVerify: cfg.TLSSkipVerify,
		Index:         cfg.IndexName,
		BatchSize:     cfg.BatchSize,
		Embedding:     cfg.Embedding,
		VectorField:   cfg.VectorField,
		ContentField:  cfg.ContentField,
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to create OpenSearch indexer")
	}

	ret, err := retrieveropensearch.NewRetriever(ctx, &retrieveropensearch.Config{
		URLs:           cfg.URLs,
		Username:       cfg.Username,
		Password:       cfg.Password,
		TLSSkipVerify:  cfg.TLSSkipVerify,
		SearchPipeline: cfg.SearchPipeline,
		Embedding:      cfg.Embedding,
		VectorField:    cfg.VectorField,
		ContentField:   cfg.ContentField,
		Hybrid:         cfg.Hybrid,
		K:              cfg.K,
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to create OpenSearch retriever")
	}

	return &Store{
		client:       client,
		indexer:      idx,
		retriever:    ret,
		indexName:    cfg.IndexName,
		contentField: cfg.ContentField,
	}, nil
}

func ensureIndex(ctx context.Context, client opensearchv4.Client, cfg *Config) error {
	exists, err := client.Indices().Exists(ctx, []string{cfg.IndexName})
	if err != nil {
		return errors.Wrapf(err, "failed to check if index %s exists", cfg.IndexName)
	}

	if !exists {
		if err := createIndex(ctx, client, cfg); err != nil {
			return err
		}
	}

	return nil
}

func createIndex(ctx context.Context, client opensearchv4.Client, cfg *Config) error {
	properties := map[string]any{
		cfg.ContentField: map[string]any{
			"type": "text",
		},
		"category": map[string]any{
			"type": "keyword",
		},
		"source": map[string]any{
			"type": "keyword",
		},
		"session_id": map[string]any{
			"type": "keyword",
		},
		"user_id": map[string]any{
			"type": "keyword",
		},
		"created_at": map[string]any{
			"type": "date",
		},
		"updated_at": map[string]any{
			"type": "date",
		},
	}

	if cfg.Embedding != nil {
		properties[cfg.VectorField] = map[string]any{
			"type":      "knn_vector",
			"dimension": 384,
			"method": map[string]any{
				"name":       "hnsw",
				"engine":     "nmslib",
				"space_type": "innerproduct",
			},
		}
	}

	body := map[string]any{
		"settings": map[string]any{
			"number_of_shards":          1,
			"index.auto_expand_replicas": "0-2",
		},
		"mappings": map[string]any{
			"dynamic":    false,
			"properties": properties,
		},
	}

	_, err := client.Indices().Create(ctx, cfg.IndexName, body)
	if err != nil {
		return errors.Wrapf(err, "failed to create index %s", cfg.IndexName)
	}

	return nil
}

func (s *Store) Store(ctx context.Context, docs []*schema.Document, opts ...indexer.Option) ([]string, error) {
	return s.indexer.Store(ctx, docs, opts...)
}

func (s *Store) Retrieve(ctx context.Context, query string, opts ...retriever.Option) ([]*schema.Document, error) {
	return s.retriever.Retrieve(ctx, query, withRetrieverIndex(s.indexName, opts)...)
}

func (s *Store) Delete(ctx context.Context, id string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	_, err := s.client.Document().Delete(ctx, &api.DeleteRequest{
		Index: s.indexName,
		Id:    id,
	})
	if err != nil {
		return errors.Wrap(err, "failed to delete document")
	}

	return nil
}

func (s *Store) DeleteByFilter(ctx context.Context, filter map[string]any) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	query, err := buildFilterQuery(filter)
	if err != nil {
		return 0, errors.Wrap(err, "failed to build filter query")
	}

	resp, err := s.client.Document().DeleteByQuery(ctx, []string{s.indexName}, map[string]any{
		"query": query,
	})
	if err != nil {
		return 0, errors.Wrap(err, "failed to delete by filter")
	}

	return int(resp.Deleted), nil
}

func (s *Store) List(ctx context.Context, offset, limit int) ([]*schema.Document, error) {
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = 100
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	body := map[string]any{
		"query": map[string]any{
			"match_all": map[string]any{},
		},
		"from": offset,
		"size": limit,
		"sort": []any{
			map[string]any{
				"created_at": map[string]any{
					"order": "desc",
				},
			},
		},
	}

	result, err := s.client.Search().Search(ctx, &api.SearchRequest{
		Indices: []string{s.indexName},
		Body:    body,
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to list documents")
	}

	return s.parseHits(result)
}

func (s *Store) Count(ctx context.Context) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	count, err := s.client.Search().Count(ctx, []string{s.indexName}, nil)
	if err != nil {
		return 0, errors.Wrap(err, "failed to count documents")
	}

	return int(count), nil
}

func (s *Store) GetType() string {
	return "OpenSearch"
}

func (s *Store) IsCallbacksEnabled() bool {
	return true
}

func buildFilterQuery(filter map[string]any) (any, error) {
	if len(filter) == 0 {
		return querydsl.NewMatchAllQuery().Source()
	}

	must := make([]querydsl.Query, 0, len(filter))
	for k, v := range filter {
		must = append(must, querydsl.NewTermQuery(k, v))
	}

	return querydsl.NewBoolQuery().Must(must...).Source()
}

func withRetrieverIndex(indexName string, opts []retriever.Option) []retriever.Option {
	return append(
		[]retriever.Option{retriever.WithIndex(indexName)},
		opts...,
	)
}

func (s *Store) parseHits(result *querydsl.SearchResult) ([]*schema.Document, error) {
	if result.Hits == nil || len(result.Hits.Hits) == 0 {
		return nil, nil
	}

	docs := make([]*schema.Document, 0, len(result.Hits.Hits))
	for _, hit := range result.Hits.Hits {
		src := map[string]any{}
		if err := json.Unmarshal(hit.Source, &src); err != nil {
			return nil, errors.Wrap(err, "failed to unmarshal hit source")
		}

		content, _ := src[s.contentField].(string)
		meta := make(map[string]any, len(src))
		for k, v := range src {
			if k == s.contentField {
				continue
			}
			meta[k] = v
		}

		docs = append(docs, &schema.Document{
			ID:       hit.Id,
			Content:  content,
			MetaData: meta,
		})
	}

	return docs, nil
}
