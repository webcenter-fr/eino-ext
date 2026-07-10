package copilot

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
)

func TestNewCopilotChatModelDirectToken(t *testing.T) {
	ctx := context.Background()
	cfg := &Config{
		CopilotToken: "test-copilot-token",
		BaseURL:      "http://localhost:0",
		Timeout:      10 * time.Second,
	}

	m, err := NewCopilotChatModel(ctx, cfg)
	if err != nil {
		t.Fatalf("NewCopilotChatModel: unexpected error: %v", err)
	}
	if m == nil {
		t.Fatal("expected non-nil model")
	}
	if m.lockedToken.get() != "test-copilot-token" {
		t.Errorf("expected token 'test-copilot-token', got %q", m.lockedToken.get())
	}
	if m.baseURL != "http://localhost:0" {
		t.Errorf("expected baseURL 'http://localhost:0', got %q", m.baseURL)
	}
	if m.cancelRefresh != nil {
		t.Error("expected nil cancelRefresh when using direct CopilotToken")
	}
}

func TestNewCopilotChatModelNilConfig(t *testing.T) {
	ctx := context.Background()
	_, err := NewCopilotChatModel(ctx, nil)
	if err == nil {
		t.Fatal("expected error for nil config")
	}
}

func TestNewCopilotChatModelNoToken(t *testing.T) {
	ctx := context.Background()
	cfg := &Config{
		Timeout: 10 * time.Second,
	}
	_, err := NewCopilotChatModel(ctx, cfg)
	if err == nil {
		t.Fatal("expected error for missing token")
	}
}

func TestNewCopilotChatModelInvalidTimeout(t *testing.T) {
	ctx := context.Background()
	cfg := &Config{
		CopilotToken: "token",
		Timeout:      500 * time.Millisecond,
	}
	_, err := NewCopilotChatModel(ctx, cfg)
	if err == nil {
		t.Fatal("expected error for timeout below minimum (1s)")
	}
}

func TestCopilotModelGetType(t *testing.T) {
	ctx := context.Background()
	cfg := &Config{
		CopilotToken: "token",
		BaseURL:      "http://localhost:0",
		Timeout:      10 * time.Second,
	}
	m, err := NewCopilotChatModel(ctx, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := m.GetType(); got != copilotGetType {
		t.Errorf("GetType() = %q, want %q", got, copilotGetType)
	}
}

func TestCopilotModelIsCallbacksEnabled(t *testing.T) {
	ctx := context.Background()
	cfg := &Config{
		CopilotToken: "token",
		BaseURL:      "http://localhost:0",
		Timeout:      10 * time.Second,
	}
	m, err := NewCopilotChatModel(ctx, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !m.IsCallbacksEnabled() {
		t.Error("expected IsCallbacksEnabled to return true")
	}
}

func TestCopilotModelWithTools(t *testing.T) {
	ctx := context.Background()
	cfg := &Config{
		CopilotToken: "token",
		BaseURL:      "http://localhost:0",
		Timeout:      10 * time.Second,
	}
	m, err := NewCopilotChatModel(ctx, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	m2, err := m.WithTools(nil)
	if err != nil {
		t.Fatalf("unexpected error for nil tools: %v", err)
	}
	if m2 == nil {
		t.Fatal("expected non-nil model")
	}
}

func TestCopilotModelBindTools(t *testing.T) {
	ctx := context.Background()
	cfg := &Config{
		CopilotToken: "token",
		BaseURL:      "http://localhost:0",
		Timeout:      10 * time.Second,
	}
	m, err := NewCopilotChatModel(ctx, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := m.BindTools(nil); err != nil {
		t.Fatalf("unexpected error for nil tools: %v", err)
	}
}

func TestCopilotModelWithToolsStoresTools(t *testing.T) {
	ctx := context.Background()
	cfg := &Config{
		CopilotToken: "token",
		BaseURL:      "http://localhost:0",
		Timeout:      10 * time.Second,
	}
	m, err := NewCopilotChatModel(ctx, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	toolInfo := &schema.ToolInfo{
		Name: "test-tool",
		Desc: "a test tool",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"arg1": {Type: schema.String, Required: true},
		}),
	}

	m2, err := m.WithTools([]*schema.ToolInfo{toolInfo})
	if err != nil {
		t.Fatalf("WithTools: unexpected error: %v", err)
	}

	cm := m2.(*CopilotModel)
	if len(cm.tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(cm.tools))
	}
	if cm.tools[0].Name != "test-tool" {
		t.Errorf("expected tool name 'test-tool', got %q", cm.tools[0].Name)
	}
	if cm.toolChoice == nil {
		t.Fatal("expected toolChoice to be set")
	}
}

func TestCopilotModelBindToolsStoresTools(t *testing.T) {
	ctx := context.Background()
	cfg := &Config{
		CopilotToken: "token",
		BaseURL:      "http://localhost:0",
		Timeout:      10 * time.Second,
	}
	m, err := NewCopilotChatModel(ctx, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	toolInfo := &schema.ToolInfo{
		Name: "test-tool",
		Desc: "a test tool",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"arg1": {Type: schema.String, Required: true},
		}),
	}

	if err := m.BindTools([]*schema.ToolInfo{toolInfo}); err != nil {
		t.Fatalf("BindTools: unexpected error: %v", err)
	}

	if len(m.tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(m.tools))
	}
	if m.tools[0].Name != "test-tool" {
		t.Errorf("expected tool name 'test-tool', got %q", m.tools[0].Name)
	}
	if m.toolChoice == nil {
		t.Fatal("expected toolChoice to be set")
	}
}

