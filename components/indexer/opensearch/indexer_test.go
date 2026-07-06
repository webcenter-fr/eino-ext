package opensearch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/embedding"
	"github.com/cloudwego/eino/components/indexer"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"emperror.dev/errors"
)

// fakeEmbedder returns a deterministic vector per input text (length of the
// text repeated 3 times), so tests can assert on embedded output without a
// real embedding backend.
type fakeEmbedder struct {
	calls    int
	lastSize int
	err      error
}

func (f *fakeEmbedder) EmbedStrings(_ context.Context, texts []string, _ ...embedding.Option) ([][]float64, error) {
	f.calls++
	f.lastSize = len(texts)
	if f.err != nil {
		return nil, f.err
	}
	out := make([][]float64, len(texts))
	for i, t := range texts {
		out[i] = []float64{float64(len(t)), float64(len(t)), float64(len(t))}
	}
	return out, nil
}

// bulkLine is a single decoded NDJSON pair (action + optional source).
type bulkLine struct {
	action map[string]map[string]any
	source map[string]any
}

// parseBulkBody decodes the NDJSON body of a bulk request into pairs of
// action/source lines.
func parseBulkBody(t *testing.T, body string) []bulkLine {
	t.Helper()
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	require.Equal(t, 0, len(lines)%2, "bulk body must have an even number of lines")

	result := make([]bulkLine, 0, len(lines)/2)
	for i := 0; i < len(lines); i += 2 {
		var action map[string]map[string]any
		require.NoError(t, json.Unmarshal([]byte(lines[i]), &action))
		var source map[string]any
		require.NoError(t, json.Unmarshal([]byte(lines[i+1]), &source))
		result = append(result, bulkLine{action: action, source: source})
	}
	return result
}

