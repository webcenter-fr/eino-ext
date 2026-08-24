package opensearch

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/components/embedding"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubEmbedder struct{}

func (s stubEmbedder) EmbedStrings(ctx context.Context, texts []string, opts ...embedding.Option) ([][]float64, error) {
	return nil, nil
}

// matchBody extracts the BM25 match body for the given field from a search body.
func matchBody(body map[string]any, field string) map[string]any {
	return body["query"].(map[string]any)["match"].(map[string]any)[field].(map[string]any)
}

func TestBuildSearchBody_DefaultOperatorIsOr(t *testing.T) {
	r := &Retriever{config: Config{ContentField: "content", Operator: "or"}}
	body := r.buildSearchBody("long multi sentence query", 10, nil)

	match := matchBody(body, "content")
	assert.Equal(t, "or", match["operator"])
	assert.Equal(t, "long multi sentence query", match["query"])
	assert.Equal(t, 10, body["size"])
}

func TestBuildSearchBody_ExplicitAndOperator(t *testing.T) {
	r := &Retriever{config: Config{ContentField: "content", Operator: "and"}}
	body := r.buildSearchBody("test query", 5, nil)

	assert.Equal(t, "and", matchBody(body, "content")["operator"])
}

func TestBuildSearchBody_MinimumShouldMatch(t *testing.T) {
	t.Run("set", func(t *testing.T) {
		r := &Retriever{config: Config{ContentField: "content", Operator: "or", MinimumShouldMatch: "2<70%"}}
		body := r.buildSearchBody("test", 10, nil)

		assert.Equal(t, "2<70%", matchBody(body, "content")["minimum_should_match"])
	})

	t.Run("unset", func(t *testing.T) {
		r := &Retriever{config: Config{ContentField: "content", Operator: "or", MinimumShouldMatch: ""}}
		body := r.buildSearchBody("test", 10, nil)

		_, ok := matchBody(body, "content")["minimum_should_match"]
		assert.False(t, ok)
	})
}

func TestBuildSearchBody_MinScore(t *testing.T) {
	t.Run("set", func(t *testing.T) {
		ms := 0.5
		r := &Retriever{config: Config{ContentField: "content", Operator: "or", MinScore: &ms}}
		body := r.buildSearchBody("test", 10, nil)

		assert.Equal(t, 0.5, body["min_score"])
	})

	t.Run("nil", func(t *testing.T) {
		r := &Retriever{config: Config{ContentField: "content", Operator: "or", MinScore: nil}}
		body := r.buildSearchBody("test", 10, nil)

		_, ok := body["min_score"]
		assert.False(t, ok)
	})

	t.Run("explicit_zero", func(t *testing.T) {
		ms := 0.0
		r := &Retriever{config: Config{ContentField: "content", Operator: "or", MinScore: &ms}}
		body := r.buildSearchBody("test", 10, nil)

		assert.Equal(t, 0.0, body["min_score"])
	})
}

func TestBuildSearchBody_HybridUnchanged(t *testing.T) {
	r := &Retriever{config: Config{
		Embedding:    stubEmbedder{},
		VectorField:  "vector",
		ContentField: "content",
		Hybrid:       true,
		K:            5,
	}}
	body := r.buildSearchBody("test", 10, [][]float64{{0.1, 0.2}})

	boolQuery := body["query"].(map[string]any)["bool"].(map[string]any)
	should := boolQuery["should"].([]any)
	require.Len(t, should, 2)

	_, hasKnn := should[0].(map[string]any)["knn"]
	assert.True(t, hasKnn)

	matchContent := should[1].(map[string]any)["match"].(map[string]any)["content"]
	assert.Equal(t, "test", matchContent)

	assert.Equal(t, 10, body["size"])
}

func TestBuildSearchBody_SearchPipeline(t *testing.T) {
	r := &Retriever{config: Config{ContentField: "content", Operator: "or", SearchPipeline: "rrf"}}
	body := r.buildSearchBody("test", 10, nil)

	assert.Equal(t, "rrf", body["search_pipeline"])
}

func TestBuildSearchBody_AllFieldsCombined(t *testing.T) {
	ms := 0.5
	r := &Retriever{config: Config{
		ContentField:       "content",
		Operator:           "or",
		SearchPipeline:     "rrf",
		MinScore:           &ms,
		MinimumShouldMatch: "1",
	}}
	body := r.buildSearchBody("test", 10, nil)

	assert.Equal(t, "rrf", body["search_pipeline"])
	assert.Equal(t, 0.5, body["min_score"])

	match := matchBody(body, "content")
	assert.Equal(t, "or", match["operator"])
	assert.Equal(t, "1", match["minimum_should_match"])
	assert.Equal(t, "test", match["query"])
}
