package memory

import "github.com/cloudwego/eino/schema"

// SummaryMarkerKey is the key used in schema.Message.Extra to mark a message as a summary.
const SummaryMarkerKey = "__eino_ext_memory_summary"

// IsSummary returns true if the message is a summary message.
func IsSummary(msg *schema.Message) bool {
	if msg == nil || msg.Extra == nil {
		return false
	}
	v, ok := msg.Extra[SummaryMarkerKey]
	if !ok {
		return false
	}
	b, ok := v.(bool)
	return ok && b
}

// NewSummaryMessage creates a new Assistant message marked as a summary.
func NewSummaryMessage(content string) *schema.Message {
	return &schema.Message{
		Role:    schema.Assistant,
		Content: content,
		Extra: map[string]any{
			SummaryMarkerKey: true,
		},
	}
}

type Conversation interface {
	// Append adds a message to the conversation.
	Append(msg *schema.Message)

	// GetFullMessages returns all messages in the conversation.
	GetFullMessages() []*schema.Message

	// GetMessages returns the messages in the conversation. The number of messages returned is limited by the max window size of the conversation.
	GetMessages() []*schema.Message

	// Load loads the conversation from the storage.
	Load() error

	// Save saves the conversation to the storage.
	Save(msg *schema.Message) error

	// AppendSummary adds a summary-marked message to the conversation (non-destructive).
	AppendSummary(summary *schema.Message)

	// GetWindow returns [last summary + following messages], bounded by a token budget.
	// If budget <= 0, only applies the last-summary trimming without a token cap.
	GetWindow(budget int) []*schema.Message

	// CountTokens counts the tokens in the current window (via the injected TokenCounter).
	CountTokens() int

	// LastSummaryIndex returns the index of the last summary message, or -1 if none.
	LastSummaryIndex() int
}
