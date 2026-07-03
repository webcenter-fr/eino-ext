package summarizer

import (
	"context"

	"github.com/cloudwego/eino/schema"
)

type Summarizer interface {
	Summarize(ctx context.Context, history []*schema.Message, previousSummary string) (string, error)
}

type SummarizerFunc func(ctx context.Context, history []*schema.Message, previousSummary string) (string, error)

func (f SummarizerFunc) Summarize(ctx context.Context, history []*schema.Message, previousSummary string) (string, error) {
	return f(ctx, history, previousSummary)
}
