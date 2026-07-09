package promptenhance

import (
	"context"

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

	lastUser := findLastUserMessage(state.Messages)
	if lastUser == nil {
		return ctx, state, nil
	}

	if isEnhanced(lastUser) {
		return ctx, state, nil
	}

	choice := getChoiceFromCtx(ctx)
	if choice != nil {
		return m.applyResume(ctx, state, choice, lastUser)
	}

	if m.shouldEnhance != nil && !m.shouldEnhance(ctx) {
		markEnhanced(lastUser)
		return ctx, state, nil
	}

	enhanced, err := m.enhancer.Enhance(ctx, lastUser.Content)
	if err != nil {
		return ctx, state, errors.Wrap(err, "promptenhance: enhancement failed")
	}

	if enhanced == lastUser.Content {
		markEnhanced(lastUser)
		return ctx, state, nil
	}

	if m.autoAccept {
		lastUser.Content = enhanced
		markEnhanced(lastUser)
		return ctx, state, nil
	}

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
	lastUser *schema.Message,
) (context.Context, *adk.ChatModelAgentState, error) {
	switch choice.Action {
	case "original", "skip_always":
	case "enhanced":
		enhanced, err := m.enhancer.Enhance(ctx, lastUser.Content)
		if err != nil {
			return ctx, state, errors.Wrap(err, "promptenhance: re-enhancement failed")
		}
		lastUser.Content = enhanced
	case "modified":
		if choice.Text == "" {
			return ctx, state, errors.New("promptenhance: modified action requires text")
		}
		lastUser.Content = choice.Text
	default:
		return ctx, state, errors.Errorf("promptenhance: unknown action %q", choice.Action)
	}

	markEnhanced(lastUser)
	return ctx, state, nil
}

func findLastUserMessage(msgs []*schema.Message) *schema.Message {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i] != nil && msgs[i].Role == schema.User {
			return msgs[i]
		}
	}
	return nil
}

func isEnhanced(msg *schema.Message) bool {
	return memory.HasBoolMarker(msg, enhancedMarkerKey)
}

func markEnhanced(msg *schema.Message) {
	memory.SetBoolMarker(msg, enhancedMarkerKey)
}
