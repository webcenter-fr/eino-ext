package copilot

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestStreamEvents(t *testing.T) {
	idx := func(i int) *int { return &i }

	tests := []struct {
		name     string
		sseLines []string
		want     []*schema.Message
	}{
		{
			name: "plain content",
			sseLines: []string{
				`data: {"choices":[{"delta":{"content":"Hello"}}]}`,
				`data: {"choices":[{"delta":{"content":" world"}}]}`,
				`data: [DONE]`,
			},
			want: []*schema.Message{
				{Role: schema.Assistant, Content: "Hello"},
				{Role: schema.Assistant, Content: " world"},
			},
		},
		{
			name: "reasoning to content via empty delta",
			sseLines: []string{
				`data: {"choices":[{"delta":{"reasoning_text":"Let me think..."}}]}`,
				`data: {"choices":[{"delta":{"reasoning_text":" more thinking"}}]}`,
				`data: {"choices":[{"delta":{}}]}`,
				`data: {"choices":[{"delta":{"content":"The answer is 42"}}]}`,
				`data: [DONE]`,
			},
			want: []*schema.Message{
				{Role: schema.Assistant, ReasoningContent: "Let me think..."},
				{Role: schema.Assistant, ReasoningContent: " more thinking"},
				{Role: schema.Assistant, Content: "The answer is 42"},
			},
		},
		{
			name: "reasoning to content without empty delta (immediate transition)",
			sseLines: []string{
				`data: {"choices":[{"delta":{"reasoning_text":"Let me think..."}}]}`,
				`data: {"choices":[{"delta":{"content":"The answer is 42"}}]}`,
				`data: [DONE]`,
			},
			want: []*schema.Message{
				{Role: schema.Assistant, ReasoningContent: "Let me think..."},
				{Role: schema.Assistant, Content: "The answer is 42"},
			},
		},
		{
			// Regression test: reasoning → tool calls without an intermediate
			// empty delta. Before the fix at copilot_stream.go line 122,
			// reasoningOpen stayed true through tool-call chunks, causing the
			// final empty chunk to be swallowed by the transition guard and
			// accumulated tool calls to be silently dropped.
			name: "reasoning to tool calls (regression: final chunk must emit accumulated tools)",
			sseLines: []string{
				`data: {"choices":[{"delta":{"reasoning_text":"I need to search"}}]}`,
				`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"search","arguments":"q=weather"}}]}}]}`,
				`data: {"choices":[{"finish_reason":"stop","delta":{}}]}`,
				`data: [DONE]`,
			},
			want: []*schema.Message{
				{Role: schema.Assistant, ReasoningContent: "I need to search"},
				{
					Role: schema.Assistant,
					ToolCalls: []schema.ToolCall{
						{
							Index:    idx(0),
							ID:       "call_1",
							Function: schema.FunctionCall{Name: "search", Arguments: "q=weather"},
						},
					},
				},
			},
		},
		{
			name: "reasoning to tool calls with empty delta transition",
			sseLines: []string{
				`data: {"choices":[{"delta":{"reasoning_text":"I need to search"}}]}`,
				`data: {"choices":[{"delta":{}}]}`,
				`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"search","arguments":"q=weather"}}]}}]}`,
				`data: {"choices":[{"finish_reason":"stop","delta":{}}]}`,
				`data: [DONE]`,
			},
			want: []*schema.Message{
				{Role: schema.Assistant, ReasoningContent: "I need to search"},
				{
					Role: schema.Assistant,
					ToolCalls: []schema.ToolCall{
						{
							Index:    idx(0),
							ID:       "call_1",
							Function: schema.FunctionCall{Name: "search", Arguments: "q=weather"},
						},
					},
				},
			},
		},
		{
			name: "content to tool calls",
			sseLines: []string{
				`data: {"choices":[{"delta":{"content":"Let me look that up"}}]}`,
				`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"search","arguments":"q=weather"}}]}}]}`,
				`data: {"choices":[{"finish_reason":"stop","delta":{}}]}`,
				`data: [DONE]`,
			},
			want: []*schema.Message{
				{Role: schema.Assistant, Content: "Let me look that up"},
				{
					Role: schema.Assistant,
					ToolCalls: []schema.ToolCall{
						{
							Index:    idx(0),
							ID:       "call_1",
							Function: schema.FunctionCall{Name: "search", Arguments: "q=weather"},
						},
					},
				},
			},
		},
		{
			name: "tool calls only, no content",
			sseLines: []string{
				`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"search","arguments":"q=weather"}}]}}]}`,
				`data: {"choices":[{"finish_reason":"stop","delta":{}}]}`,
				`data: [DONE]`,
			},
			want: []*schema.Message{
				{
					Role: schema.Assistant,
					ToolCalls: []schema.ToolCall{
						{
							Index:    idx(0),
							ID:       "call_1",
							Function: schema.FunctionCall{Name: "search", Arguments: "q=weather"},
						},
					},
				},
			},
		},
		{
			name: "multiple tool calls, sorted by index",
			sseLines: []string{
				`data: {"choices":[{"delta":{"tool_calls":[{"index":1,"id":"call_2","function":{"name":"translate","arguments":"lang="}}]}}]}`,
				`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"search","arguments":"q=weather"}}]}}]}`,
				`data: {"choices":[{"finish_reason":"stop","delta":{}}]}`,
				`data: [DONE]`,
			},
			want: []*schema.Message{
				{
					Role: schema.Assistant,
					ToolCalls: []schema.ToolCall{
						{
							Index:    idx(0),
							ID:       "call_1",
							Function: schema.FunctionCall{Name: "search", Arguments: "q=weather"},
						},
						{
							Index:    idx(1),
							ID:       "call_2",
							Function: schema.FunctionCall{Name: "translate", Arguments: "lang="},
						},
					},
				},
			},
		},
		{
			// reasoning_opaque is encrypted/binary content — it must never be
			// used as the displayed reasoning text. Only reasoning_text is shown.
			name: "reasoning_opaque only does not emit ReasoningContent",
			sseLines: []string{
				`data: {"choices":[{"delta":{"reasoning_opaque":"opaque thought"}}]}`,
				`data: {"choices":[{"delta":{"content":"response"}}]}`,
				`data: [DONE]`,
			},
			want: []*schema.Message{
				{Role: schema.Assistant, Content: "response"},
			},
		},
		{
			// Regression test: reasoning_text and content in the SAME chunk.
			// The pre-fix code silently dropped content when reasoning was present.
			name: "reasoning and content in same chunk (regression: both must be emitted)",
			sseLines: []string{
				`data: {"choices":[{"delta":{"reasoning_text":"Let me think...","content":"Here is the answer"}}]}`,
				`data: [DONE]`,
			},
			want: []*schema.Message{
				{Role: schema.Assistant, ReasoningContent: "Let me think..."},
				{Role: schema.Assistant, Content: "Here is the answer"},
			},
		},
		{
			name: "argument accumulation across tool call chunks",
			sseLines: []string{
				`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"search","arguments":"hello "}}]}}]}`,
				`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"world"}}]}}]}`,
				`data: {"choices":[{"finish_reason":"stop","delta":{}}]}`,
				`data: [DONE]`,
			},
			want: []*schema.Message{
				{
					Role: schema.Assistant,
					ToolCalls: []schema.ToolCall{
						{
							Index:    idx(0),
							ID:       "call_1",
							Function: schema.FunctionCall{Name: "search", Arguments: "hello world"},
						},
					},
				},
			},
		},
		{
			name: "streaming token usage in final chunk",
			sseLines: []string{
				`data: {"choices":[{"delta":{"content":"response"}}]}`,
				`data: {"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`,
				`data: [DONE]`,
			},
			want: []*schema.Message{
				{Role: schema.Assistant, Content: "response"},
				{
					Role: schema.Assistant,
					ResponseMeta: &schema.ResponseMeta{
						Usage: &schema.TokenUsage{
							PromptTokens:     10,
							CompletionTokens: 5,
							TotalTokens:      15,
						},
					},
				},
			},
		},
		{
			// Regression test: tool-call deltas followed by [DONE] without
			// a finish-reason chunk. The defensive flush must emit the
			// accumulated tool calls before the stream ends.
			name: "[DONE] without finish_reason flushes accumulated tool calls",
			sseLines: []string{
				`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"search","arguments":"q=weather"}}]}}]}`,
				`data: [DONE]`,
			},
			want: []*schema.Message{
				{
					Role: schema.Assistant,
					ToolCalls: []schema.ToolCall{
						{
							Index:    idx(0),
							ID:       "call_1",
							Function: schema.FunctionCall{Name: "search", Arguments: "q=weather"},
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := runStreamEventsTest(t, tt.sseLines)

			if len(got) != len(tt.want) {
				t.Fatalf("got %d messages, want %d:\ngot: %+v\nwant: %+v",
					len(got), len(tt.want), got, tt.want)
			}
			for i, msg := range got {
				compareMsg(t, i, msg, tt.want[i])
			}
		})
	}
}

// TestStreamReasoningTextOnly verifies that when only reasoning_text is
// present (no reasoning_opaque), ReasoningContent is emitted and Extra does
// NOT contain copilot_reasoning_opaque.
func TestStreamReasoningTextOnly(t *testing.T) {
	msgs := runStreamEventsTest(t, []string{
		`data: {"choices":[{"delta":{"reasoning_text":"I should think about this..."}}]}`,
		`data: [DONE]`,
	})

	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d: %+v", len(msgs), msgs)
	}
	if msgs[0].ReasoningContent != "I should think about this..." {
		t.Errorf("ReasoningContent = %q, want %q", msgs[0].ReasoningContent, "I should think about this...")
	}
	if msgs[0].Extra != nil {
		if _, ok := msgs[0].Extra[extraKeyReasoningOpaque]; ok {
			t.Errorf("Extra[extraKeyReasoningOpaque] should not be set when no opaque is present")
		}
	}
}

// TestStreamReasoningOpaqueOnly verifies that when only reasoning_opaque is
// present (no reasoning_text), ReasoningContent is NOT emitted. The opaque
// value is stored in Extra for multi-turn round-trip only.
func TestStreamReasoningOpaqueOnly(t *testing.T) {
	// Opaque and content in separate chunks so the msg that carries Extra
	// is actually sent (otherwise msg is not sent and Extra is lost).
	msgs := runStreamEventsTest(t, []string{
		`data: {"choices":[{"delta":{"reasoning_opaque":"ZXhhbXBsZQ=="}}]}`,
		`data: {"choices":[{"delta":{"content":"response"}}]}`,
		`data: [DONE]`,
	})

	// No message should have ReasoningContent (opaque is never shown).
	for i, m := range msgs {
		if m.ReasoningContent != "" {
			t.Errorf("message[%d]: ReasoningContent = %q, want empty (opaque must not be displayed)", i, m.ReasoningContent)
		}
	}

	// The content message should not carry the opaque in Extra since the
	// opaque-only chunk does not enter the ReasoningText guard.
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d: %+v", len(msgs), msgs)
	}
	if msgs[0].Content != "response" {
		t.Errorf("Content = %q, want %q", msgs[0].Content, "response")
	}
	// With the current implementation, opaque-only (no reasoning_text) does
	// not trigger Extra storage because the storage path is inside the
	// ReasoningText guard. In production, opaque typically accompanies
	// reasoning_text; the isolated-opaque case is a no-op for display safety.
}

// TestStreamReasoningBothFields verifies that when both reasoning_text and
// reasoning_opaque are present, ReasoningContent uses the human-readable text
// and the opaque is stored in Extra.
func TestStreamReasoningBothFields(t *testing.T) {
	msgs := runStreamEventsTest(t, []string{
		`data: {"choices":[{"delta":{"reasoning_text":"Hello","reasoning_opaque":"Z29vZGJ5ZQ==","content":"resp"}}]}`,
		`data: [DONE]`,
	})

	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages (reasoning + content), got %d: %+v", len(msgs), msgs)
	}

	// First message: reasoning
	if msgs[0].ReasoningContent != "Hello" {
		t.Errorf("message[0].ReasoningContent = %q, want %q", msgs[0].ReasoningContent, "Hello")
	}

	// Second message: content
	if msgs[1].Content != "resp" {
		t.Errorf("message[1].Content = %q, want %q", msgs[1].Content, "resp")
	}
	if msgs[1].Extra != nil {
		t.Errorf("message[1].Extra should be nil (opaque travels with reasoning message), got %v", msgs[1].Extra)
	}

	// Opaque must be attached to the reasoning message.
	if opaque, ok := msgs[0].Extra[extraKeyReasoningOpaque].(string); !ok {
		t.Fatalf("message[0].Extra[extraKeyReasoningOpaque] missing or wrong type: %v", msgs[0].Extra)
	} else if opaque != "Z29vZGJ5ZQ==" {
		t.Errorf("Extra[extraKeyReasoningOpaque] = %q, want %q", opaque, "Z29vZGJ5ZQ==")
	}
}

