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
			name: "reasoning_opaque is mapped to ReasoningContent",
			sseLines: []string{
				`data: {"choices":[{"delta":{"reasoning_opaque":"opaque thought"}}]}`,
				`data: {"choices":[{"delta":{"content":"response"}}]}`,
				`data: [DONE]`,
			},
			want: []*schema.Message{
				{Role: schema.Assistant, ReasoningContent: "opaque thought"},
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
			ctx := context.Background()
			pr, pw := io.Pipe()
			sr, sw := schema.Pipe[*schema.Message](2)

			// Write SSE lines in a goroutine.
			go func() {
				for _, line := range tt.sseLines {
					fmt.Fprintln(pw, line)
				}
				pw.Close()
			}()

			// Run streamEvents concurrently.
			errCh := make(chan error, 1)
			go func() {
				errCh <- streamEvents(ctx, pr, sw)
				sw.Close()
			}()

			// Collect emitted messages.
			var got []*schema.Message
			for {
				msg, err := sr.Recv()
				if err != nil {
					if err == io.EOF || strings.Contains(err.Error(), "EOF") {
						break
					}
					t.Fatalf("Recv: unexpected error: %v", err)
				}
				got = append(got, msg)
			}

			if err := <-errCh; err != nil {
				t.Fatalf("streamEvents: %v", err)
			}

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
