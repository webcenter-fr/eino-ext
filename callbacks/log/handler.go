// Package log provides a callbacks.Handler that logs eino component lifecycle
// events via logrus with structured fields, making it easy to inspect agent
// execution in production and development.
//
// Attach it globally once at startup with callbacks.AppendGlobalHandlers(h), or
// per-run with compose.WithCallbacks(h). The handler extracts the agent name
// from the context (set by components/middleware/agentattr or activity.WithAgent)
// and includes it in every log entry so multi-agent runs are easy to follow.
package log

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/sirupsen/logrus"

	"github.com/webcenter-fr/eino-ext/callbacks/activity"
)

const (
	maxContentLen = 500
	maxInputLen   = 2000
)

// Handler bridges eino component lifecycle callbacks into structured logrus
// log entries. It implements callbacks.Handler.
//
// Each log entry carries fields that identify the component, its name and type,
// and the agent that produced the event. ChatModel entries include token usage
// and content; Tool entries include arguments and response.
type Handler struct {
	logger *logrus.Entry
}

// compile-time interface check.
var _ callbacks.Handler = (*Handler)(nil)

// doneKeyType is an unexported context key type used by streaming tests to
// synchronize with the handler's background goroutines. Not used in production.
type doneKeyType struct{}

var doneKey = doneKeyType{}

// withDoneChan returns a context carrying a channel that is closed when the
// handler's streaming goroutine completes. This is an internal test helper.
func withDoneChan(ctx context.Context) (context.Context, chan struct{}) {
	ch := make(chan struct{})
	return context.WithValue(ctx, doneKey, ch), ch
}

// signalDone checks ctx for a done channel and closes it if present.
func signalDone(ctx context.Context) {
	if ch, ok := ctx.Value(doneKey).(chan struct{}); ok {
		close(ch)
	}
}

// NewHandler returns a Handler that logs at Trace level for lifecycle events
// and Debug level for errors, using logger as the base entry.
func NewHandler(logger *logrus.Entry) *Handler {
	return &Handler{logger: logger}
}

// OnStart handles the non-streaming start timing.
func (h *Handler) OnStart(ctx context.Context, info *callbacks.RunInfo, input callbacks.CallbackInput) context.Context {
	fields := commonFields(ctx, info)

	switch componentOf(info) {
	case components.ComponentOfChatModel:
		if mi := model.ConvCallbackInput(input); mi != nil && mi.Messages != nil {
			fields["messages"] = len(mi.Messages)
		}
		h.logger.WithFields(fields).Trace("chat model start")

	case components.ComponentOfTool:
		if ti := tool.ConvCallbackInput(input); ti != nil {
			fields["input"] = truncate(ti.ArgumentsInJSON, maxInputLen)
		}
		h.logger.WithFields(fields).Trace("tool start")

	default:
		h.logger.WithFields(fields).Trace("component start")
	}
	return ctx
}

// OnEnd handles the non-streaming end timing.
func (h *Handler) OnEnd(ctx context.Context, info *callbacks.RunInfo, output callbacks.CallbackOutput) context.Context {
	fields := commonFields(ctx, info)

	switch componentOf(info) {
	case components.ComponentOfChatModel:
		mo := model.ConvCallbackOutput(output)
		if mo == nil {
			return ctx
		}
		enrichChatModelFields(fields, mo.Message, mo.TokenUsage)
		h.logger.WithFields(fields).Trace("chat model end")

	case components.ComponentOfTool:
		if to := tool.ConvCallbackOutput(output); to != nil {
			fields["output"] = truncate(to.Response, maxContentLen)
		}
		h.logger.WithFields(fields).Trace("tool end")

	default:
		h.logger.WithFields(fields).Trace("component end")
	}
	return ctx
}

// OnError handles the error timing.
func (h *Handler) OnError(ctx context.Context, info *callbacks.RunInfo, err error) context.Context {
	fields := commonFields(ctx, info)
	fields["error"] = err.Error()
	h.logger.WithFields(fields).Debug("component error")
	return ctx
}

