package opensearch

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	memoryagent "github.com/webcenter-fr/eino-ext/components/agent/memory"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
)

func getTestConfig() *Config {
	return &Config{
		URLs: []string{os.Getenv("OPENSEARCH_TEST_URL")},
	}
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	cfg := getTestConfig()
	if cfg.URLs[0] == "" {
		t.Skip("OPENSEARCH_TEST_URL is not set")
	}
	store, err := NewStore(context.Background(), cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = store.client.Document().DeleteByQuery(ctx, []string{store.indexName}, map[string]any{
			"query": map[string]any{"match_all": map[string]any{}},
		})
	})
	return store
}

func TestConfigValidation(t *testing.T) {
	tests := []struct {
		name    string
		setup   func() *Config
		wantErr bool
	}{
		{
			name: "nil config has no URLs",
			setup: func() *Config {
				cfg := &Config{}
				applyDefaults(cfg)
				return cfg
			},
			wantErr: true,
		},
		{
			name: "valid minimal config",
			setup: func() *Config {
				cfg := &Config{URLs: []string{"http://localhost:9200"}}
				applyDefaults(cfg)
				return cfg
			},
			wantErr: false,
		},
		{
			name: "batch size zero gets default",
			setup: func() *Config {
				cfg := &Config{
					URLs:      []string{"http://localhost:9200"},
					BatchSize: 0,
				}
				applyDefaults(cfg)
				return cfg
			},
			wantErr: false,
		},
		{
			name: "negative batch size invalid",
			setup: func() *Config {
				cfg := &Config{
					URLs:      []string{"http://localhost:9200"},
					BatchSize: -1,
				}
				applyDefaults(cfg)
				return cfg
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := tt.setup()
			err := validate.Struct(cfg)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func applyDefaults(cfg *Config) {
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
	if cfg.VectorDimension == 0 {
		cfg.VectorDimension = 384
	}
}

func TestBuildFilterQuery(t *testing.T) {
	tests := []struct {
		name    string
		filter  map[string]any
		wantErr bool
	}{
		{"empty filter", nil, false},
		{"empty map", map[string]any{}, false},
		{"single term", map[string]any{"category": "fact"}, false},
		{"multiple terms", map[string]any{"category": "fact", "source": "user"}, false},
		{"with session_id", map[string]any{"session_id": "abc123"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			query, err := buildFilterQuery(tt.filter)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, query)
			}
		})
	}
}

func TestStore_ImplementsMemoryStore(t *testing.T) {
	s := newTestStore(t)
	var _ memoryagent.MemoryStore = s
}

func TestStore_CRUD(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	ids, err := s.Store(ctx, []*schema.Document{
		{Content: "entry 1", MetaData: map[string]any{"category": "fact"}},
		{Content: "entry 2", MetaData: map[string]any{"category": "preference"}},
	})
	require.NoError(t, err)
	require.Len(t, ids, 2)

	count, err := s.Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, count)

	err = s.Delete(ctx, ids[0])
	require.NoError(t, err)

	count, err = s.Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	docs, err := s.List(ctx, 0, 10)
	require.NoError(t, err)
	require.Len(t, docs, 1)
	assert.Equal(t, "entry 2", docs[0].Content)
}

func TestStore_DeleteNonExistent(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	err := s.Delete(ctx, "nonexistent")
	require.NoError(t, err)
}

func TestStore_DeleteByFilter(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	_, err := s.Store(ctx, []*schema.Document{
		{Content: "fact 1", MetaData: map[string]any{"category": "fact"}},
		{Content: "fact 2", MetaData: map[string]any{"category": "fact"}},
		{Content: "pref 1", MetaData: map[string]any{"category": "preference"}},
	})
	require.NoError(t, err)

	deleted, err := s.DeleteByFilter(ctx, map[string]any{"category": "fact"})
	require.NoError(t, err)
	assert.Equal(t, 2, deleted)

	docs, err := s.List(ctx, 0, 10)
	require.NoError(t, err)
	require.Len(t, docs, 1)
	assert.Equal(t, "pref 1", docs[0].Content)
}

func TestStore_ListPagination(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	var docs []*schema.Document
	for i := 0; i < 10; i++ {
		docs = append(docs, &schema.Document{Content: "entry", MetaData: map[string]any{"index": i}})
	}
	_, err := s.Store(ctx, docs)
	require.NoError(t, err)

	result, err := s.List(ctx, 0, 3)
	require.NoError(t, err)
	assert.Len(t, result, 3)

	result2, err := s.List(ctx, 5, 5)
	require.NoError(t, err)
	assert.Len(t, result2, 5)
}

func TestStore_EmptyStore(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	count, err := s.Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, count)

	docs, err := s.List(ctx, 0, 10)
	require.NoError(t, err)
	assert.Empty(t, docs)
}

