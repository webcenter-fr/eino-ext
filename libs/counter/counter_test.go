package counter

import (
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestDefaultTokenCounter_Empty(t *testing.T) {
	count := DefaultTokenCounter(nil)
	if count != 0 {
		t.Errorf("expected 0 tokens for nil messages, got %d", count)
	}
	count = DefaultTokenCounter([]*schema.Message{})
	if count != 0 {
		t.Errorf("expected 0 tokens for empty messages, got %d", count)
	}
}

func TestDefaultTokenCounter_Content(t *testing.T) {
	msgs := []*schema.Message{
		{Role: schema.User, Content: "hello world"},
	}
	count := DefaultTokenCounter(msgs)
	if count != 2 { // 11 chars / 4 = 2
		t.Errorf("expected 2 tokens, got %d", count)
	}
}

func TestDefaultTokenCounter_SmallContent(t *testing.T) {
	msgs := []*schema.Message{
		{Role: schema.User, Content: "a"},
	}
	count := DefaultTokenCounter(msgs)
	if count != 1 { // 1 char / 4 = 0, but minimum is 1
		t.Errorf("expected 1 token for small content, got %d", count)
	}
}

func TestDefaultTokenCounter_ToolCalls(t *testing.T) {
	msgs := []*schema.Message{
		{
			Role: schema.Assistant,
			ToolCalls: []schema.ToolCall{
				{Function: schema.FunctionCall{Arguments: "{\"key\":\"value\"}"}},
			},
		},
	}
	count := DefaultTokenCounter(msgs)
	expected := len("{\"key\":\"value\"}") / 4
	if count != expected {
		t.Errorf("expected %d tokens, got %d", expected, count)
	}
}
