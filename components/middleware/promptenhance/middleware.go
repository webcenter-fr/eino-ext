package promptenhance

import (
	"context"
	"maps"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"github.com/webcenter-fr/eino-ext/components/memory"
	libspromptenhance "github.com/webcenter-fr/eino-ext/libs/promptenhance"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
)

const enhancedMarkerKey = "__eino_ext_promptenhance_enhanced"

// Middleware enhances the last user message before each model call.
// On first invocation, it rewrites the draft and — when AutoAccept is false —
// returns an InterruptError so the consumer can present the result to the user
// for approval. The consumer re-runs the agent with the user's Choice in
// context (via WithChoice) to apply the decision.
type Middleware struct {
	*adk.BaseChatModelAgentMiddleware
	enhancer      *libspromptenhance.Enhancer
	autoAccept    bool
	shouldEnhance ShouldEnhanceFunc
}

var _ adk.ChatModelAgentMiddleware = (*Middleware)(nil)

// NewMiddleware validates cfg and returns a Middleware.
func NewMiddleware(cfg *Config) (*Middleware, error) {
	if cfg == nil {
		cfg = &Config{}
	}
	c := *cfg
	if err := validate.Struct(&c); err != nil {
		return nil, err
	}
	return &Middleware{
		BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{},
		enhancer:                     c.Enhancer,
		autoAccept:                   c.AutoAccept,
		shouldEnhance:                c.ShouldEnhance,
	}, nil
}

// BeforeModelRewriteState is called before each model invocation. It enhances
// the last user message and may return an InterruptError for human approval.
func (m *Middleware) BeforeModelRewriteState(
	ctx context.Context,
	state *adk.ChatModelAgentState,
	mc *adk.ModelContext,
) (context.Context, *adk.ChatModelAgentState, error) {
	if state == nil || len(state.Messages) == 0 {
		return ctx, state, nil
	}

	idx := findLastUserMessageIndex(state.Messages)
	if idx < 0 {
		return ctx, state, nil
	}

	lastUser := state.Messages[idx]
	if isEnhanced(lastUser) {
		return ctx, state, nil
	}

	history := state.Messages[:idx]

	choice := getChoiceFromCtx(ctx)
	if choice != nil {
		return m.applyResume(ctx, state, choice, idx, history)
	}

	if m.shouldEnhance != nil && !m.shouldEnhance(ctx) {
		markSkipped(state, idx)
		return ctx, state, nil
	}

	enhanced, err := m.enhancer.EnhanceInContext(ctx, history, lastUser.Content)
	if err != nil {
		return ctx, state, errors.Wrap(err, "promptenhance: enhancement failed")
	}

	if enhanced == "" || enhanced == lastUser.Content {
		markSkipped(state, idx)
		return ctx, state, nil
	}

	if m.autoAccept {
		applyEnhanced(state, idx, enhanced)
		return ctx, state, nil
	}

	// Interrupt path: DO NOT mutate state.Messages (preserves the invariant
	// asserted by TestMiddleware_BeforeModelRewriteState_FirstCall).
	return ctx, state, &InterruptError{
		InterruptInfo: InterruptInfo{
			Original: lastUser.Content,
			Enhanced: enhanced,
		},
	}
}

func (m *Middleware) applyResume(
	ctx context.Context,
	state *adk.ChatModelAgentState,
	choice *Choice,
	idx int,
	history []*schema.Message,
) (context.Context, *adk.ChatModelAgentState, error) {
	switch choice.Action {
	case "original", "skip_always":
		markSkipped(state, idx)
	case "enhanced":
		enhanced, err := m.enhancer.EnhanceInContext(ctx, history, state.Messages[idx].Content)
		if err != nil {
			return ctx, state, errors.Wrap(err, "promptenhance: re-enhancement failed")
		}
		if enhanced == "" {
			markSkipped(state, idx)
			return ctx, state, nil
		}
		applyEnhanced(state, idx, enhanced)
	case "modified":
		if choice.Text == "" {
			return ctx, state, errors.New("promptenhance: modified action requires text")
		}
		applyEnhanced(state, idx, choice.Text)
	default:
		return ctx, state, errors.Errorf("promptenhance: unknown action %q", choice.Action)
	}

	return ctx, state, nil
}

// cloneMessage returns a shallow copy of m with a freshly-allocated Extra map,
// so that marker mutations applied to the clone never leak back to the
// caller-owned original. Other reference fields (ToolCalls, MultiContent,
// ReasoningContent, ResponseMeta) are shallow-copied: user messages have nil
// ToolCalls and none of these fields are mutated by this middleware, so a deep
// copy of them is unnecessary.
func cloneMessage(m *schema.Message) *schema.Message {
	if m == nil {
		return nil
	}
	c := *m
	c.Extra = maps.Clone(m.Extra)
	return &c
}

// replaceMessage swaps state.Messages[idx] for a clone marked enhanced and
// returns the clone, leaving the caller's original pointer untouched. It is a
// no-op (returning nil) when the target message is nil.
func replaceMessage(state *adk.ChatModelAgentState, idx int) *schema.Message {
	c := cloneMessage(state.Messages[idx])
	if c == nil {
		return nil
	}
	markEnhanced(c)
	state.Messages[idx] = c
	return c
}

// applyEnhanced replaces state.Messages[idx] with a clone carrying content.
func applyEnhanced(state *adk.ChatModelAgentState, idx int, content string) {
	if c := replaceMessage(state, idx); c != nil {
		c.Content = content
	}
}

// markSkipped replaces state.Messages[idx] with a marked clone whose content is
// unchanged, so the original is untouched while the skip stays idempotent.
func markSkipped(state *adk.ChatModelAgentState, idx int) {
	replaceMessage(state, idx)
}

func findLastUserMessageIndex(msgs []*schema.Message) int {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i] != nil && msgs[i].Role == schema.User {
			return i
		}
	}
	return -1
}

func isEnhanced(msg *schema.Message) bool {
	return memory.HasBoolMarker(msg, enhancedMarkerKey)
}

func markEnhanced(msg *schema.Message) {
	memory.SetBoolMarker(msg, enhancedMarkerKey)
}
