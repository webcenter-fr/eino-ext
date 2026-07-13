// Package oteltrace provides a callbacks.Handler that records OpenTelemetry
// spans for eino component lifecycle: a span per chat-model generate (with
// token-usage attributes) and a span per tool call (with name + error
// attributes). Attach globally once with callbacks.AppendGlobalHandlers, or
// per-run with compose.WithCallbacks. The default tracer comes from
// trace/global.TracerProvider(); inject one via Config for tests.
package oteltrace

import (
	"context"
	"fmt"
	"io"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/webcenter-fr/eino-ext/callbacks/activity"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
)

// Config for NewHandler.
type Config struct {
	TracerProvider trace.TracerProvider `json:"-"`
	TracerName     string               `json:"tracerName"     validate:"omitempty"`
	SpanKindClient bool                 `json:"spanKindClient"`
	IncludeToolIO  bool                 `json:"includeToolIO"`
	MaxSpanIO      int                  `json:"maxSpanIO"      validate:"omitempty,gte=1"`
}

// Handler bridges eino component lifecycle callbacks into OpenTelemetry spans.
type Handler struct {
	tracer trace.Tracer
	cfg    Config
}

var (
	_ callbacks.Handler       = (*Handler)(nil)
	_ callbacks.TimingChecker = (*Handler)(nil)
)

// spanCtxKey is an unexported context key for storing the current span.
type spanCtxKey struct{}

// doneKeyType is an unexported context key used by streaming tests to
// synchronize with the handler's background goroutines.
type doneKeyType struct{}

var doneKey = doneKeyType{}

// withDoneChan returns a context carrying a channel that is closed when the
// handler's streaming goroutine completes.
func withDoneChan(ctx context.Context) (context.Context, chan struct{}) {
	ch := make(chan struct{})
	return context.WithValue(ctx, doneKey, ch), ch
}

func signalDone(ctx context.Context) {
	if ch, ok := ctx.Value(doneKey).(chan struct{}); ok {
		close(ch)
	}
}

// NewHandler applies defaults (TracerName="eino-ext", SpanKindClient=true,
// IncludeToolIO=false, MaxSpanIO=500) then validates via validate.Struct.
func NewHandler(ctx context.Context, cfg *Config) (*Handler, error) {
	applyDefaults := false
	if cfg == nil {
		cfg = &Config{}
		applyDefaults = true
	}
	if cfg.TracerName == "" {
		cfg.TracerName = "eino-ext"
	}
	if cfg.MaxSpanIO == 0 {
		cfg.MaxSpanIO = 500
	}
	if applyDefaults {
		cfg.SpanKindClient = true
	}
	if cfg.TracerProvider == nil {
		cfg.TracerProvider = trace.NewNoopTracerProvider()
	}
	if err := validate.Struct(cfg); err != nil {
		return nil, err
	}
	tracer := cfg.TracerProvider.Tracer(cfg.TracerName)
	return &Handler{tracer: tracer, cfg: *cfg}, nil
}

// Needed returns true for non-stream timings always. For stream timings,
// returns true only when the current context carries a recording span.
func (h *Handler) Needed(ctx context.Context, info *callbacks.RunInfo, timing callbacks.CallbackTiming) bool {
	switch timing {
	case callbacks.TimingOnStartWithStreamInput, callbacks.TimingOnEndWithStreamOutput:
		span := trace.SpanFromContext(ctx)
		return span.IsRecording()
	default:
		return true
	}
}

func componentOf(info *callbacks.RunInfo) components.Component {
	if info == nil {
		return ""
	}
	return info.Component
}

func modelName(info *callbacks.RunInfo) string {
	if info == nil {
		return ""
	}
	if info.Type != "" {
		return info.Type
	}
	return info.Name
}

func toolName(info *callbacks.RunInfo) string {
	if info == nil {
		return ""
	}
	if info.Name != "" {
		return info.Name
	}
	return info.Type
}

func commonAttrs(ctx context.Context, info *callbacks.RunInfo) []attribute.KeyValue {
	attrs := []attribute.KeyValue{}
	if info != nil {
		attrs = append(attrs, attribute.String("component", string(info.Component)))
	}
	if agent, ok := activity.AgentFromContext(ctx); ok && agent != "" {
		attrs = append(attrs, attribute.String("agent", agent))
	}
	if sessionID, ok := activity.SessionFromContext(ctx); ok && sessionID != "" {
		attrs = append(attrs, attribute.String("session.id", sessionID))
	}
	return attrs
}

// OnStart handles the non-streaming start timing.
func (h *Handler) OnStart(ctx context.Context, info *callbacks.RunInfo, input callbacks.CallbackInput) context.Context {
	switch componentOf(info) {
	case components.ComponentOfChatModel:
		attrs := commonAttrs(ctx, info)
		attrs = append(attrs, attribute.String("gen_ai.request.model", modelName(info)))
		ctx, span := h.tracer.Start(ctx, "chat_model.generate",
			trace.WithSpanKind(trace.SpanKindInternal),
			trace.WithAttributes(attrs...),
		)
		return context.WithValue(ctx, spanCtxKey{}, span)

	case components.ComponentOfTool:
		attrs := commonAttrs(ctx, info)
		name := toolName(info)
		if name == "" {
			name = "tool"
		}
		attrs = append(attrs, attribute.String("tool.name", name))
		spanKind := trace.SpanKindInternal
		if h.cfg.SpanKindClient {
			spanKind = trace.SpanKindClient
		}
		ctx, span := h.tracer.Start(ctx, fmt.Sprintf("tool.%s", name),
			trace.WithSpanKind(spanKind),
			trace.WithAttributes(attrs...),
		)
		return context.WithValue(ctx, spanCtxKey{}, span)

	default:
		return ctx
	}
}

