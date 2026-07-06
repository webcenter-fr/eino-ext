package sizecap

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"
)

func TestNewSplitter(t *testing.T) {
	splitter, err := NewSplitter(context.Background(), nil)
	assert.NoError(t, err)
	assert.NotNil(t, splitter)
}

func TestTransform_ShortContent(t *testing.T) {
	splitter, err := NewSplitter(context.Background(), &Config{
		ChunkSize:    1000,
		ChunkOverlap: 200,
	})
	assert.NoError(t, err)

	docs := []*schema.Document{
		{ID: "1", Content: "short content here", MetaData: map[string]any{"key": "val"}},
	}

	result, err := splitter.Transform(context.Background(), docs)
	assert.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Equal(t, "short content here", result[0].Content)
	assert.Equal(t, "val", result[0].MetaData["key"])
}

func TestTransform_LongContentSplit(t *testing.T) {
	splitter, err := NewSplitter(context.Background(), &Config{
		ChunkSize:    10,
		ChunkOverlap: 0,
	})
	assert.NoError(t, err)

	longContent := strings.Repeat("abcdefghij", 5) // 50 chars
	docs := []*schema.Document{
		{ID: "1", Content: longContent},
	}

	result, err := splitter.Transform(context.Background(), docs)
	assert.NoError(t, err)
	assert.GreaterOrEqual(t, len(result), 5)
}

func TestTransform_UTF8NotSplitMidRune(t *testing.T) {
	splitter, err := NewSplitter(context.Background(), &Config{
		ChunkSize:    5,
		ChunkOverlap: 0,
	})
	assert.NoError(t, err)

	// UTF-8 with multi-byte characters
	content := "Héllo世界привет"
	docs := []*schema.Document{
		{ID: "1", Content: content},
	}

	result, err := splitter.Transform(context.Background(), docs)
	assert.NoError(t, err)

	for _, chunk := range result {
		assert.True(t, utf8.ValidString(chunk.Content), "chunk must be valid UTF-8: %q", chunk.Content)
	}
}

func TestTransform_EmptyDocs(t *testing.T) {
	splitter, err := NewSplitter(context.Background(), &Config{
		ChunkSize:    100,
		ChunkOverlap: 10,
	})
	assert.NoError(t, err)

	result, err := splitter.Transform(context.Background(), nil)
	assert.NoError(t, err)
	assert.Nil(t, result)

	result, err = splitter.Transform(context.Background(), []*schema.Document{})
	assert.NoError(t, err)
	assert.Empty(t, result)
}

func TestTransform_Overlap(t *testing.T) {
	splitter, err := NewSplitter(context.Background(), &Config{
		ChunkSize:    10,
		ChunkOverlap: 3,
	})
	assert.NoError(t, err)

	content := "abcdefghijklmnopqrstuvwxyz" // 26 chars -> should produce 3+ chunks
	docs := []*schema.Document{
		{ID: "1", Content: content},
	}

	result, err := splitter.Transform(context.Background(), docs)
	assert.NoError(t, err)
	assert.GreaterOrEqual(t, len(result), 3)

	// Check overlap: first chunk's end should align with second chunk's start
	if len(result) >= 2 {
		r1 := []rune(result[0].Content)
		r2 := []rune(result[1].Content)
		// The last 3 runes of chunk 1 should match the first 3 runes of chunk 2
		if len(r1) >= 3 && len(r2) >= 3 {
			assert.Equal(t, string(r1[len(r1)-3:]), string(r2[:3]),
				"expected overlap between consecutive chunks")
		}
	}
}
