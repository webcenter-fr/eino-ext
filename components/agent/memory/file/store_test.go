package file

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	memoryagent "github.com/webcenter-fr/eino-ext/components/agent/memory"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewStore(Config{Dir: t.TempDir()})
	require.NoError(t, err)
	return s
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
	assert.Len(t, docs, 1)
	assert.Equal(t, "entry 2", docs[0].Content)
}

func TestStore_Retrieve(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	_, err := s.Store(ctx, []*schema.Document{
		{Content: "user likes Go programming language"},
		{Content: "project uses PostgreSQL database"},
	})
	require.NoError(t, err)

	docs, err := s.Retrieve(ctx, "Go")
	require.NoError(t, err)
	require.Len(t, docs, 1)
	assert.Contains(t, docs[0].Content, "Go")

	docs, err = s.Retrieve(ctx, "PostgreSQL")
	require.NoError(t, err)
	require.Len(t, docs, 1)
	assert.Contains(t, docs[0].Content, "PostgreSQL")

	docs, err = s.Retrieve(ctx, "Ruby")
	require.NoError(t, err)
	assert.Empty(t, docs)

	docs, err = s.Retrieve(ctx, "")
	require.NoError(t, err)
	assert.Len(t, docs, 2)
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

func TestStore_Persistence(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	s1, err := NewStore(Config{Dir: dir})
	require.NoError(t, err)

	_, err = s1.Store(ctx, []*schema.Document{
		{Content: "persistent entry", MetaData: map[string]any{"category": "fact"}},
	})
	require.NoError(t, err)

	s2, err := NewStore(Config{Dir: dir})
	require.NoError(t, err)

	docs, err := s2.List(ctx, 0, 10)
	require.NoError(t, err)
	assert.Len(t, docs, 1)
	assert.Equal(t, "persistent entry", docs[0].Content)
}

func TestStore_ListPagination(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	for i := 0; i < 10; i++ {
		_, err := s.Store(ctx, []*schema.Document{{Content: "entry"}})
		require.NoError(t, err)
	}

	docs, err := s.List(ctx, 0, 3)
	require.NoError(t, err)
	assert.Len(t, docs, 3)

	docs2, err := s.List(ctx, 5, 5)
	require.NoError(t, err)
	assert.Len(t, docs2, 5)
}

func TestStore_Concurrency(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	done := make(chan struct{})
	for range 5 {
		go func() {
			for range 20 {
				_, _ = s.Store(ctx, []*schema.Document{{Content: "concurrent"}})
				_, _ = s.List(ctx, 0, 100)
			}
			done <- struct{}{}
		}()
	}

	for range 5 {
		<-done
	}

	count, err := s.Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 100, count)
}

func TestStore_EmptyStore(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	docs, err := s.Retrieve(ctx, "query")
	require.NoError(t, err)
	assert.Empty(t, docs)

	count, err := s.Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, count)
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

func TestMatchesFilter_Metadata(t *testing.T) {
	entry := &memoryagent.Entry{
		Content:  "test",
		Metadata: map[string]any{"confidence": "high"},
	}
	assert.True(t, matchesFilter(entry, map[string]any{"confidence": "high"}))
	assert.False(t, matchesFilter(entry, map[string]any{"confidence": "low"}))
}

func TestMatchesFilter_NonStringValue(t *testing.T) {
	entry := &memoryagent.Entry{Category: "fact"}
	assert.False(t, matchesFilter(entry, map[string]any{"category": 42}))
}

func TestRetrieve_WithTopK(t *testing.T) {
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

func TestStore_ImplementsMemoryStore(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	var store memoryagent.MemoryStore = s
	_, err := store.Store(ctx, []*schema.Document{{Content: "test"}})
	require.NoError(t, err)
}