// OnStartWithStreamInput handles the streamed tool-call input timing.
// It MUST close the copied reader to avoid leaking the pipeline.
func (h *Handler) OnStartWithStreamInput(ctx context.Context, info *callbacks.RunInfo, input *schema.StreamReader[callbacks.CallbackInput]) context.Context {
	if componentOf(info) != components.ComponentOfTool {
		input.Close()
		return ctx
	}

	fields := commonFields(ctx, info)

	go func() {
		defer input.Close()
		defer signalDone(ctx)
		var buf strings.Builder
		for {
			chunk, err := input.Recv()
			if err == io.EOF {
				break
			}
			if err != nil {
				h.logger.WithFields(fields).WithError(err).Debug("tool stream input error")
				return
			}
			ti := tool.ConvCallbackInput(chunk)
			if ti != nil && ti.ArgumentsInJSON != "" {
				buf.WriteString(ti.ArgumentsInJSON)
			}
		}
		if buf.Len() > 0 {
			fields["input"] = truncate(buf.String(), maxInputLen)
		}
		h.logger.WithFields(fields).Trace("tool stream input end")
	}()

	return ctx
}

// OnEndWithStreamOutput handles the streamed model output timing.
// It MUST close the copied reader to avoid leaking the pipeline.
func (h *Handler) OnEndWithStreamOutput(ctx context.Context, info *callbacks.RunInfo, output *schema.StreamReader[callbacks.CallbackOutput]) context.Context {
	if componentOf(info) != components.ComponentOfChatModel {
		output.Close()
		return ctx
	}

	fields := commonFields(ctx, info)

	go func() {
		defer output.Close()
		defer signalDone(ctx)
		var (
			textBuf, reasonBuf strings.Builder
			usage              *model.TokenUsage
		)
		for {
			chunk, err := output.Recv()
			if err == io.EOF {
				break
			}
			if err != nil {
				h.logger.WithFields(fields).WithError(err).Debug("model stream output error")
				return
			}
			mo := model.ConvCallbackOutput(chunk)
			if mo == nil {
				continue
			}
			if mo.TokenUsage != nil {
				usage = mo.TokenUsage
			}
			if mo.Message == nil {
				continue
			}
			if mo.Message.ReasoningContent != "" {
				reasonBuf.WriteString(mo.Message.ReasoningContent)
			}
			if mo.Message.Content != "" {
				textBuf.WriteString(mo.Message.Content)
			}
		}
		enrichChatModelFields(fields, &schema.Message{
			Content:          textBuf.String(),
			ReasoningContent: reasonBuf.String(),
		}, usage)
		h.logger.WithFields(fields).Trace("chat model stream end")
	}()

	return ctx
}

func componentOf(info *callbacks.RunInfo) components.Component {
	if info == nil {
		return ""
	}
	return info.Component
}

func commonFields(ctx context.Context, info *callbacks.RunInfo) logrus.Fields {
	fields := logrus.Fields{}

	if info != nil {
		fields["component"] = string(info.Component)
		if info.Name != "" {
			fields["component_name"] = info.Name
		}
		if info.Type != "" {
			fields["component_type"] = info.Type
		}
	}

	if agent, ok := activity.AgentFromContext(ctx); ok && agent != "" {
		fields["agent"] = agent
	}

	return fields
}

func enrichChatModelFields(fields logrus.Fields, msg *schema.Message, usage *model.TokenUsage) {
	if msg != nil {
		if msg.Content != "" {
			fields["content"] = truncate(msg.Content, maxContentLen)
		}
		if msg.ReasoningContent != "" {
			fields["reasoning"] = truncate(msg.ReasoningContent, maxContentLen)
		}
		if msg.ResponseMeta != nil && msg.ResponseMeta.FinishReason != "" {
			fields["finish_reason"] = msg.ResponseMeta.FinishReason
		}
	}
	if usage != nil {
		fields["prompt_tokens"] = usage.PromptTokens
		fields["completion_tokens"] = usage.CompletionTokens
		fields["total_tokens"] = usage.TotalTokens
	}
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return fmt.Sprintf("%s…[truncated %d total chars]", s[:maxLen], len(s))
}
