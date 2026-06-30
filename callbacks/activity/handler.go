/*
 * Copyright 2025 CloudWeGo Authors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package activity

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync/atomic"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// Handler bridges eino component lifecycle callbacks into activity events on a
// Bus. It implements callbacks.Handler and callbacks.TimingChecker.
//
// Attach it globally once at startup with callbacks.AppendGlobalHandlers(h)
// (not thread-safe), or per run with compose.WithCallbacks(h). Correlate runs to
// fan-out buckets by setting WithSession on the context passed to the run.
//
// Invariants honored here, per the eino callbacks contract:
//   - copied stream readers are always Close()d (else the pipeline leaks);
//   - callback Input/Output are never mutated (shared pointers);
//   - start/end pairing uses only the SAME handler's context chain.
type Handler struct {
	bus Bus
	ids atomic.Uint64
}

// NewHandler returns a Handler publishing to bus.
func NewHandler(bus Bus) *Handler {
	return &Handler{bus: bus}
}

// compile-time checks.
var (
	_ callbacks.Handler       = (*Handler)(nil)
	_ callbacks.TimingChecker = (*Handler)(nil)
)

// SubscriberCounter is an optional Bus capability letting the Handler skip
// stream-copy overhead when nobody is listening to a session.
type SubscriberCounter interface {
	HasSubscribers(sessionID string) bool
}

// ctxKey is an unexported context key type for start/end pairing.
type ctxKey string

const ctxKeyCallID ctxKey = "activity.callID"

func (h *Handler) newID(prefix string) string {
	return fmt.Sprintf("%s_%d", prefix, h.ids.Add(1))
}

// stepFailedData builds the StepFailed payload from an error, keeping the error
// shape consistent across the OnError and streaming-error paths.
func stepFailedData(err error) StepFailed {
	return StepFailed{Error: ErrorData{Type: "unknown", Message: errString(err)}}
}

func (h *Handler) publish(ctx context.Context, t Type, data any) {
	sessionID, _ := SessionFromContext(ctx)
	h.bus.Publish(ctx, Event{SessionID: sessionID, Type: t, Data: data})
}

// Needed reports whether a timing is worth setting up. Coarse timings are always
// needed (so lifecycle events still buffer for replay); the expensive stream
// timings are skipped when the Bus reports no subscribers for the session.
func (h *Handler) Needed(ctx context.Context, info *callbacks.RunInfo, timing callbacks.CallbackTiming) bool {
	switch timing {
	case callbacks.TimingOnStartWithStreamInput, callbacks.TimingOnEndWithStreamOutput:
		if sc, ok := h.bus.(SubscriberCounter); ok {
			sessionID, _ := SessionFromContext(ctx)
			return sc.HasSubscribers(sessionID)
		}
		return true
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

// OnStart handles the non-streaming start timing.
func (h *Handler) OnStart(ctx context.Context, info *callbacks.RunInfo, input callbacks.CallbackInput) context.Context {
	switch componentOf(info) {
	case components.ComponentOfChatModel:
		h.publish(ctx, TypeStepStarted, StepStarted{Model: modelName(info)})
		h.publish(ctx, TypeTextStarted, TextStarted{})
	case components.ComponentOfTool:
		callID := h.newID("call")
		name := toolName(info)
		h.publish(ctx, TypeToolInputStarted, ToolInputStarted{CallID: callID, Name: name})
		var args string
		if ti := tool.ConvCallbackInput(input); ti != nil {
			args = ti.ArgumentsInJSON
			h.publish(ctx, TypeToolInputEnded, ToolInputEnded{CallID: callID, Text: args})
		}
		h.publish(ctx, TypeToolCalled, ToolCalled{CallID: callID, Tool: name, Input: args})
		return context.WithValue(ctx, ctxKeyCallID, callID)
	}
	return ctx
}

// OnEnd handles the non-streaming end timing.
func (h *Handler) OnEnd(ctx context.Context, info *callbacks.RunInfo, output callbacks.CallbackOutput) context.Context {
	switch componentOf(info) {
	case components.ComponentOfChatModel:
		mo := model.ConvCallbackOutput(output)
		if mo == nil {
			return ctx
		}
		if mo.Message != nil && mo.Message.ReasoningContent != "" {
			rid := h.newID("reasoning")
			h.publish(ctx, TypeReasoningStarted, ReasoningStarted{ReasoningID: rid})
			h.publish(ctx, TypeReasoningDelta, ReasoningDelta{ReasoningID: rid, Delta: mo.Message.ReasoningContent})
			h.publish(ctx, TypeReasoningEnded, ReasoningEnded{ReasoningID: rid, Text: mo.Message.ReasoningContent})
		}
		text := ""
		if mo.Message != nil {
			text = mo.Message.Content
		}
		h.publish(ctx, TypeTextEnded, TextEnded{Text: text})
		h.publish(ctx, TypeStepEnded, stepEnded(finishReason(mo), mo.TokenUsage))
	case components.ComponentOfTool:
		callID, _ := ctx.Value(ctxKeyCallID).(string)
		var content string
		if to := tool.ConvCallbackOutput(output); to != nil {
			content = to.Response
		}
		h.publish(ctx, TypeToolSuccess, ToolSuccess{CallID: callID, Content: content})
	}
	return ctx
}

// OnError handles the error timing for both models and tools.
func (h *Handler) OnError(ctx context.Context, info *callbacks.RunInfo, err error) context.Context {
	switch componentOf(info) {
	case components.ComponentOfChatModel:
		h.publish(ctx, TypeStepFailed, stepFailedData(err))
	case components.ComponentOfTool:
		callID, _ := ctx.Value(ctxKeyCallID).(string)
		h.publish(ctx, TypeToolFailed, ToolFailed{CallID: callID, Error: ErrorData{Type: "unknown", Message: errString(err)}})
	}
	return ctx
}

// OnStartWithStreamInput handles streamed tool-call arguments. It MUST close the
// copied reader to avoid leaking the pipeline.
func (h *Handler) OnStartWithStreamInput(ctx context.Context, info *callbacks.RunInfo, input *schema.StreamReader[callbacks.CallbackInput]) context.Context {
	if componentOf(info) != components.ComponentOfTool {
		input.Close()
		return ctx
	}
	callID := h.newID("call")
	name := toolName(info)
	h.publish(ctx, TypeToolInputStarted, ToolInputStarted{CallID: callID, Name: name})

	go func() {
		defer input.Close()
		var buf strings.Builder
		for {
			chunk, err := input.Recv()
			if err == io.EOF {
				break
			}
			if err != nil {
				break
			}
			ti := tool.ConvCallbackInput(chunk)
			if ti == nil || ti.ArgumentsInJSON == "" {
				continue
			}
			buf.WriteString(ti.ArgumentsInJSON)
			h.publish(ctx, TypeToolInputDelta, ToolInputDelta{CallID: callID, Delta: ti.ArgumentsInJSON})
		}
		h.publish(ctx, TypeToolInputEnded, ToolInputEnded{CallID: callID, Text: buf.String()})
		h.publish(ctx, TypeToolCalled, ToolCalled{CallID: callID, Tool: name, Input: buf.String()})
	}()

	return context.WithValue(ctx, ctxKeyCallID, callID)
}

// OnEndWithStreamOutput handles streamed model output. It MUST close the copied
// reader to avoid leaking the pipeline.
func (h *Handler) OnEndWithStreamOutput(ctx context.Context, info *callbacks.RunInfo, output *schema.StreamReader[callbacks.CallbackOutput]) context.Context {
	if componentOf(info) != components.ComponentOfChatModel {
		output.Close()
		return ctx
	}

	go func() {
		defer output.Close()
		var (
			textBuf, reasonBuf strings.Builder
			usage              *model.TokenUsage
			finish             string
			reasoningStarted   bool
			rid                string
		)
		for {
			chunk, err := output.Recv()
			if err == io.EOF {
				break
			}
			if err != nil {
				// Close any open started blocks so consumers tracking a
				// started -> ended state machine don't see dangling events.
				if reasoningStarted {
					h.publish(ctx, TypeReasoningEnded, ReasoningEnded{ReasoningID: rid, Text: reasonBuf.String()})
				}
				h.publish(ctx, TypeTextEnded, TextEnded{Text: textBuf.String()})
				h.publish(ctx, TypeStepFailed, stepFailedData(err))
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
			if mo.Message.ResponseMeta != nil {
				if mo.Message.ResponseMeta.FinishReason != "" {
					finish = mo.Message.ResponseMeta.FinishReason
				}
				if usage == nil && mo.Message.ResponseMeta.Usage != nil {
					usage = convUsage(mo.Message.ResponseMeta.Usage)
				}
			}
			if rc := mo.Message.ReasoningContent; rc != "" {
				if !reasoningStarted {
					reasoningStarted = true
					rid = h.newID("reasoning")
					h.publish(ctx, TypeReasoningStarted, ReasoningStarted{ReasoningID: rid})
				}
				reasonBuf.WriteString(rc)
				h.publish(ctx, TypeReasoningDelta, ReasoningDelta{ReasoningID: rid, Delta: rc})
			}
			if c := mo.Message.Content; c != "" {
				textBuf.WriteString(c)
				h.publish(ctx, TypeTextDelta, TextDelta{Delta: c})
			}
		}
		if reasoningStarted {
			h.publish(ctx, TypeReasoningEnded, ReasoningEnded{ReasoningID: rid, Text: reasonBuf.String()})
		}
		h.publish(ctx, TypeTextEnded, TextEnded{Text: textBuf.String()})
		h.publish(ctx, TypeStepEnded, stepEnded(finish, usage))
	}()

	return ctx
}

// stepEnded builds a StepEnded payload from a finish reason and token usage.
func stepEnded(finish string, usage *model.TokenUsage) StepEnded {
	se := StepEnded{Finish: finish}
	if usage != nil {
		se.Tokens = Tokens{
			Input:     usage.PromptTokens,
			Output:    usage.CompletionTokens,
			Reasoning: usage.CompletionTokensDetails.ReasoningTokens,
			// Cache.Write is kept for Kilocode wire parity but stays 0: eino's
			// TokenUsage exposes only a cache-read (CachedTokens) count.
			Cache: CacheTokens{Read: usage.PromptTokenDetails.CachedTokens},
		}
	}
	return se
}

// convUsage maps a schema.TokenUsage to a model.TokenUsage shape.
func convUsage(u *schema.TokenUsage) *model.TokenUsage {
	if u == nil {
		return nil
	}
	return &model.TokenUsage{
		PromptTokens:            u.PromptTokens,
		PromptTokenDetails:      model.PromptTokenDetails{CachedTokens: u.PromptTokenDetails.CachedTokens},
		CompletionTokens:        u.CompletionTokens,
		TotalTokens:             u.TotalTokens,
		CompletionTokensDetails: model.CompletionTokensDetails{ReasoningTokens: u.CompletionTokensDetails.ReasoningTokens},
	}
}

func finishReason(mo *model.CallbackOutput) string {
	if mo != nil && mo.Message != nil && mo.Message.ResponseMeta != nil {
		return mo.Message.ResponseMeta.FinishReason
	}
	return ""
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
