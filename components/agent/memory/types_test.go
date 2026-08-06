package memory

import (
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"
)

func TestIsMemoryContext(t *testing.T) {
	assert.False(t, IsMemoryContext(nil))
	msg := schema.AssistantMessage("hi", nil)
	assert.False(t, IsMemoryContext(msg))
	ctxMsg := NewMemoryContextMessage("test memory")
	assert.True(t, IsMemoryContext(ctxMsg))
	assert.Equal(t, schema.System, ctxMsg.Role)
	assert.Equal(t, "test memory", ctxMsg.Content)
}

func TestNewMemoryContextMessage_AlwaysMarked(t *testing.T) {
	msg := NewMemoryContextMessage("hello")
	assert.True(t, IsMemoryContext(msg))
	assert.Equal(t, schema.System, msg.Role)
	assert.Equal(t, "hello", msg.Content)
}

func TestEntryToDocumentRoundTrip(t *testing.T) {
	now := time.Now()
	entry := &Entry{
		ID:        "id-1",
		Content:   "user prefers Go",
		Category:  CategoryPreference,
		Source:    SourceUser,
		SessionID: "session-1",
		CreatedAt: now,
		UpdatedAt: now.Add(time.Hour),
		Metadata:  map[string]any{"confidence": 0.95},
	}
	doc := entry.ToDocument()
	assert.Equal(t, "id-1", doc.ID)
	assert.Equal(t, "user prefers Go", doc.Content)
	assert.Equal(t, "preference", doc.MetaData["category"])
	assert.Equal(t, "user", doc.MetaData["source"])
	assert.Equal(t, "session-1", doc.MetaData["session_id"])
	assert.Equal(t, 0.95, doc.MetaData["confidence"])
	back := EntryFromDocument(doc)
	assert.Equal(t, entry.Content, back.Content)
	assert.Equal(t, entry.Category, back.Category)
	assert.Equal(t, entry.Source, back.Source)
	assert.Equal(t, entry.SessionID, back.SessionID)
	assert.WithinDuration(t, entry.CreatedAt, back.CreatedAt, time.Second)
	assert.WithinDuration(t, entry.UpdatedAt, back.UpdatedAt, time.Second)
	assert.Equal(t, 0.95, back.Metadata["confidence"])
}

func TestEntryFromDocument_MissingMetaData(t *testing.T) {
	doc := &schema.Document{ID: "id-1", Content: "test content"}
	entry := EntryFromDocument(doc)
	assert.Equal(t, "id-1", entry.ID)
	assert.Equal(t, "test content", entry.Content)
	assert.Empty(t, entry.Category)
	assert.Empty(t, entry.Source)
	assert.Empty(t, entry.SessionID)
}