func TestStore_IDGeneration(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	ids, err := s.Store(ctx, []*schema.Document{
		{Content: "no id"},
		{ID: "custom-id", Content: "has id"},
	})
	require.NoError(t, err)
	require.Len(t, ids, 2)

	assert.NotEmpty(t, ids[0])
	assert.NotEqual(t, ids[1], ids[0])
	assert.Equal(t, "custom-id", ids[1])
}

func TestStore_Retrieve(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	_, err := s.Store(ctx, []*schema.Document{
		{Content: "user likes Go programming language"},
		{Content: "project uses PostgreSQL database"},
	})
	require.NoError(t, err)

	docs, err := s.Retrieve(ctx, "Go", retriever.WithTopK(5))
	require.NoError(t, err)
	require.NotEmpty(t, docs)

	docs, err = s.Retrieve(ctx, "", retriever.WithTopK(5))
	require.NoError(t, err)
	assert.Len(t, docs, 2)
}

func TestStore_RetrieveWithTopK(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	_, err := s.Store(ctx, []*schema.Document{
		{Content: "Go programming"},
		{Content: "Go testing"},
		{Content: "Go deployment"},
	})
	require.NoError(t, err)

	docs, err := s.Retrieve(ctx, "Go", retriever.WithTopK(2))
	require.NoError(t, err)
	assert.Len(t, docs, 2)
}

func TestStore_GetType(t *testing.T) {
	s := newTestStore(t)

	assert.Equal(t, "OpenSearch", s.GetType())
	assert.True(t, s.IsCallbacksEnabled())
}

func TestStore_DeleteByFilterMultipleKeys(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	_, err := s.Store(ctx, []*schema.Document{
		{Content: "a1", MetaData: map[string]any{"category": "fact", "source": "user"}},
		{Content: "a2", MetaData: map[string]any{"category": "fact", "source": "assistant"}},
		{Content: "b1", MetaData: map[string]any{"category": "preference", "source": "user"}},
	})
	require.NoError(t, err)

	deleted, err := s.DeleteByFilter(ctx, map[string]any{
		"category": "fact",
		"source":   "user",
	})
	require.NoError(t, err)
	assert.Equal(t, 1, deleted)

	docs, err := s.List(ctx, 0, 10)
	require.NoError(t, err)
	assert.Len(t, docs, 2)
}

func TestConfig_OperatorFields(t *testing.T) {
	ms := 0.5
	cfg := &Config{
		URLs:               []string{"http://localhost:9200"},
		Operator:           "or",
		MinimumShouldMatch: "2<70%",
		MinScore:           &ms,
	}
	applyDefaults(cfg)
	err := validate.Struct(cfg)
	assert.NoError(t, err)
}

func TestConfig_OperatorInvalid(t *testing.T) {
	cfg := &Config{
		URLs:     []string{"http://localhost:9200"},
		Operator: "invalid",
	}
	applyDefaults(cfg)
	err := validate.Struct(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Operator")
}

func TestConfig_MinScoreNegative(t *testing.T) {
	// validate.Struct accepts any non-nil *float64 (tag is omitempty only).
	// The >= 0 check is performed in NewStore.
	ms := -1.0
	cfg := &Config{
		URLs:     []string{"http://localhost:9200"},
		MinScore: &ms,
	}
	applyDefaults(cfg)
	err := validate.Struct(cfg)
	assert.NoError(t, err)
}

func TestConfig_VectorDimensionDefault(t *testing.T) {
	cfg := &Config{
		URLs: []string{"http://localhost:9200"},
	}
	applyDefaults(cfg)
	assert.Equal(t, 384, cfg.VectorDimension)
}

func TestConfig_VectorDimensionNegative(t *testing.T) {
	cfg := &Config{
		URLs:            []string{"http://localhost:9200"},
		VectorDimension: -1,
	}
	applyDefaults(cfg)
	err := validate.Struct(cfg)
	assert.Error(t, err)
}