// OnEnd handles the non-streaming end timing.
func (h *Handler) OnEnd(ctx context.Context, info *callbacks.RunInfo, output callbacks.CallbackOutput) context.Context {
	span, _ := ctx.Value(spanCtxKey{}).(trace.Span)
	if span == nil {
		return ctx
	}
	defer span.End()

	switch componentOf(info) {
	case components.ComponentOfChatModel:
		mo := model.ConvCallbackOutput(output)
		if mo == nil {
			return ctx
		}
		if mo.Message != nil && mo.Message.ResponseMeta != nil && mo.Message.ResponseMeta.FinishReason != "" {
			span.SetAttributes(attribute.String("gen_ai.response.finish_reason", mo.Message.ResponseMeta.FinishReason))
		}
		if mo.TokenUsage != nil {
			span.SetAttributes(
				attribute.Int("gen_ai.usage.prompt_tokens", mo.TokenUsage.PromptTokens),
				attribute.Int("gen_ai.usage.completion_tokens", mo.TokenUsage.CompletionTokens),
				attribute.Int("gen_ai.usage.total_tokens", mo.TokenUsage.TotalTokens),
			)
		}

	case components.ComponentOfTool:
		if h.cfg.IncludeToolIO {
			if to := tool.ConvCallbackOutput(output); to != nil && to.Response != "" {
				span.SetAttributes(attribute.String("tool.response", truncate(to.Response, h.cfg.MaxSpanIO)))
			}
		}
	}
	return ctx
}

// OnError handles the error timing.
func (h *Handler) OnError(ctx context.Context, info *callbacks.RunInfo, err error) context.Context {
	span, _ := ctx.Value(spanCtxKey{}).(trace.Span)
	if span == nil {
		return ctx
	}
	defer span.End()

	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
	return ctx
}

// OnStartWithStreamInput handles streamed tool-call arguments.
func (h *Handler) OnStartWithStreamInput(ctx context.Context, info *callbacks.RunInfo, input *schema.StreamReader[callbacks.CallbackInput]) context.Context {
	if componentOf(info) != components.ComponentOfTool {
		input.Close()
		return ctx
	}

	attrs := commonAttrs(ctx, info)
	name := toolName(info)
	if name == "" {
		name = "tool"
	}
	attrs = append(attrs, attribute.String("tool.name", name))
	spanKind := trace.SpanKindInternal
	if h.cfg.SpanKindClient {
		spanKind = trace.SpanKindClient
	}
	ctx, span := h.tracer.Start(ctx, fmt.Sprintf("tool.%s", name),
		trace.WithSpanKind(spanKind),
		trace.WithAttributes(attrs...),
	)

	go func() {
		defer input.Close()
		defer span.End()
		defer signalDone(ctx)
		for {
			_, err := input.Recv()
			if err == io.EOF {
				break
			}
			if err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
				return
			}
		}
	}()

	return context.WithValue(ctx, spanCtxKey{}, span)
}

// OnEndWithStreamOutput handles streamed model output.
func (h *Handler) OnEndWithStreamOutput(ctx context.Context, info *callbacks.RunInfo, output *schema.StreamReader[callbacks.CallbackOutput]) context.Context {
	if componentOf(info) != components.ComponentOfChatModel {
		output.Close()
		return ctx
	}

	span, _ := ctx.Value(spanCtxKey{}).(trace.Span)
	if span == nil {
		output.Close()
		return ctx
	}

	go func() {
		defer output.Close()
		defer span.End()
		defer signalDone(ctx)

		var (
			usage  *model.TokenUsage
			finish string
		)
		for {
			chunk, err := output.Recv()
			if err == io.EOF {
				break
			}
			if err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
				return
			}
			mo := model.ConvCallbackOutput(chunk)
			if mo == nil {
				continue
			}
			if mo.TokenUsage != nil {
				usage = mo.TokenUsage
			}
			if mo.Message != nil && mo.Message.ResponseMeta != nil {
				if mo.Message.ResponseMeta.FinishReason != "" {
					finish = mo.Message.ResponseMeta.FinishReason
				}
				if usage == nil && mo.Message.ResponseMeta.Usage != nil {
					usage = convUsage(mo.Message.ResponseMeta.Usage)
				}
			}
		}
		if finish != "" {
			span.SetAttributes(attribute.String("gen_ai.response.finish_reason", finish))
		}
		if usage != nil {
			span.SetAttributes(
				attribute.Int("gen_ai.usage.prompt_tokens", usage.PromptTokens),
				attribute.Int("gen_ai.usage.completion_tokens", usage.CompletionTokens),
				attribute.Int("gen_ai.usage.total_tokens", usage.TotalTokens),
			)
		}
	}()

	return ctx
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return fmt.Sprintf("%s…[truncated %d total chars]", s[:maxLen], len(s))
}

func convUsage(u *schema.TokenUsage) *model.TokenUsage {
	if u == nil {
		return nil
	}
	return &model.TokenUsage{
		PromptTokens:     u.PromptTokens,
		CompletionTokens: u.CompletionTokens,
		TotalTokens:      u.TotalTokens,
	}
}

