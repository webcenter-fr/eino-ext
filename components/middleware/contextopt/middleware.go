package contextopt

import (
	"context"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/adk"
)

// Middleware is an adk.ChatModelAgentMiddleware that optimizes the conversation
// history before each model invocation. It embeds *adk.BaseChatModelAgentMiddleware
// to inherit no-op implementations for every hook except BeforeModelRewriteState.
type Middleware struct {
	*adk.BaseChatModelAgentMiddleware
	opt *Optimizer
}

var _ adk.ChatModelAgentMiddleware = (*Middleware)(nil)

// NewMiddleware builds a Middleware from the given Config.
func NewMiddleware(cfg *Config) (*Middleware, error) {
	opt, err := NewOptimizer(cfg)
	if err != nil {
		return nil, err
	}
	return &Middleware{
		BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{},
		opt:                          opt,
	}, nil
}

// NewMiddlewareFromOptimizer wraps an existing Optimizer as a Middleware.
func NewMiddlewareFromOptimizer(opt *Optimizer) (*Middleware, error) {
	if opt == nil {
		return nil, errors.New("contextopt: optimizer must not be nil")
	}
	return &Middleware{
		BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{},
		opt:                          opt,
	}, nil
}

// BeforeModelRewriteState rewrites state.Messages with the optimized history.
func (m *Middleware) BeforeModelRewriteState(ctx context.Context, state *adk.ChatModelAgentState, mc *adk.ModelContext) (context.Context, *adk.ChatModelAgentState, error) {
	if state == nil {
		return ctx, state, nil
	}
	msgs, err := m.opt.Optimize(ctx, state.Messages)
	if err != nil {
		return ctx, state, err
	}
	state.Messages = msgs
	return ctx, state, nil
}
