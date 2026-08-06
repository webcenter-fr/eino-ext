// Package summarizer defines a minimal interface for conversation-summarization
// backends, enabling pluggable condensation strategies.
package summarizer

import (
	"context"

	"github.com/cloudwego/eino/schema"
)

// Summarizer condenses a conversation history into a single summary string,
// optionally incorporating a previously generated summary for incremental
// condensation.
type Summarizer interface {
	Summarize(ctx context.Context, history []*schema.Message, previousSummary string) (string, error)
}

// SummarizerFunc is an adapter that allows an ordinary function to be used as a
// [Summarizer].
//
//nolint:revive // SummarizerFunc is the established public name.
type SummarizerFunc func(ctx context.Context, history []*schema.Message, previousSummary string) (string, error)

// Summarize calls the underlying function f.
func (f SummarizerFunc) Summarize(ctx context.Context, history []*schema.Message, previousSummary string) (string, error) {
	return f(ctx, history, previousSummary)
}