func TestNewCopilotChatModelDefaultTimeout(t *testing.T) {
	ctx := context.Background()
	cfg := &Config{
		CopilotToken: "token",
		BaseURL:      "http://localhost:0",
	}
	m, err := NewCopilotChatModel(ctx, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.cfg.Timeout != defaultTimeout {
		t.Errorf("expected default timeout %v, got %v", defaultTimeout, m.cfg.Timeout)
	}
}

func TestCopilotModelEnterpriseBaseURL(t *testing.T) {
	ctx := context.Background()
	cfg := &Config{
		CopilotToken:  "token",
		EnterpriseURL: "mycompany.com",
		Timeout:       10 * time.Second,
	}
	m, err := NewCopilotChatModel(ctx, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.baseURL != "https://copilot-api.mycompany.com" {
		t.Errorf("expected enterprise base URL, got %q", m.baseURL)
	}
}

func TestCopilotModelBaseURLOverride(t *testing.T) {
	ctx := context.Background()
	cfg := &Config{
		CopilotToken:  "token",
		BaseURL:       "https://custom.example.com",
		EnterpriseURL: "mycompany.com",
		Timeout:       10 * time.Second,
	}
	m, err := NewCopilotChatModel(ctx, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.baseURL != "https://custom.example.com" {
		t.Errorf("expected BaseURL override, got %q", m.baseURL)
	}
}

func TestNewCopilotChatModelWithReasoning(t *testing.T) {
	ctx := context.Background()
	cfg := &Config{
		CopilotToken:  "token",
		BaseURL:       "http://localhost:0",
		Timeout:       10 * time.Second,
		Model:         "gpt-4o",
		ReasoningEffort: ReasoningEffortHigh,
	}
	m, err := NewCopilotChatModel(ctx, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m == nil {
		t.Fatal("expected non-nil model")
	}
}

// --- Mock-server tests for Generate and Stream ---

func TestGenerateWithMockServer(t *testing.T) {
	tokenVal := "test-token"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+tokenVal {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		var body copilotChatRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		resp := copilotChatResponse{
			ID:      "chat-1",
			Model:   body.Model,
			Choices: []copilotChatChoice{{
				Index: 0,
				Message: copilotMessage{
					Role:    "assistant",
					Content: "Hello, world!",
				},
				FinishReason: "stop",
			}},
			Usage: &copilotUsage{
				PromptTokens:     10,
				CompletionTokens: 5,
				TotalTokens:      15,
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	m, err := newTestModel(srv.URL, tokenVal)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx := context.Background()
	msg, err := m.Generate(ctx, []*schema.Message{
		{Role: schema.User, Content: "Hi"},
	})
	if err != nil {
		t.Fatalf("Generate: unexpected error: %v", err)
	}
	if msg.Content != "Hello, world!" {
		t.Errorf("expected 'Hello, world!', got %q", msg.Content)
	}
	if msg.ResponseMeta == nil {
		t.Fatal("expected ResponseMeta")
	}
	if msg.ResponseMeta.FinishReason != "stop" {
		t.Errorf("expected finish_reason 'stop', got %q", msg.ResponseMeta.FinishReason)
	}
}

func TestGenerateWithToolChoice(t *testing.T) {
	tokenVal := "test-token"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body copilotChatRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		// tool_choice should be present when tools are present.
		if body.ToolChoice == nil {
			http.Error(w, "missing tool_choice", http.StatusBadRequest)
			return
		}

		resp := copilotChatResponse{
			ID:      "chat-1",
			Model:   body.Model,
			Choices: []copilotChatChoice{{
				Index: 0,
				Message: copilotMessage{
					Role: "assistant",
					ToolCalls: []copilotToolCall{{
						ID:   "call_1",
						Type: "function",
						Function: copilotToolCallFunc{
							Name:      "test_tool",
							Arguments: `{"arg":"val"}`,
						},
					}},
				},
				FinishReason: "tool_calls",
			}},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	m, err := newTestModel(srv.URL, tokenVal)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	toolInfo := &schema.ToolInfo{
		Name: "test_tool",
		Desc: "A test tool",
	}
	m2, err := m.WithTools([]*schema.ToolInfo{toolInfo})
	if err != nil {
		t.Fatalf("WithTools: %v", err)
	}

	ctx := context.Background()
	msg, err := m2.Generate(ctx, []*schema.Message{
		{Role: schema.User, Content: "Use the tool"},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(msg.ToolCalls))
	}
	if msg.ResponseMeta.FinishReason != "tool_calls" {
		t.Errorf("expected finish_reason 'tool_calls', got %q", msg.ResponseMeta.FinishReason)
	}
}

func TestGenerateWithEmptyModel(t *testing.T) {
	tokenVal := "test-token"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "should not be called", http.StatusInternalServerError)
	}))
	defer srv.Close()

	m, err := newTestModel(srv.URL, tokenVal)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Override model to empty.
	m.cfg.Model = ""

	ctx := context.Background()
	_, err = m.Generate(ctx, []*schema.Message{
		{Role: schema.User, Content: "Hi"},
	})
	if err == nil {
		t.Fatal("expected error for empty model")
	}
	if !strings.Contains(err.Error(), "model must not be empty") {
		t.Errorf("expected 'model must not be empty' error, got: %v", err)
	}
}

func TestGenerateWithVisionInput(t *testing.T) {
	tokenVal := "test-token"
	var receivedContent json.RawMessage

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body copilotChatRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if len(body.Messages) > 0 {
			if b, err := json.Marshal(body.Messages[0].Content); err == nil {
				receivedContent = b
			}
		}
		resp := copilotChatResponse{
			ID:    "chat-1",
			Model: body.Model,
			Choices: []copilotChatChoice{{
				Index:        0,
				Message:      copilotMessage{Role: "assistant", Content: "I see an image."},
				FinishReason: "stop",
			}},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	m, err := newTestModel(srv.URL, tokenVal)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	imgURL := "https://example.com/img.png"
	ctx := context.Background()
	msg, err := m.Generate(ctx, []*schema.Message{
		{
			Role: schema.User,
			UserInputMultiContent: []schema.MessageInputPart{
				{Type: schema.ChatMessagePartTypeText, Text: "What is this?"},
				{Type: schema.ChatMessagePartTypeImageURL, Image: &schema.MessageInputImage{
					MessagePartCommon: schema.MessagePartCommon{URL: &imgURL},
				}},
			},
		},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if msg.Content != "I see an image." {
		t.Errorf("expected 'I see an image.', got %q", msg.Content)
	}

	// Verify the content was sent as an array with image_url.
	var parts []copilotContentPart
	if err := json.Unmarshal(receivedContent, &parts); err != nil {
		t.Fatalf("failed to unmarshal content array: %v (raw=%s)", err, receivedContent)
	}
	if len(parts) != 2 {
		t.Fatalf("expected 2 content parts, got %d", len(parts))
	}
	if parts[0].Type != "text" || parts[0].Text != "What is this?" {
		t.Errorf("part[0]: type=%s text=%s", parts[0].Type, parts[0].Text)
	}
	if parts[1].Type != "image_url" || parts[1].ImageURL == nil || parts[1].ImageURL.URL != imgURL {
		t.Errorf("part[1]: type=%s url=%v", parts[1].Type, parts[1].ImageURL)
	}
}

func TestGenerateWithReasoningRoundTrip(t *testing.T) {
	tokenVal := "test-token"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body copilotChatRequest
		json.NewDecoder(r.Body).Decode(&body)

		// Check that reasoning_opaque from the input assistant message is
		// round-tripped back in the request.
		var hasOpaque bool
		for _, msg := range body.Messages {
			if msg.Role == "assistant" && msg.ReasoningOpaque == "opaque-data" {
				hasOpaque = true
			}
		}
		if hasOpaque {
			// Second call: round-tripped opaque was found — return a follow-up.
			resp := copilotChatResponse{
				ID:    "chat-2",
				Model: body.Model,
				Choices: []copilotChatChoice{{
					Index: 0,
					Message: copilotMessage{
						Role:            "assistant",
						Content:         "Second response",
						ReasoningText:   "more thinking...",
						ReasoningOpaque: "new-opaque-data",
					},
					FinishReason: "stop",
				}},
			}
			json.NewEncoder(w).Encode(resp)
			return
		}

		// First call: no opaque yet — return the initial response with opaque.
		resp := copilotChatResponse{
			ID:    "chat-1",
			Model: body.Model,
			Choices: []copilotChatChoice{{
				Index: 0,
				Message: copilotMessage{
					Role:            "assistant",
					Content:         "First response",
					ReasoningText:   "thinking...",
					ReasoningOpaque: "opaque-data",
				},
				FinishReason: "stop",
			}},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	m, err := newTestModel(srv.URL, tokenVal)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx := context.Background()
	// First call: get opaque data.
	msg1, err := m.Generate(ctx, []*schema.Message{
		{Role: schema.User, Content: "Think about it"},
	})
	if err != nil {
		t.Fatalf("first Generate: %v", err)
	}
	if msg1.Extra["copilot_reasoning_opaque"] != "opaque-data" {
		t.Errorf("expected opaque-data in Extra, got %v", msg1.Extra)
	}

	// Second call: send opaque data back.
	msg2, err := m.Generate(ctx, []*schema.Message{
		{Role: schema.User, Content: "Continue"},
		msg1, // Assistant message with Extra containing opaque
	})
	if err != nil {
		t.Fatalf("second Generate: %v", err)
	}
	if msg2.ReasoningContent != "more thinking..." {
		t.Errorf("expected reasoning content 'more thinking...', got %q", msg2.ReasoningContent)
	}
}

func TestUseResponsesAPI(t *testing.T) {
	// Test cases backported from kilocode:
	//   packages/core/test/plugin/provider-github-copilot.test.ts
	//   "uses responses for gpt-5 models except gpt-5-mini"
	tests := []struct {
		model               string
		forceChatCompletions bool
		want                bool
	}{
		// GPT-5+ models → /responses
		{model: "gpt-5", forceChatCompletions: false, want: true},
		{model: "gpt-5-chat-latest", forceChatCompletions: false, want: true},
		{model: "gpt-5.1-codex", forceChatCompletions: false, want: true},
		{model: "gpt-5.4-mini", forceChatCompletions: false, want: false},
		{model: "gpt-5.4-nano", forceChatCompletions: false, want: false},
		{model: "gpt-6", forceChatCompletions: false, want: true},
		{model: "gpt-6.1", forceChatCompletions: false, want: true},
		{model: "gpt-55", forceChatCompletions: false, want: true},
		// gpt-5-mini and variants → /chat/completions (kilocode exclusion)
		{model: "gpt-5-mini", forceChatCompletions: false, want: false},
		{model: "gpt-5-mini-2025-08-07", forceChatCompletions: false, want: false},
		// GPT-4 and below, non-GPT models → /chat/completions
		{model: "gpt-4o", forceChatCompletions: false, want: false},
		{model: "gpt-4", forceChatCompletions: false, want: false},
		{model: "claude-3.5-sonnet", forceChatCompletions: false, want: false},
		{model: "", forceChatCompletions: false, want: false},
		// ForceChatCompletions overrides all → /chat/completions
		{model: "gpt-5", forceChatCompletions: true, want: false},
		{model: "gpt-5-chat-latest", forceChatCompletions: true, want: false},
		{model: "gpt-5.4-mini", forceChatCompletions: true, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			m := &CopilotModel{
				cfg: &Config{
					ForceChatCompletions: tt.forceChatCompletions,
				},
			}
			if got := m.useResponsesAPI(tt.model); got != tt.want {
				t.Errorf("useResponsesAPI(%q) with ForceChatCompletions=%v = %v, want %v", tt.model, tt.forceChatCompletions, got, tt.want)
			}
		})
	}
}

func TestWouldUseResponses(t *testing.T) {
	// Test cases backported from kilocode:
	//   "gpt-5 → responses, gpt-5.1-codex → responses, gpt-4o → chat,
	//    gpt-5-mini → chat, gpt-5-mini-2025-08-07 → chat"
	tests := []struct {
		model string
		want  bool
	}{
		// GPT-5+ → true
		{model: "gpt-5", want: true},
		{model: "gpt-5-chat-latest", want: true},
		{model: "gpt-5.1-codex", want: true},
		{model: "gpt-5.4-mini", want: false},
		{model: "gpt-5.4-nano", want: false},
		{model: "gpt-6", want: true},
		{model: "gpt-6.1", want: true},
		{model: "gpt-55", want: true},
		{model: "gpt-5-dashed", want: true},
		// gpt-5-mini variants → false (kilocode exclusion)
		{model: "gpt-5-mini", want: false},
		{model: "gpt-5-mini-2025-08-07", want: false},
		// gpt-5-minimal (not gpt-5-mini) still routes to Responses
		{model: "gpt-5-minimal", want: true},
		// GPT-4 and below, non-GPT → false
		{model: "gpt-4o", want: false},
		{model: "gpt-4", want: false},
		{model: "claude-3.5-sonnet", want: false},
		{model: "", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			if got := wouldUseResponses(tt.model); got != tt.want {
				t.Errorf("wouldUseResponses(%q) = %v, want %v", tt.model, got, tt.want)
			}
		})
	}
}

func TestXInitiator(t *testing.T) {
	tests := []struct {
		name string
		in   []*schema.Message
		want string
	}{
		{"empty", nil, "user"},
		{"user text", []*schema.Message{{Role: schema.User, Content: "Hello"}}, "user"},
		{"assistant last", []*schema.Message{{Role: schema.User, Content: "Hello"}, {Role: schema.Assistant, Content: "Hi"}}, "agent"},
		{"tool last", []*schema.Message{{Role: schema.User, Content: "Hello"}, {Role: schema.Tool, Content: "result", ToolCallID: "c1"}}, "agent"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := xInitiator(tt.in); got != tt.want {
				t.Errorf("xInitiator() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMapFinishReason(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"stop", "stop"},
		{"length", "length"},
		{"content_filter", "content_filter"},
		{"tool_calls", "tool_calls"},
		{"function_call", "tool_calls"},
		{"unknown", "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := mapFinishReason(tt.in); got != tt.want {
				t.Errorf("mapFinishReason(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestStreamWithMockServer(t *testing.T) {
	tokenVal := "test-token"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		for _, line := range []string{
			`data: {"choices":[{"delta":{"content":"Hello"}}]}`,
			`data: {"choices":[{"delta":{"content":" world"}}]}`,
			`data: {"choices":[{"finish_reason":"stop","delta":{}}]}`,
			`data: [DONE]`,
		} {
			fmt.Fprintf(w, "%s\n\n", line)
			flusher.Flush()
		}
	}))
	defer srv.Close()

	m, err := newTestModel(srv.URL, tokenVal)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx := context.Background()
	sr, err := m.Stream(ctx, []*schema.Message{
		{Role: schema.User, Content: "Hi"},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer sr.Close()

	var gotContent string
	for {
		msg, err := sr.Recv()
		if err != nil {
			break
		}
		gotContent += msg.Content
	}
	if gotContent != "Hello world" {
		t.Errorf("expected 'Hello world', got %q", gotContent)
	}
}

// newTestModel creates a CopilotModel pointed at a test server.
func newTestModel(baseURL, token string) (*CopilotModel, error) {
	ctx := context.Background()
	cfg := &Config{
		CopilotToken: token,
		BaseURL:      baseURL,
		Model:        "gpt-4o",
		Timeout:      10 * time.Second,
	}
	return NewCopilotChatModel(ctx, cfg)
}
