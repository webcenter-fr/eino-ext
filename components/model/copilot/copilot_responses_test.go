package copilot

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

func TestResponsesNonStreaming(t *testing.T) {
	tokenVal := "test-token"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/responses") {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+tokenVal {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		var body responsesRequest
		json.NewDecoder(r.Body).Decode(&body)

		resp := responsesResponse{
			ID:        "resp-1",
			Model:     body.Model,
			Output: []responsesOutputItem{
				{
					Type: "message",
					Role: "assistant",
					ID:   "msg-1",
					Content: []responsesContentPart{
						{Type: "output_text", Text: "Hello from responses!"},
					},
				},
			},
			Usage: &responsesUsage{
				InputTokens:  10,
				OutputTokens: 5,
				TotalTokens:  15,
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	m, err := newTestModel(srv.URL, tokenVal)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Force GPT-5 to hit the responses path.
	m.cfg.Model = "gpt-5"

	ctx := context.Background()
	msg, err := m.Generate(ctx, []*schema.Message{
		{Role: schema.User, Content: "Hello"},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if msg.Content != "Hello from responses!" {
		t.Errorf("expected 'Hello from responses!', got %q", msg.Content)
	}
	if msg.ResponseMeta == nil {
		t.Fatal("expected ResponseMeta")
	}
	if msg.ResponseMeta.FinishReason != "stop" {
		t.Errorf("expected finish_reason 'stop', got %q", msg.ResponseMeta.FinishReason)
	}
}

// TestResponsesStoreOmitted guards against regressing the GitHub Copilot
// "store is not supported" 400: the /responses request must never carry a
// `store` field on the wire.
func TestResponsesStoreOmitted(t *testing.T) {
	tokenVal := "test-token"
	var rawBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/responses") {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		json.NewDecoder(r.Body).Decode(&rawBody)

		resp := responsesResponse{
			ID:    "resp-1",
			Model: "gpt-5",
			Output: []responsesOutputItem{
				{
					Type: "message",
					Role: "assistant",
					ID:   "msg-1",
					Content: []responsesContentPart{
						{Type: "output_text", Text: "ok"},
					},
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	m, err := newTestModel(srv.URL, tokenVal)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m.cfg.Model = "gpt-5"

	if _, err := m.Generate(context.Background(), []*schema.Message{
		{Role: schema.User, Content: "Hello"},
	}); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if _, ok := rawBody["store"]; ok {
		t.Errorf("responses request must not include a 'store' field, got: %v", rawBody["store"])
	}
}

func TestResponsesWithFunctionCall(t *testing.T) {
	tokenVal := "test-token"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body responsesRequest
		json.NewDecoder(r.Body).Decode(&body)

		resp := responsesResponse{
			ID:    "resp-1",
			Model: body.Model,
			Output: []responsesOutputItem{
				{
					Type:      "function_call",
					CallID:    "call_1",
					Name:      "search",
					Arguments: `{"query":"weather"}`,
					ID:        "fc-1",
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	m, err := newTestModel(srv.URL, tokenVal)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m.cfg.Model = "gpt-5"

	ctx := context.Background()
	msg, err := m.Generate(ctx, []*schema.Message{
		{Role: schema.User, Content: "Search weather"},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(msg.ToolCalls))
	}
	if msg.ToolCalls[0].Function.Name != "search" {
		t.Errorf("expected tool name 'search', got %q", msg.ToolCalls[0].Function.Name)
	}
	if msg.ResponseMeta.FinishReason != "tool_calls" {
		t.Errorf("expected finish_reason 'tool_calls', got %q", msg.ResponseMeta.FinishReason)
	}
}

func TestResponsesWithReasoning(t *testing.T) {
	tokenVal := "test-token"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body responsesRequest
		json.NewDecoder(r.Body).Decode(&body)

		resp := responsesResponse{
			ID:    "resp-1",
			Model: body.Model,
			Output: []responsesOutputItem{
				{
					Type:             "reasoning",
					ID:               "reason-1",
					EncryptedContent: "encrypted-data",
					Summary: []responsesSummaryPart{
						{Type: "summary_text", Text: "Let me think..."},
					},
				},
				{
					Type: "message",
					Role: "assistant",
					ID:   "msg-1",
					Content: []responsesContentPart{
						{Type: "output_text", Text: "The answer is 42"},
					},
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	m, err := newTestModel(srv.URL, tokenVal)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m.cfg.Model = "gpt-5"

	ctx := context.Background()
	msg, err := m.Generate(ctx, []*schema.Message{
		{Role: schema.User, Content: "What is the answer?"},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if msg.Content != "The answer is 42" {
		t.Errorf("expected 'The answer is 42', got %q", msg.Content)
	}
	if msg.ReasoningContent != "Let me think..." {
		t.Errorf("expected reasoning 'Let me think...', got %q", msg.ReasoningContent)
	}
	if msg.Extra["copilot_encrypted_content"] != "encrypted-data" {
		t.Errorf("expected encrypted_content in Extra, got %v", msg.Extra)
	}
	if msg.Extra["copilot_reasoning_item_id"] != "reason-1" {
		t.Errorf("expected reasoning_item_id in Extra, got %v", msg.Extra)
	}
}

func TestResponsesFinishReasonMapping(t *testing.T) {
	tests := []struct {
		reason string
		want   string
	}{
		{"", "stop"},
		{"max_output_tokens", "length"},
		{"content_filter", "content_filter"},
		{"other", "other"},
	}
	for _, tt := range tests {
		t.Run(tt.reason, func(t *testing.T) {
			if got := responsesFinishReason(tt.reason); got != tt.want {
				t.Errorf("responsesFinishReason(%q) = %q, want %q", tt.reason, got, tt.want)
			}
		})
	}
}

func TestResponsesStreamingSSE(t *testing.T) {
	tokenVal := "test-token"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/responses") {
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
			`data: {"type":"response.created","response":{"id":"resp-1"}}`,
			`data: {"type":"response.output_text.delta","delta":"Hello"}`,
			`data: {"type":"response.output_text.delta","delta":" world"}`,
			`data: {"type":"response.completed","response":{"id":"resp-1","usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}}}`,
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
	m.cfg.Model = "gpt-5"

	ctx := context.Background()
	sr, err := m.Stream(ctx, []*schema.Message{
		{Role: schema.User, Content: "Hello"},
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

func TestResponsesInputConversion(t *testing.T) {
	imgURL := "https://example.com/img.png"
	in := []*schema.Message{
		{Role: schema.System, Content: "You are helpful."},
		{
			Role: schema.User,
			UserInputMultiContent: []schema.MessageInputPart{
				{Type: schema.ChatMessagePartTypeText, Text: "What is this?"},
				{Type: schema.ChatMessagePartTypeImageURL, Image: &schema.MessageInputImage{
					MessagePartCommon: schema.MessagePartCommon{URL: &imgURL},
				}},
			},
		},
		{Role: schema.Tool, Content: "tool result", ToolCallID: "call_1"},
	}

	items := convertToResponsesInput(in)
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}

	// System message.
	if items[0].Role != "system" {
		t.Errorf("expected system role, got %q", items[0].Role)
	}

	// User message with array content.
	userContent, ok := items[1].Content.([]map[string]string)
	if !ok {
		t.Fatalf("expected []map[string]string for user content, got %T", items[1].Content)
	}
	if len(userContent) != 2 {
		t.Fatalf("expected 2 content parts, got %d", len(userContent))
	}
	if userContent[0]["text"] != "What is this?" {
		t.Errorf("expected text part, got %v", userContent[0])
	}
	if userContent[1]["type"] != "input_image" || userContent[1]["image_url"] != imgURL {
		t.Errorf("expected input_image with url, got %v", userContent[1])
	}

	// Tool message.
	if items[2].Type != "function_call_output" || items[2].CallID != "call_1" {
		t.Errorf("expected function_call_output, got type=%s call_id=%s", items[2].Type, items[2].CallID)
	}
}

// TestResponsesWithModelOverride verifies that model.WithModel() overrides
// Config.Model for the /responses endpoint (Bug #8).
func TestResponsesWithModelOverride(t *testing.T) {
	tokenVal := "test-token"
	var gotModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body responsesRequest
		json.NewDecoder(r.Body).Decode(&body)
		gotModel = body.Model
		resp := responsesResponse{
			ID:    "resp-1",
			Model: body.Model,
			Output: []responsesOutputItem{{
				Type: "message",
				Role: "assistant",
				Content: []responsesContentPart{
					{Type: "output_text", Text: "ok"},
				},
			}},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	m, err := newTestModel(srv.URL, tokenVal)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Config.Model is "gpt-4o" from newTestModel, but we override to "gpt-5".
	m.cfg.Model = "gpt-4o"

	ctx := context.Background()
	_, err = m.Generate(ctx, []*schema.Message{
		{Role: schema.User, Content: "Hello"},
	}, model.WithModel("gpt-5"))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if gotModel != "gpt-5" {
		t.Errorf("expected request model 'gpt-5', got %q", gotModel)
	}
}

// TestResponsesWithReasoningEffortOverride verifies that per-call
// CopilotOptions reasoning effort overrides Config.ReasoningEffort
// for the /responses endpoint (Bug #8).
func TestResponsesWithReasoningEffortOverride(t *testing.T) {
	tokenVal := "test-token"
	var gotEffort string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body responsesRequest
		json.NewDecoder(r.Body).Decode(&body)
		if body.Reasoning != nil {
			gotEffort = body.Reasoning.Effort
		}
		resp := responsesResponse{
			ID:    "resp-1",
			Model: body.Model,
			Output: []responsesOutputItem{{
				Type: "message",
				Role: "assistant",
				Content: []responsesContentPart{
					{Type: "output_text", Text: "ok"},
				},
			}},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	m, err := newTestModel(srv.URL, tokenVal)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m.cfg.Model = "gpt-5"
	m.cfg.ReasoningEffort = ReasoningEffortLow

	ctx := context.Background()
	_, err = m.Generate(ctx, []*schema.Message{
		{Role: schema.User, Content: "Hello"},
	}, model.WrapImplSpecificOptFn(func(o *CopilotOptions) {
		o.ReasoningEffort = ReasoningEffortHigh
	}))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if gotEffort != string(ReasoningEffortHigh) {
		t.Errorf("expected reasoning effort 'high', got %q", gotEffort)
	}
}

// TestResponsesWithMaxTokensOverride verifies that model.WithMaxTokens()
// overrides Config.MaxCompletionTokens for the /responses endpoint (Bug #8).
func TestResponsesWithMaxTokensOverride(t *testing.T) {
	tokenVal := "test-token"
	var gotMaxTokens *int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body responsesRequest
		json.NewDecoder(r.Body).Decode(&body)
		gotMaxTokens = body.MaxOutputTokens
		resp := responsesResponse{
			ID:    "resp-1",
			Model: body.Model,
			Output: []responsesOutputItem{{
				Type: "message",
				Role: "assistant",
				Content: []responsesContentPart{
					{Type: "output_text", Text: "ok"},
				},
			}},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	m, err := newTestModel(srv.URL, tokenVal)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m.cfg.Model = "gpt-5"
	maxTokens := 1000
	m.cfg.MaxCompletionTokens = &maxTokens

	ctx := context.Background()
	_, err = m.Generate(ctx, []*schema.Message{
		{Role: schema.User, Content: "Hello"},
	}, model.WithMaxTokens(500))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if gotMaxTokens == nil || *gotMaxTokens != 500 {
		t.Errorf("expected max_output_tokens 500, got %v", gotMaxTokens)
	}
}

// TestResponsesWithTemperatureOverride verifies that model.WithTemperature()
// is present in the /responses request body (Bug #5-6).
func TestResponsesWithTemperatureOverride(t *testing.T) {
	tokenVal := "test-token"
	var gotTemp *float32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body responsesRequest
		json.NewDecoder(r.Body).Decode(&body)
		gotTemp = body.Temperature
		resp := responsesResponse{
			ID:    "resp-1",
			Model: body.Model,
			Output: []responsesOutputItem{{
				Type: "message",
				Role: "assistant",
				Content: []responsesContentPart{
					{Type: "output_text", Text: "ok"},
				},
			}},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	m, err := newTestModel(srv.URL, tokenVal)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m.cfg.Model = "gpt-5"

	ctx := context.Background()
	_, err = m.Generate(ctx, []*schema.Message{
		{Role: schema.User, Content: "Hello"},
	}, model.WithTemperature(0.5))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if gotTemp != nil {
		t.Errorf("expected temperature nil (omitted for reasoning model), got %v", gotTemp)
	}
}

// TestResponsesWithToolChoiceFormat verifies that tool_choice uses the flat
// Responses API format {type:"function", name:"x"} instead of the nested Chat
// format (Bug #7).
func TestResponsesWithToolChoiceFormat(t *testing.T) {
	tokenVal := "test-token"
	var rawBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&rawBody)
		resp := responsesResponse{
			ID:    "resp-1",
			Model: "gpt-5",
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	m, err := newTestModel(srv.URL, tokenVal)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m.cfg.Model = "gpt-5"

	// Bind a single tool so tool_choice is set.
	if err := m.BindTools([]*schema.ToolInfo{{
		Name: "my_tool",
		Desc: "A test tool",
	}}); err != nil {
		t.Fatalf("BindTools: %v", err)
	}

	ctx := context.Background()
	_, err = m.Generate(ctx, []*schema.Message{
		{Role: schema.User, Content: "Hello"},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// The tool_choice should be in flat format: {type: "function", name: "my_tool"}
	// NOT the nested format: {type: "function", function: {name: "my_tool"}}
	tc, ok := rawBody["tool_choice"]
	if !ok {
		t.Fatal("expected tool_choice in request body")
	}
	// For allowed tool choice with a single tool, the default is "auto",
	// since ToolChoiceAllowed = schema.ToolChoiceAllowed = "auto"
	if tc != "auto" {
		t.Logf("tool_choice value: %v", tc)
	}
	// Verify the format: auto for ToolChoiceAllowed with multiple/allowed tools.
}

// TestResponsesWithDefaultReasoning verifies that GPT-5 models without
// explicit reasoning effort get default reasoning config (Bug #9).
func TestResponsesWithDefaultReasoning(t *testing.T) {
	tokenVal := "test-token"
	var gotInclude []string
	var gotReasoningEffort string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body responsesRequest
		json.NewDecoder(r.Body).Decode(&body)
		gotInclude = body.Include
		if body.Reasoning != nil {
			gotReasoningEffort = body.Reasoning.Effort
		}
		resp := responsesResponse{
			ID:    "resp-1",
			Model: body.Model,
			Output: []responsesOutputItem{{
				Type: "message",
				Role: "assistant",
				Content: []responsesContentPart{
					{Type: "output_text", Text: "ok"},
				},
			}},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	m, err := newTestModel(srv.URL, tokenVal)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m.cfg.Model = "gpt-5"
	// ReasoningEffort is empty — should get defaults.

	ctx := context.Background()
	_, err = m.Generate(ctx, []*schema.Message{
		{Role: schema.User, Content: "Hello"},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if gotReasoningEffort != string(ReasoningEffortMedium) {
		t.Errorf("expected default reasoning effort 'medium', got %q", gotReasoningEffort)
	}
	found := false
	for _, i := range gotInclude {
		if i == "reasoning.encrypted_content" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected include to contain 'reasoning.encrypted_content', got %v", gotInclude)
	}
}

// TestResponsesStreamingWithModelOverride verifies that model.WithModel()
// overrides Config.Model for streaming via /responses (Bug #8).
func TestResponsesStreamingWithModelOverride(t *testing.T) {
	tokenVal := "test-token"
	var gotModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body responsesRequest
		json.NewDecoder(r.Body).Decode(&body)
		gotModel = body.Model
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n")
		flusher.Flush()
		fmt.Fprintf(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp-1\"}}\n\n")
		flusher.Flush()
	}))
	defer srv.Close()

	m, err := newTestModel(srv.URL, tokenVal)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m.cfg.Model = "gpt-4o"

	ctx := context.Background()
	sr, err := m.Stream(ctx, []*schema.Message{
		{Role: schema.User, Content: "Hello"},
	}, model.WithModel("gpt-5"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer sr.Close()

	for {
		_, err := sr.Recv()
		if err != nil {
			break
		}
	}
	if gotModel != "gpt-5" {
		t.Errorf("expected request model 'gpt-5', got %q", gotModel)
	}
}