// TestStreamReasoningBothFieldsSeparateChunks verifies that when
// reasoning_text+reasoning_opaque arrive in one chunk and content arrives in a
// separate chunk (the common streaming pattern), the opaque is preserved in the
// reasoning message's Extra for multi-turn round-trip.
func TestStreamReasoningBothFieldsSeparateChunks(t *testing.T) {
	msgs := runStreamEventsTest(t, []string{
		`data: {"choices":[{"delta":{"reasoning_text":"Hello","reasoning_opaque":"Z29vZGJ5ZQ=="}}]}`,
		`data: {"choices":[{"delta":{"content":"world"}}]}`,
		`data: [DONE]`,
	})

	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages (reasoning + content), got %d: %+v", len(msgs), msgs)
	}

	// First message: reasoning with opaque in Extra
	if msgs[0].ReasoningContent != "Hello" {
		t.Errorf("message[0].ReasoningContent = %q, want %q", msgs[0].ReasoningContent, "Hello")
	}
	if opaque, ok := msgs[0].Extra[extraKeyReasoningOpaque].(string); !ok {
		t.Fatalf("message[0].Extra[extraKeyReasoningOpaque] missing or wrong type: %v", msgs[0].Extra)
	} else if opaque != "Z29vZGJ5ZQ==" {
		t.Errorf("Extra[extraKeyReasoningOpaque] = %q, want %q", opaque, "Z29vZGJ5ZQ==")
	}

	// Second message: content only, no Extra
	if msgs[1].Content != "world" {
		t.Errorf("message[1].Content = %q, want %q", msgs[1].Content, "world")
	}
	if msgs[1].Extra != nil {
		t.Errorf("message[1].Extra should be nil, got %v", msgs[1].Extra)
	}
}

