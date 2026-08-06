package memory

import (
	"time"

	"github.com/cloudwego/eino/schema"

	memcore "github.com/webcenter-fr/eino-ext/components/memory"
)

const (
	// MemoryContextMarkerKey is the metadata key for memory context markers.
	MemoryContextMarkerKey = "__eino_ext_memory_context"

	// CategoryFact is the "fact" memory category.
	CategoryFact = "fact"
	// CategoryPreference is the "preference" memory category.
	CategoryPreference = "preference"
	// CategoryLearning is the "learning" memory category.
	CategoryLearning = "learning"
	// CategorySummary is the "summary" memory category.
	CategorySummary = "summary"

	// SourceUser indicates a memory sourced from user input.
	SourceUser = "user"
	// SourceAssistant indicates a memory sourced from assistant output.
	SourceAssistant = "assistant"
	// SourceObservation indicates a memory sourced from an observation.
	SourceObservation = "observation"
	// SourceSession indicates a memory sourced from a session summary.
	SourceSession = "session"
)

// Entry represents a single stored memory with category, source, and
// session tracking metadata.
type Entry struct {
	ID        string         `json:"id"`
	Content   string         `json:"content"`
	Category  string         `json:"category"`
	Source    string         `json:"source"`
	SessionID string         `json:"session_id"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

// ToDocument converts this Entry into a schema.Document suitable for
// storage and retrieval.
func (e *Entry) ToDocument() *schema.Document {
	doc := &schema.Document{
		ID:      e.ID,
		Content: e.Content,
		MetaData: map[string]any{
			"category":   e.Category,
			"source":     e.Source,
			"session_id": e.SessionID,
			"created_at": e.CreatedAt.Format(time.RFC3339),
			"updated_at": e.UpdatedAt.Format(time.RFC3339),
		},
	}
	for k, v := range e.Metadata {
		doc.MetaData[k] = v
	}
	return doc
}

// EntryFromDocument reconstructs a Entry from a schema.Document,
// parsing metadata fields back into the struct.
func EntryFromDocument(doc *schema.Document) *Entry {
	e := &Entry{
		ID:      doc.ID,
		Content: doc.Content,
	}
	if doc.MetaData == nil {
		return e
	}
	if v, ok := doc.MetaData["category"].(string); ok {
		e.Category = v
	}
	if v, ok := doc.MetaData["source"].(string); ok {
		e.Source = v
	}
	if v, ok := doc.MetaData["session_id"].(string); ok {
		e.SessionID = v
	}
	if v, ok := doc.MetaData["created_at"].(string); ok {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			e.CreatedAt = t
		}
	}
	if v, ok := doc.MetaData["updated_at"].(string); ok {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			e.UpdatedAt = t
		}
	}
	// Collect non-reserved metadata keys.
	extraCount := 0
	for k := range doc.MetaData {
		switch k {
		case "category", "source", "session_id", "created_at", "updated_at":
		default:
			extraCount++
		}
	}
	if extraCount > 0 {
		e.Metadata = make(map[string]any, extraCount)
		for k, v := range doc.MetaData {
			switch k {
			case "category", "source", "session_id", "created_at", "updated_at":
			default:
				e.Metadata[k] = v
			}
		}
	}
	return e
}

// IsMemoryContext reports whether the given message is a memory context marker.
func IsMemoryContext(msg *schema.Message) bool {
	return memcore.HasBoolMarker(msg, MemoryContextMarkerKey)
}

// NewMemoryContextMessage creates a system message marked as memory context.
// schema.SystemMessage does not initialize Extra, so we must create it.
func NewMemoryContextMessage(content string) *schema.Message {
	msg := schema.SystemMessage(content)
	memcore.SetBoolMarker(msg, MemoryContextMarkerKey)
	return msg
}
