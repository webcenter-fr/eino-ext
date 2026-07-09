package promptenhance

import (
	"context"
	"errors"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type mockModel struct {
	generateFunc func(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error)
	streamFunc   func(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error)
}

func (m *mockModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	return m.generateFunc(ctx, input, opts...)
}

func (m *mockModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return m.streamFunc(ctx, input, opts...)
}

func TestNewEnhancer(t *testing.T) {
	t.Run("nil config", func(t *testing.T) {
		_, err := NewEnhancer(context.Background(), nil)
		if err == nil {
			t.Fatal("expected error for nil config")
		}
	})

	t.Run("missing model", func(t *testing.T) {
		_, err := NewEnhancer(context.Background(), &Config{})
		if err == nil {
			t.Fatal("expected error for missing model")
		}
	})

	t.Run("valid config", func(t *testing.T) {
		m := &mockModel{}
		e, err := NewEnhancer(context.Background(), &Config{Model: m})
		if err != nil {
			t.Fatal(err)
		}
		if e == nil {
			t.Fatal("expected non-nil enhancer")
		}
	})

	t.Run("custom system prompt", func(t *testing.T) {
		m := &mockModel{}
		customPrompt := "custom system prompt"
		e, err := NewEnhancer(context.Background(), &Config{Model: m, SystemPrompt: customPrompt})
		if err != nil {
			t.Fatal(err)
		}
		if e.systemPrompt != customPrompt {
			t.Fatalf("expected custom system prompt %q, got %q", customPrompt, e.systemPrompt)
		}
	})
}

func TestEnhancer_Enhance(t *testing.T) {
	t.Run("normal enhancement", func(t *testing.T) {
		m := &mockModel{
			generateFunc: func(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
				if len(input) < 2 {
					t.Fatal("expected at least system + user message")
				}
				return &schema.Message{Role: schema.Assistant, Content: "improved version of the draft"}, nil
			},
		}
		e, err := NewEnhancer(context.Background(), &Config{Model: m})
		if err != nil {
			t.Fatal(err)
		}
		result, err := e.Enhance(context.Background(), "original draft")
		if err != nil {
			t.Fatal(err)
		}
		if result != "improved version of the draft" {
			t.Fatalf("got %q", result)
		}
	})

	t.Run("empty draft", func(t *testing.T) {
		e, err := NewEnhancer(context.Background(), &Config{Model: &mockModel{}})
		if err != nil {
			t.Fatal(err)
		}
		result, err := e.Enhance(context.Background(), "")
		if err != nil {
			t.Fatal(err)
		}
		if result != "" {
			t.Fatalf("expected empty result for empty draft, got %q", result)
		}
	})

	t.Run("model error propagation", func(t *testing.T) {
		modelErr := errors.New("model failure")
		m := &mockModel{
			generateFunc: func(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
				return nil, modelErr
			},
		}
		e, err := NewEnhancer(context.Background(), &Config{Model: m})
		if err != nil {
			t.Fatal(err)
		}
		_, err = e.Enhance(context.Background(), "draft")
		if err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestClean(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"no change", "hello world", "hello world"},
		{"trim whitespace", "  hello  ", "hello"},
		{"markdown fences", "```\nhello world\n```", "hello world"},
		{"markdown fences no lang", "```\nhello\n```", "hello"},
		{"double quotes", `"hello world"`, "hello world"},
		{"single quotes", "'hello world'", "hello world"},
		// nested quotes-then-fences intentionally not handled; models
		// practically never output combined fence+quote wrapping
		{"not double-stripping inner quotes", `"he said 'hello'"`, "he said 'hello'"},
		{"newlines trimmed", "\n\nhello\n\n", "hello"},
		{"empty string", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := clean(tt.input)
			if got != tt.want {
				t.Fatalf("clean(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