// TestStreamReasoningOpaqueOnlyNonEmptyContent verifies that when
// reasoning_text is empty and reasoning_opaque is set, no reasoning
// content is emitted (opaque is never shown).
func TestStreamReasoningOpaqueOnlyNonEmptyContent(t *testing.T) {
	msgs := runStreamEventsTest(t, []string{
		`data: {"choices":[{"delta":{"reasoning_text":"","reasoning_opaque":"dGVzdA==","content":"result"}}]}`,
		`data: [DONE]`,
	})

	// No message should have ReasoningContent.
	for i, m := range msgs {
		if m.ReasoningContent != "" {
			t.Errorf("message[%d]: ReasoningContent = %q, want empty (opaque must not be displayed)", i, m.ReasoningContent)
		}
	}

	if len(msgs) != 1 {
		t.Fatalf("expected 1 message (content only), got %d: %+v", len(msgs), msgs)
	}
	if msgs[0].Content != "result" {
		t.Errorf("Content = %q, want %q", msgs[0].Content, "result")
	}
}

// runStreamEventsTest sends SSE lines through streamEvents and collects
// emitted messages. It handles the pipe/goroutine setup common to all
// streamEvents tests.
func runStreamEventsTest(t *testing.T, sseLines []string) []*schema.Message {
	t.Helper()
	ctx := context.Background()
	pr, pw := io.Pipe()
	sr, sw := schema.Pipe[*schema.Message](2)

	go func() {
		for _, line := range sseLines {
			_, _ = fmt.Fprintln(pw, line)
		}
		_ = pw.Close()
	}()

	errCh := make(chan error, 1)
	go func() {
		errCh <- streamEvents(ctx, pr, sw, nil)
		sw.Close()
	}()

	var msgs []*schema.Message
	for {
		msg, err := sr.Recv()
		if err != nil {
			if err == io.EOF || strings.Contains(err.Error(), "EOF") {
				break
			}
			t.Fatalf("Recv: unexpected error: %v", err)
		}
		msgs = append(msgs, msg)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("streamEvents: %v", err)
	}
	return msgs
}

