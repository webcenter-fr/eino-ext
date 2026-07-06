package memory

import (
	"time"

	"github.com/cloudwego/eino/schema"
)

const (
	MemoryContextMarkerKey = "__eino_ext_memory_context"

	CategoryFact       = "fact"
	CategoryPreference = "preference"
	CategoryLearning   = "learning"
	CategorySummary    = "summary"

	SourceUser       = "user"
	SourceAssistant  = "assistant"
	SourceObservation = "observation"
	SourceSession    = "session"
)

type MemoryEntry struct {
	ID        string         `json:"id"`
	Content   string         `json:"content"`
	Category  string         `json:"category"`
	Source    string         `json:"source"`
	SessionID string         `json:"session_id"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

func (e *MemoryEntry) ToDocument() *schema.Document {
	doc := &schema.Document{
		ID:      e.ID,
		Content: e.Content,
		MetaData: map[string]any{
			"category":    e.Category,
			"source":      e.Source,
			"session_id":  e.SessionID,
			"created_at":  e.CreatedAt.Format(time.RFC3339),
			"updated_at":  e.UpdatedAt.Format(time.RFC3339),
		},
	}
	for k, v := range e.Metadata {
		doc.MetaData[k] = v
	}
	return doc
}

func EntryFromDocument(doc *schema.Document) *MemoryEntry {
	e := &MemoryEntry{
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

func IsMemoryContext(msg *schema.Message) bool {
	if msg == nil || msg.Extra == nil {
		return false
	}
	v, ok := msg.Extra[MemoryContextMarkerKey]
	if !ok {
		return false
	}
	b, _ := v.(bool)
	return b
}

// NewMemoryContextMessage creates a system message marked as memory context.
// schema.SystemMessage does not initialize Extra, so we must create it.
func NewMemoryContextMessage(content string) *schema.Message {
	msg := schema.SystemMessage(content)
	if msg.Extra == nil {
		msg.Extra = make(map[string]any)
	}
	msg.Extra[MemoryContextMarkerKey] = true
	return msg
}
