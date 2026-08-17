package oteltrace

import (
	"context"
	"io"
	"testing"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/webcenter-fr/eino-ext/callbacks/activity"
)

func setupTracer(t *testing.T) (*tracetest.InMemoryExporter, trace.TracerProvider) {
	t.Helper()
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exp),
	)
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	return exp, tp
}

func newHandlerForTest(t *testing.T, tp trace.TracerProvider, overrides ...func(*Config)) *Handler {
	t.Helper()
	cfg := &Config{
		TracerProvider: tp,
		SpanKindClient: true,
	}
	for _, fn := range overrides {
		fn(cfg)
	}
	h, err := NewHandler(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	return h
}

func collectSpans(t *testing.T, exp *tracetest.InMemoryExporter) tracetest.SpanStubs {
	t.Helper()
	spans := exp.GetSpans()
	exp.Reset()
	return spans
}

func attrValue(spans tracetest.SpanStubs, key string) (string, bool) {
	for _, s := range spans {
		for _, a := range s.Attributes {
			if string(a.Key) == key {
				return a.Value.Emit(), true
			}
		}
	}
	return "", false
}

func TestHandlerOnStartOnEndChatModel(t *testing.T) {
	exp, tp := setupTracer(t)
	h := newHandlerForTest(t, tp)

	info := &callbacks.RunInfo{
		Name:      "my-chat",
		Type:      "openai/gpt-4o",
		Component: components.ComponentOfChatModel,
	}
	ctx := activity.WithSession(context.Background(), "sess-1")
	ctx = activity.WithAgent(ctx, "test-agent")

	ctx = h.OnStart(ctx, info, nil)
	output := &model.CallbackOutput{
		Message: &schema.Message{
			Role:    schema.Assistant,
			Content: "Hello",
			ResponseMeta: &schema.ResponseMeta{
				FinishReason: "stop",
			},
		},
		TokenUsage: &model.TokenUsage{
			PromptTokens:     10,
			CompletionTokens: 5,
			TotalTokens:      15,
		},
	}
	h.OnEnd(ctx, info, output)

	spans := collectSpans(t, exp)
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	s := spans[0]
	if s.Name != "chat_model.generate" {
		t.Errorf("expected span name 'chat_model.generate', got %q", s.Name)
	}
	if s.SpanKind != trace.SpanKindInternal {
		t.Errorf("expected SpanKindInternal, got %v", s.SpanKind)
	}
	if v, ok := attrValue(spans, "gen_ai.request.model"); !ok || v != "openai/gpt-4o" {
		t.Errorf("expected gen_ai.request.model='openai/gpt-4o', got %q (ok=%v)", v, ok)
	}
	if v, ok := attrValue(spans, "agent"); !ok || v != "test-agent" {
		t.Errorf("expected agent='test-agent', got %q (ok=%v)", v, ok)
	}
	if v, ok := attrValue(spans, "session.id"); !ok || v != "sess-1" {
		t.Errorf("expected session.id='sess-1', got %q (ok=%v)", v, ok)
	}
	if v, ok := attrValue(spans, "gen_ai.response.finish_reason"); !ok || v != "stop" {
		t.Errorf("expected finish_reason='stop', got %q (ok=%v)", v, ok)
	}
	if v, ok := attrValue(spans, "gen_ai.usage.total_tokens"); !ok || v != "15" {
		t.Errorf("expected total_tokens='15', got %q (ok=%v)", v, ok)
	}
}

func TestHandlerOnError(t *testing.T) {
	exp, tp := setupTracer(t)
	h := newHandlerForTest(t, tp)

	info := &callbacks.RunInfo{
		Component: components.ComponentOfChatModel,
		Type:      "openai/gpt-4o",
	}
	ctx := h.OnStart(context.Background(), info, nil)
	h.OnError(ctx, info, io.ErrUnexpectedEOF)

	spans := collectSpans(t, exp)
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	s := spans[0]
	if s.Status.Code != codes.Error {
		t.Errorf("expected status Error, got %v", s.Status.Code)
	}
	if len(s.Events) < 1 {
		t.Error("expected at least one event (exception)")
	}
}

func TestHandlerOnStartOnEndTool(t *testing.T) {
	exp, tp := setupTracer(t)
	h := newHandlerForTest(t, tp)

	info := &callbacks.RunInfo{
		Name:      "get_weather",
		Type:      "weather_api",
		Component: components.ComponentOfTool,
	}
	ctx := h.OnStart(context.Background(), info, nil)
	output := &tool.CallbackOutput{
		Response: "Sunny, 22°C",
	}
	h.OnEnd(ctx, info, output)

	spans := collectSpans(t, exp)
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	s := spans[0]
	if s.Name != "tool.get_weather" {
		t.Errorf("expected span name 'tool.get_weather', got %q", s.Name)
	}
	if s.SpanKind != trace.SpanKindClient {
		t.Errorf("expected SpanKindClient, got %v", s.SpanKind)
	}
	if v, ok := attrValue(spans, "tool.name"); !ok || v != "get_weather" {
		t.Errorf("expected tool.name='get_weather', got %q (ok=%v)", v, ok)
	}
	if _, ok := attrValue(spans, "tool.response"); ok {
		t.Error("expected no tool.response attr when IncludeToolIO=false")
	}
}

func TestHandlerToolWithIO(t *testing.T) {
	exp, tp := setupTracer(t)
	h := newHandlerForTest(t, tp, func(c *Config) {
		c.IncludeToolIO = true
		c.MaxSpanIO = 10
	})

	info := &callbacks.RunInfo{
		Name:      "get_weather",
		Component: components.ComponentOfTool,
	}
	ctx := h.OnStart(context.Background(), info, nil)
	output := &tool.CallbackOutput{
		Response: "This is a very long response that should be truncated",
	}
	h.OnEnd(ctx, info, output)

	spans := collectSpans(t, exp)
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	v, ok := attrValue(spans, "tool.response")
	if !ok {
		t.Fatal("expected tool.response attr")
	}
	if v == "This is a very long response that should be truncated" {
		t.Errorf("expected truncated response, got untruncated %q", v)
	}
}

func TestHandlerOnEndWithStreamOutput(t *testing.T) {
	exp, tp := setupTracer(t)
	h := newHandlerForTest(t, tp)

	info := &callbacks.RunInfo{
		Component: components.ComponentOfChatModel,
		Type:      "openai/gpt-4o",
	}
	ctx := h.OnStart(context.Background(), info, nil)
	ctx, done := withDoneChan(ctx)

	output := schema.StreamReaderFromArray([]callbacks.CallbackOutput{
		&model.CallbackOutput{Message: &schema.Message{Content: "Hello"}},
		&model.CallbackOutput{Message: &schema.Message{Content: " world"}},
		&model.CallbackOutput{TokenUsage: &model.TokenUsage{
			PromptTokens:     5,
			CompletionTokens: 2,
			TotalTokens:      7,
		}},
	})

	h.OnEndWithStreamOutput(ctx, info, output)
	<-done

	spans := collectSpans(t, exp)
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	s := spans[0]
	if s.Name != "chat_model.generate" {
		t.Errorf("expected span name 'chat_model.generate', got %q", s.Name)
	}
	if v, ok := attrValue(spans, "gen_ai.usage.total_tokens"); !ok || v != "7" {
		t.Errorf("expected total_tokens='7', got %q (ok=%v)", v, ok)
	}
}

func TestHandlerStreamOutputError(t *testing.T) {
	exp, tp := setupTracer(t)
	h := newHandlerForTest(t, tp)

	info := &callbacks.RunInfo{
		Component: components.ComponentOfChatModel,
		Type:      "openai/gpt-4o",
	}
	ctx := h.OnStart(context.Background(), info, nil)
	ctx, done := withDoneChan(ctx)

	output := schema.StreamReaderFromArray([]callbacks.CallbackOutput{
		&model.CallbackOutput{Message: &schema.Message{Content: "Hello"}},
	})
	output.Close()

	h.OnEndWithStreamOutput(ctx, info, output)
	<-done

	spans := collectSpans(t, exp)
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
}

func TestHandlerNestedSpans(t *testing.T) {
	exp, tp := setupTracer(t)
	h := newHandlerForTest(t, tp)

	modelInfo := &callbacks.RunInfo{
		Name:      "model-1",
		Type:      "openai/gpt-4o",
		Component: components.ComponentOfChatModel,
	}
	toolInfo := &callbacks.RunInfo{
		Name:      "tool-1",
		Component: components.ComponentOfTool,
	}

	ctx := h.OnStart(context.Background(), modelInfo, nil)
	modelCtx := ctx
	ctx = h.OnStart(modelCtx, toolInfo, nil)
	h.OnEnd(ctx, toolInfo, &tool.CallbackOutput{})

	modelOutput := &model.CallbackOutput{
		Message:    &schema.Message{Role: schema.Assistant, Content: "ok"},
		TokenUsage: &model.TokenUsage{PromptTokens: 5, CompletionTokens: 1, TotalTokens: 6},
	}
	h.OnEnd(modelCtx, modelInfo, modelOutput)

	spans := collectSpans(t, exp)
	if len(spans) != 2 {
		t.Fatalf("expected 2 spans, got %d", len(spans))
	}

	var modelSpan, toolSpan tracetest.SpanStub
	foundModel, foundTool := false, false
	for _, s := range spans {
		switch s.Name {
		case "chat_model.generate":
			modelSpan = s
			foundModel = true
		case "tool.tool-1":
			toolSpan = s
			foundTool = true
		}
	}
	if !foundModel || !foundTool {
		t.Fatal("both spans should exist")
	}
	if toolSpan.Parent.SpanID() != modelSpan.SpanContext.SpanID() {
		t.Errorf("tool span parent (%v) should match model span id (%v)",
			toolSpan.Parent.SpanID(), modelSpan.SpanContext.SpanID())
	}
}

func TestHandlerNeeded(t *testing.T) {
	h, err := NewHandler(context.Background(), &Config{
		TracerProvider: noop.NewTracerProvider(),
		SpanKindClient: true,
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	info := &callbacks.RunInfo{Component: components.ComponentOfChatModel}
	if h.Needed(context.Background(), info, callbacks.TimingOnStartWithStreamInput) {
		t.Error("expected Needed=false for stream timing with no span in context")
	}
	if h.Needed(context.Background(), info, callbacks.TimingOnEndWithStreamOutput) {
		t.Error("expected Needed=false for stream timing with no span in context")
	}
	if !h.Needed(context.Background(), info, callbacks.TimingOnStart) {
		t.Error("expected Needed=true for OnStart")
	}
	if !h.Needed(context.Background(), info, callbacks.TimingOnEnd) {
		t.Error("expected Needed=true for OnEnd")
	}
	if !h.Needed(context.Background(), info, callbacks.TimingOnError) {
		t.Error("expected Needed=true for OnError")
	}
}

func TestHandlerConfigValidation(t *testing.T) {
	_, err := NewHandler(context.Background(), &Config{
		TracerProvider: noop.NewTracerProvider(),
		MaxSpanIO:      0,
		SpanKindClient: true,
	})
	if err != nil {
		t.Fatalf("MaxSpanIO=0 should be allowed: %v", err)
	}

	_, err = NewHandler(context.Background(), &Config{
		TracerProvider: noop.NewTracerProvider(),
		MaxSpanIO:      -1,
		SpanKindClient: true,
	})
	if err == nil {
		t.Fatal("MaxSpanIO=-1 should fail validation (gte=1)")
	}
}

func TestHandlerStreamInputNonTool(t *testing.T) {
	h, err := NewHandler(context.Background(), &Config{
		TracerProvider: noop.NewTracerProvider(),
		SpanKindClient: true,
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	info := &callbacks.RunInfo{Component: components.ComponentOfChatModel}
	input := schema.StreamReaderFromArray([]callbacks.CallbackInput{"ignored"})

	h.OnStartWithStreamInput(context.Background(), info, input)

	_, err = input.Recv()
	if err != nil {
		t.Errorf("expected stream readable after close, got: %v", err)
	}
}

func TestHandlerStreamOutputNonChatModel(t *testing.T) {
	h, err := NewHandler(context.Background(), &Config{
		TracerProvider: noop.NewTracerProvider(),
		SpanKindClient: true,
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	info := &callbacks.RunInfo{Component: components.ComponentOfTool}
	output := schema.StreamReaderFromArray([]callbacks.CallbackOutput{"ignored"})

	h.OnEndWithStreamOutput(context.Background(), info, output)

	_, err = output.Recv()
	if err != nil {
		t.Errorf("expected stream readable after close, got: %v", err)
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		maxLen int
		wantOK bool
	}{
		{"short", "hello", 10, true},
		{"exact", "hello", 5, true},
		{"long", "this is a long string", 6, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncate(tt.input, tt.maxLen)
			if tt.wantOK {
				if got != tt.input {
					t.Errorf("expected no truncation, got %q", got)
				}
			} else {
				if got == tt.input {
					t.Errorf("expected truncation, got unchanged %q", got)
				}
			}
		})
	}
}

func TestHandlerToolNameFromType(t *testing.T) {
	exp, tp := setupTracer(t)
	h := newHandlerForTest(t, tp)

	info := &callbacks.RunInfo{
		Type:      "weather_api",
		Component: components.ComponentOfTool,
	}
	ctx := h.OnStart(context.Background(), info, nil)
	h.OnEnd(ctx, info, &tool.CallbackOutput{})

	spans := collectSpans(t, exp)
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	if spans[0].Name != "tool.weather_api" {
		t.Errorf("expected span name 'tool.weather_api', got %q", spans[0].Name)
	}
}

func TestHandlerNoInfoTolerated(t *testing.T) {
	_, tp := setupTracer(t)
	h := newHandlerForTest(t, tp)

	ctx := h.OnStart(context.Background(), nil, nil)
	_ = ctx
}

func TestHandlerSpanKindClientDefault(t *testing.T) {
	exp, tp := setupTracer(t)

	h, err := NewHandler(context.Background(), nil)
	if err != nil {
		t.Fatalf("NewHandler (nil config): %v", err)
	}
	h.tracer = tp.Tracer("test")

	info := &callbacks.RunInfo{
		Name:      "t1",
		Component: components.ComponentOfTool,
	}
	ctx := h.OnStart(context.Background(), info, nil)
	h.OnEnd(ctx, info, &tool.CallbackOutput{})

	spans := collectSpans(t, exp)
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	if spans[0].SpanKind != trace.SpanKindClient {
		t.Errorf("default SpanKind should be Client, got %v", spans[0].SpanKind)
	}
}

var (
	_ callbacks.Handler       = (*Handler)(nil)
	_ callbacks.TimingChecker = (*Handler)(nil)
)

func TestDefaultConfig(t *testing.T) {
	h, err := NewHandler(context.Background(), nil)
	if err != nil {
		t.Fatalf("NewHandler(nil): %v", err)
	}
	if h.cfg.TracerName != "eino-ext" {
		t.Errorf("expected TracerName='eino-ext', got %q", h.cfg.TracerName)
	}
	if !h.cfg.SpanKindClient {
		t.Error("expected SpanKindClient=true by default")
	}
	if h.cfg.MaxSpanIO != 500 {
		t.Errorf("expected MaxSpanIO=500, got %d", h.cfg.MaxSpanIO)
	}
}
