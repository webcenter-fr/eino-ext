package memory

import (
	"github.com/goccy/go-json"

	"github.com/cloudwego/eino/schema"
)

// SummaryMarkerKey is the key used in schema.Message.Extra to mark a message as a summary.
const SummaryMarkerKey = "__eino_ext_memory_summary"

// IsSummary returns true if the message is a summary message.
func IsSummary(msg *schema.Message) bool {
	return HasBoolMarker(msg, SummaryMarkerKey)
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

	// GetActivities returns all stored activity events.
	GetActivities() []json.RawMessage

	// SetActivities replaces all stored activity events (batch write after run).
	SetActivities(raw []json.RawMessage)
}

// LastSummaryIndex returns the index of the last summary message in msgs, or -1
// when none is present. It is the shared implementation used by every
// Conversation backend.
func LastSummaryIndex(msgs []*schema.Message) int {
	for i := len(msgs) - 1; i >= 0; i-- {
		if IsSummary(msgs[i]) {
			return i
		}
	}
	return -1
}

// SelectWindow returns [last summary + following messages] from msgs, bounded by
// a token budget. When budget <= 0, maxWindowTokens is used; when that is also 0,
// no token cap is applied. Trimming uses binary search (O(log N) calls to count)
// and always preserves the leading summary (when present) and the last message.
//
// This is the shared windowing logic behind every Conversation backend's
// GetWindow implementation.
func SelectWindow(msgs []*schema.Message, count TokenCounter, budget, maxWindowTokens int) []*schema.Message {
	if budget <= 0 {
		budget = maxWindowTokens
	}

	startIdx := 0
	if idx := LastSummaryIndex(msgs); idx >= 0 {
		startIdx = idx
	}

	window := msgs[startIdx:]

	if budget <= 0 || len(window) == 0 {
		return window
	}

	n := len(window)

	// Fast path: already fits within budget.
	if count(window) <= budget {
		return window
	}

	// Nothing more we can trim if there is only one message.
	if n == 1 {
		return window
	}

	if !IsSummary(window[0]) {
		// Find the leftmost trimStart such that window[trimStart:] fits within
		// budget, always keeping at least the last message.
		if count(window[n-1:]) > budget {
			return window[n-1:]
		}
		lo, hi := 0, n-1
		for lo < hi {
			mid := (lo + hi) / 2
			if count(window[mid:]) <= budget {
				hi = mid
			} else {
				lo = mid + 1
			}
		}
		return window[lo:]
	}

	// Has summary: preserve the summary (window[0]) and the last message.
	if n == 2 {
		return window
	}

	// Check whether even the minimal window [summary, last] fits.
	minWindow := []*schema.Message{window[0], window[n-1]}
	if count(minWindow) > budget {
		return minWindow
	}

	// Binary search for the leftmost trimStart in [1, n-1] whose candidate fits.
	// Use a single pre-allocated scratch slice to avoid per-iteration allocations.
	scratch := make([]*schema.Message, n)
	scratch[0] = window[0]

	lo, hi := 1, n-1
	for lo < hi {
		mid := (lo + hi) / 2
		sz := n - mid
		copy(scratch[1:1+sz], window[mid:])
		if count(scratch[:1+sz]) <= budget {
			hi = mid
		} else {
			lo = mid + 1
		}
	}

	trimStart := lo
	sz := n - trimStart
	result := make([]*schema.Message, 1+sz)
	result[0] = window[0]
	copy(result[1:], window[trimStart:])
	return result
}