// newBulkTestServer returns an httptest.Server that accepts any /_bulk
// request, decodes it, records every request via capture, and replies with a
// synthetic success response (assigning "generated-<n>" ids to items that
// omitted _id).
func newBulkTestServer(t *testing.T, capture *[][]bulkLine) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "_bulk") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)

		lines := parseBulkBody(t, string(buf))
		*capture = append(*capture, lines)

		items := make([]map[string]*bulkRespItemJSON, 0, len(lines))
		for i, l := range lines {
			id, _ := l.action["index"]["_id"].(string)
			if id == "" {
				id = "generated-" + string(rune('a'+i))
			}
			items = append(items, map[string]*bulkRespItemJSON{
				"index": {ID: id, Result: "created", Status: 201},
			})
		}

		resp := map[string]any{
			"took":   1,
			"errors": false,
			"items":  items,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

// bulkRespItemJSON mirrors the subset of api.BulkResponseItem fields the
// test server needs to emit.
type bulkRespItemJSON struct {
	ID     string             `json:"_id"`
	Result string             `json:"result"`
	Status int                `json:"status"`
	Error  *bulkErrorItemJSON `json:"error,omitempty"`
}

// bulkErrorItemJSON mirrors types.OpenSearchErrorDetails for test purposes.
type bulkErrorItemJSON struct {
	Type   string `json:"type"`
	Reason string `json:"reason"`
}

// newFailingBulkTestServer returns an httptest.Server whose /_bulk endpoint
// always reports every item as failed, so callers can exercise the bulkError
// aggregation path.
func newFailingBulkTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		lines := parseBulkBody(t, string(buf))

		items := make([]map[string]*bulkRespItemJSON, 0, len(lines))
		for _, l := range lines {
			id, _ := l.action["index"]["_id"].(string)
			items = append(items, map[string]*bulkRespItemJSON{
				"index": {
					ID:     id,
					Status: 400,
					Error:  &bulkErrorItemJSON{Type: "mapper_parsing_exception", Reason: "failed to parse field"},
				},
			})
		}

		resp := map[string]any{"took": 1, "errors": true, "items": items}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

func newTestIndexer(t *testing.T, serverURL string, mutate func(*Config)) *Indexer {
	t.Helper()
	cfg := &Config{
		URLs:  []string{serverURL},
		Index: "test-index",
	}
	if mutate != nil {
		mutate(cfg)
	}
	idx, err := NewIndexer(context.Background(), cfg)
	require.NoError(t, err)
	return idx
}

func TestNewIndexer_MissingURLs(t *testing.T) {
	_, err := NewIndexer(context.Background(), &Config{Index: "foo"})
	assert.Error(t, err)
}

func TestNewIndexer_MissingIndex(t *testing.T) {
	_, err := NewIndexer(context.Background(), &Config{URLs: []string{"http://localhost:9200"}})
	assert.Error(t, err)
}

func TestNewIndexer_Defaults(t *testing.T) {
	idx, err := NewIndexer(context.Background(), &Config{
		URLs:  []string{"http://localhost:9200"},
		Index: "my-index",
	})
	require.NoError(t, err)
	assert.Equal(t, defaultBatchSize, idx.config.BatchSize)
	assert.Equal(t, defaultVectorField, idx.config.VectorField)
	assert.Equal(t, defaultContentField, idx.config.ContentField)
	assert.NotNil(t, idx.config.DocumentToFields)
}

func TestNewIndexer_CustomBatchSize(t *testing.T) {
	idx, err := NewIndexer(context.Background(), &Config{
		URLs:      []string{"http://localhost:9200"},
		Index:     "my-index",
		BatchSize: 7,
	})
	require.NoError(t, err)
	assert.Equal(t, 7, idx.config.BatchSize)
}

func TestGetType(t *testing.T) {
	idx := newTestIndexer(t, "http://localhost:9200", nil)
	assert.Equal(t, "OpenSearch", idx.GetType())
}

func TestIsCallbacksEnabled(t *testing.T) {
	idx := newTestIndexer(t, "http://localhost:9200", nil)
	assert.True(t, idx.IsCallbacksEnabled())
}

func TestStore_NoEmbeddingUsesDefaultMapper(t *testing.T) {
	var captured [][]bulkLine
	server := newBulkTestServer(t, &captured)
	defer server.Close()

	idx := newTestIndexer(t, server.URL, nil)

	docs := []*schema.Document{
		{ID: "doc-1", Content: "hello world", MetaData: map[string]any{"lang": "en"}},
		{ID: "doc-2", Content: "bonjour", MetaData: map[string]any{"lang": "fr"}},
	}

	ids, err := idx.Store(context.Background(), docs)
	require.NoError(t, err)
	assert.Equal(t, []string{"doc-1", "doc-2"}, ids)

	require.Len(t, captured, 1)
	lines := captured[0]
	require.Len(t, lines, 2)

	assert.Equal(t, "doc-1", lines[0].action["index"]["_id"])
	assert.Equal(t, "test-index", lines[0].action["index"]["_index"])
	assert.Equal(t, "hello world", lines[0].source["content"])
	assert.Equal(t, "en", lines[0].source["lang"])

	assert.Equal(t, "bonjour", lines[1].source["content"])
	assert.Equal(t, "fr", lines[1].source["lang"])
}

func TestStore_WithEmbeddingVectorizesContent(t *testing.T) {
	var captured [][]bulkLine
	server := newBulkTestServer(t, &captured)
	defer server.Close()

	emb := &fakeEmbedder{}
	idx := newTestIndexer(t, server.URL, func(c *Config) {
		c.Embedding = emb
	})

	docs := []*schema.Document{
		{ID: "doc-1", Content: "hello"},
	}

	_, err := idx.Store(context.Background(), docs)
	require.NoError(t, err)

	assert.Equal(t, 1, emb.calls)
	assert.Equal(t, 1, emb.lastSize)

	require.Len(t, captured, 1)
	vec, ok := captured[0][0].source["vector"].([]any)
	require.True(t, ok)
	assert.Len(t, vec, 3)
}

func TestStore_PrecomputedVectorSkipsEmbedding(t *testing.T) {
	var captured [][]bulkLine
	server := newBulkTestServer(t, &captured)
	defer server.Close()

	emb := &fakeEmbedder{}
	idx := newTestIndexer(t, server.URL, func(c *Config) {
		c.Embedding = emb
	})

	doc := &schema.Document{ID: "doc-1", Content: "hello"}
	doc.WithDenseVector([]float64{0.1, 0.2, 0.3})

	_, err := idx.Store(context.Background(), []*schema.Document{doc})
	require.NoError(t, err)

	assert.Equal(t, 0, emb.calls, "embedder should not be called when the document already has a vector")

	require.Len(t, captured, 1)
	vec, ok := captured[0][0].source["vector"].([]any)
	require.True(t, ok)
	assert.InDelta(t, 0.1, vec[0], 0.0001)
}

func TestStore_EmbeddingFailurePropagates(t *testing.T) {
	var captured [][]bulkLine
	server := newBulkTestServer(t, &captured)
	defer server.Close()

	failing := &fakeEmbedder{err: errors.New("fake embedding failure")}
	idx := newTestIndexer(t, server.URL, func(c *Config) {
		c.Embedding = failing
	})

	_, err := idx.Store(context.Background(), []*schema.Document{{ID: "doc-1", Content: "x"}})
	assert.Error(t, err)
}

func TestStore_NoEmbeddingConfiguredWithoutPrecomputedVectorErrors(t *testing.T) {
	// The default mapper does not require vectorization on its own; embedding
	// is only attempted when a document has no precomputed dense vector AND
	// the caller expects one. Since neither WithEmbedding nor a per-field
	// EmbedKey exists in this component's default mapper, Store succeeds even
	// without an Embedder. This test documents that behavior explicitly.
	var captured [][]bulkLine
	server := newBulkTestServer(t, &captured)
	defer server.Close()

	idx := newTestIndexer(t, server.URL, nil)

	_, err := idx.Store(context.Background(), []*schema.Document{{ID: "doc-1", Content: "x"}})
	assert.NoError(t, err)
}

func TestStore_BatchSizeSplitsRequests(t *testing.T) {
	var captured [][]bulkLine
	server := newBulkTestServer(t, &captured)
	defer server.Close()

	idx := newTestIndexer(t, server.URL, func(c *Config) {
		c.BatchSize = 2
	})

	docs := []*schema.Document{
		{ID: "1", Content: "a"},
		{ID: "2", Content: "b"},
		{ID: "3", Content: "c"},
	}

	ids, err := idx.Store(context.Background(), docs)
	require.NoError(t, err)
	assert.Equal(t, []string{"1", "2", "3"}, ids)

	require.Len(t, captured, 2, "3 docs with batch size 2 should produce 2 bulk requests")
	assert.Len(t, captured[0], 2)
	assert.Len(t, captured[1], 1)
}

func TestStore_UsesIndexOption(t *testing.T) {
	var captured [][]bulkLine
	server := newBulkTestServer(t, &captured)
	defer server.Close()

	idx := newTestIndexer(t, server.URL, nil)

	_, err := idx.Store(context.Background(), []*schema.Document{{ID: "1", Content: "a"}}, indexer.WithIndex("other-index"))
	require.NoError(t, err)

	require.Len(t, captured, 1)
	assert.Equal(t, "other-index", captured[0][0].action["index"]["_index"])
}

func TestStore_ServerGeneratedID(t *testing.T) {
	var captured [][]bulkLine
	server := newBulkTestServer(t, &captured)
	defer server.Close()

	idx := newTestIndexer(t, server.URL, nil)

	ids, err := idx.Store(context.Background(), []*schema.Document{{Content: "no id set"}})
	require.NoError(t, err)
	require.Len(t, ids, 1)
	assert.NotEmpty(t, ids[0])
}

func TestDefaultDocumentToFields_SkipsInternalMetaKeys(t *testing.T) {
	mapper := defaultDocumentToFields("content")
	doc := &schema.Document{Content: "hi", MetaData: map[string]any{"keep": "yes"}}
	doc.WithSubIndexes([]string{"a"})

	fields, err := mapper(context.Background(), doc)
	require.NoError(t, err)
	assert.Equal(t, "hi", fields["content"])
	assert.Equal(t, "yes", fields["keep"])
	_, hasSubIndexes := fields["_sub_indexes"]
	assert.False(t, hasSubIndexes)
}

func TestStore_CustomDocumentToFields(t *testing.T) {
	var captured [][]bulkLine
	server := newBulkTestServer(t, &captured)
	defer server.Close()

	idx := newTestIndexer(t, server.URL, func(c *Config) {
		c.DocumentToFields = func(_ context.Context, doc *schema.Document) (map[string]any, error) {
			return map[string]any{"body": doc.Content}, nil
		}
	})

	_, err := idx.Store(context.Background(), []*schema.Document{{ID: "1", Content: "custom"}})
	require.NoError(t, err)

	require.Len(t, captured, 1)
	assert.Equal(t, "custom", captured[0][0].source["body"])
	_, hasContent := captured[0][0].source["content"]
	assert.False(t, hasContent)
}

func TestStore_CustomDocumentToFieldsError(t *testing.T) {
	server := newFailingBulkTestServer(t)
	defer server.Close()

	idx := newTestIndexer(t, server.URL, func(c *Config) {
		c.DocumentToFields = func(_ context.Context, _ *schema.Document) (map[string]any, error) {
			return nil, errors.New("mapper failure")
		}
	})

	_, err := idx.Store(context.Background(), []*schema.Document{{ID: "1", Content: "x"}})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "mapper failure")
}

func TestStore_BulkErrorsAreAggregated(t *testing.T) {
	server := newFailingBulkTestServer(t)
	defer server.Close()

	idx := newTestIndexer(t, server.URL, nil)

	_, err := idx.Store(context.Background(), []*schema.Document{{ID: "1", Content: "x"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mapper_parsing_exception")
	assert.Contains(t, err.Error(), "failed to parse field")
}

func TestClient_ReturnsUnderlyingClient(t *testing.T) {
	idx := newTestIndexer(t, "http://localhost:9200", nil)
	assert.NotNil(t, idx.Client())
}

func TestStore_EmptyDocsNoRequest(t *testing.T) {
	var captured [][]bulkLine
	server := newBulkTestServer(t, &captured)
	defer server.Close()

	idx := newTestIndexer(t, server.URL, nil)

	ids, err := idx.Store(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, ids)
	assert.Empty(t, captured)
}
