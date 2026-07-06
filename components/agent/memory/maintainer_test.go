package memory

import (
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"
)

func TestTextSimilarity(t *testing.T) {
	assert.Equal(t, 1.0, textSimilarity("hello world", "hello world"))
	assert.Equal(t, 0.0, textSimilarity("hello", "world"))
	sim := textSimilarity("user prefers Go for backend", "user uses Go for backend")
	assert.Greater(t, sim, 0.5)
}

func TestTokenize(t *testing.T) {
	words := tokenize("Hello, World! 123")
	assert.Equal(t, []string{"hello", "world", "123"}, words)
	words = tokenize("")
	assert.Empty(t, words)
}

func TestGroupBySimilarity(t *testing.T) {
	docs := []*schema.Document{
		{ID: "1", Content: "user prefers Go", MetaData: map[string]any{"category": "preference"}},
		{ID: "2", Content: "user likes Go language", MetaData: map[string]any{"category": "preference"}},
		{ID: "3", Content: "project uses PostgreSQL", MetaData: map[string]any{"category": "fact"}},
	}
	groups := groupBySimilarity(docs, 0.3)
	assert.Len(t, groups, 2)
	foundPrefCluster := false
	for _, group := range groups {
		if len(group) == 2 && group[0].ID == "1" && group[1].ID == "2" {
			foundPrefCluster = true
			break
		}
	}
	assert.True(t, foundPrefCluster, "expected preference docs to be clustered together")
}

func TestGroupBySimilarity_SingleDoc(t *testing.T) {
	docs := []*schema.Document{{ID: "1", Content: "only one"}}
	groups := groupBySimilarity(docs, 0.5)
	assert.Len(t, groups, 1)
	assert.Len(t, groups[0], 1)
}

func TestGroupBySimilarity_Empty(t *testing.T) {
	groups := groupBySimilarity(nil, 0.5)
	assert.Empty(t, groups)
}

func TestClusterByTextOverlap(t *testing.T) {
	docs := []*schema.Document{
		{ID: "1", Content: "identical content"},
		{ID: "2", Content: "identical content"},
		{ID: "3", Content: "different text here"},
	}
	clusters := clusterByTextOverlap(docs, 0.8)
	assert.Len(t, clusters, 2)
	var idsInFirstCluster []string
	for _, d := range clusters[0] {
		idsInFirstCluster = append(idsInFirstCluster, d.ID)
	}
	if len(idsInFirstCluster) == 2 {
		assert.Contains(t, idsInFirstCluster, "1")
		assert.Contains(t, idsInFirstCluster, "2")
	}
}

func TestCollectIDs(t *testing.T) {
	ids := collectIDs([]*schema.Document{{ID: "a"}, {ID: "b"}, {ID: "c"}})
	assert.Equal(t, []string{"a", "b", "c"}, ids)
}

func TestRemoveFromOrder(t *testing.T) {
	order := []string{"a", "b", "c", "d"}
	result := removeFromOrder(append([]string{}, order...), "c")
	assert.Equal(t, []string{"a", "b", "d"}, result)
	result = removeFromOrder(append([]string{}, order...), "x")
	assert.Equal(t, []string{"a", "b", "c", "d"}, result)
}

func removeFromOrder(order []string, id string) []string {
	result := make([]string, 0, len(order))
	for _, v := range order {
		if v != id {
			result = append(result, v)
		}
	}
	return result
}
