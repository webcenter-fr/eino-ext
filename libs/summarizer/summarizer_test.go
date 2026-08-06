package summarizer

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestSummarizerFunc(t *testing.T) {
	called := false
	f := SummarizerFunc(func(ctx context.Context, history []*schema.Message, prev string) (string, error) {
		called = true
		return "summary", nil
	})

	result, err := f.Summarize(context.Background(), nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("SummarizerFunc was not called")
	}
	if result != "summary" {
		t.Errorf("expected 'summary', got %q", result)
	}
}

func TestSummarizerFunc_WithHistory(t *testing.T) {
	history := []*schema.Message{
		{Role: schema.User, Content: "hello"},
	}
	f := SummarizerFunc(func(ctx context.Context, h []*schema.Message, prev string) (string, error) {
		if len(h) != 1 || h[0].Content != "hello" {
			t.Errorf("unexpected history: %+v", h)
		}
		return "summary with history", nil
	})

	result, err := f.Summarize(context.Background(), history, "previous")
	if err != nil {
		t.Fatal(err)
	}
	if result != "summary with history" {
		t.Errorf("expected 'summary with history', got %q", result)
	}
}