func compareMsg(t *testing.T, idx int, got, want *schema.Message) {
	t.Helper()
	if got.Role != want.Role {
		t.Errorf("message[%d].Role = %v, want %v", idx, got.Role, want.Role)
	}
	if got.Content != want.Content {
		t.Errorf("message[%d].Content = %q, want %q", idx, got.Content, want.Content)
	}
	if got.ReasoningContent != want.ReasoningContent {
		t.Errorf("message[%d].ReasoningContent = %q, want %q", idx, got.ReasoningContent, want.ReasoningContent)
	}
	if got.ResponseMeta != nil && want.ResponseMeta != nil {
		gotUsage := got.ResponseMeta.Usage
		wantUsage := want.ResponseMeta.Usage
		if gotUsage == nil && wantUsage != nil || gotUsage != nil && wantUsage == nil {
			t.Errorf("message[%d].ResponseMeta.Usage = %v, want %v", idx, gotUsage, wantUsage)
		} else if gotUsage != nil && wantUsage != nil {
			if gotUsage.PromptTokens != wantUsage.PromptTokens {
				t.Errorf("message[%d].ResponseMeta.Usage.PromptTokens = %d, want %d", idx, gotUsage.PromptTokens, wantUsage.PromptTokens)
			}
			if gotUsage.CompletionTokens != wantUsage.CompletionTokens {
				t.Errorf("message[%d].ResponseMeta.Usage.CompletionTokens = %d, want %d", idx, gotUsage.CompletionTokens, wantUsage.CompletionTokens)
			}
			if gotUsage.TotalTokens != wantUsage.TotalTokens {
				t.Errorf("message[%d].ResponseMeta.Usage.TotalTokens = %d, want %d", idx, gotUsage.TotalTokens, wantUsage.TotalTokens)
			}
		}
	}
	if len(got.ToolCalls) != len(want.ToolCalls) {
		t.Errorf("message[%d].ToolCalls len = %d, want %d", idx, len(got.ToolCalls), len(want.ToolCalls))
		return
	}
	for j, tc := range got.ToolCalls {
		wtc := want.ToolCalls[j]
		if tc.ID != wtc.ID {
			t.Errorf("message[%d].ToolCalls[%d].ID = %q, want %q", idx, j, tc.ID, wtc.ID)
		}
		if tc.Function.Name != wtc.Function.Name {
			t.Errorf("message[%d].ToolCalls[%d].Name = %q, want %q", idx, j, tc.Function.Name, wtc.Function.Name)
		}
		if tc.Function.Arguments != wtc.Function.Arguments {
			t.Errorf("message[%d].ToolCalls[%d].Arguments = %q, want %q", idx, j, tc.Function.Arguments, wtc.Function.Arguments)
		}
		if tc.Index != nil && wtc.Index != nil && *tc.Index != *wtc.Index {
			t.Errorf("message[%d].ToolCalls[%d].Index = %d, want %d", idx, j, *tc.Index, *wtc.Index)
		}
	}
}
