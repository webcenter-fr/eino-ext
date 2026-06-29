package contextopt

import (
	"context"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// ChatModel is a model.BaseChatModel decorator that optimizes the input message
// history before delegating to the wrapped model.
//
// ChatModel only implements model.BaseChatModel. When the wrapped model also
// implements model.ToolCallingChatModel, NewChatModel returns a *ToolCallingChatModel
// (which embeds *ChatModel and adds WithTools); use NewToolCallingChatModel for a
// statically-typed tool-calling decorator. This avoids advertising tool support on
// models that do not provide it.
type ChatModel struct {
	base model.BaseChatModel
	opt  *Optimizer
}

// ToolCallingChatModel decorates a model.ToolCallingChatModel, preserving
// optimization across WithTools.
type ToolCallingChatModel struct {
	*ChatModel
	base model.ToolCallingChatModel
}

var (
	_ model.BaseChatModel        = (*ChatModel)(nil)
	_ model.ToolCallingChatModel = (*ToolCallingChatModel)(nil)
)

// NewChatModel wraps base with context optimization driven by cfg. If base
// implements model.ToolCallingChatModel, the returned value is a
// *ToolCallingChatModel (still a valid model.BaseChatModel).
func NewChatModel(base model.BaseChatModel, cfg *Config) (model.BaseChatModel, error) {
	if base == nil {
		return nil, errors.New("contextopt: base model must not be nil")
	}
	opt, err := NewOptimizer(cfg)
	if err != nil {
		return nil, err
	}
	return newChatModelFromOptimizer(base, opt), nil
}

// NewToolCallingChatModel wraps a tool-calling base model with context optimization.
func NewToolCallingChatModel(base model.ToolCallingChatModel, cfg *Config) (*ToolCallingChatModel, error) {
	if base == nil {
		return nil, errors.New("contextopt: base model must not be nil")
	}
	opt, err := NewOptimizer(cfg)
	if err != nil {
		return nil, err
	}
	return &ToolCallingChatModel{ChatModel: &ChatModel{base: base, opt: opt}, base: base}, nil
}

// NewChatModelFromOptimizer wraps base with an existing Optimizer.
func NewChatModelFromOptimizer(base model.BaseChatModel, opt *Optimizer) (model.BaseChatModel, error) {
	if base == nil {
		return nil, errors.New("contextopt: base model must not be nil")
	}
	if opt == nil {
		return nil, errors.New("contextopt: optimizer must not be nil")
	}
	return newChatModelFromOptimizer(base, opt), nil
}

func newChatModelFromOptimizer(base model.BaseChatModel, opt *Optimizer) model.BaseChatModel {
	cm := &ChatModel{base: base, opt: opt}
	if tc, ok := base.(model.ToolCallingChatModel); ok {
		return &ToolCallingChatModel{ChatModel: cm, base: tc}
	}
	return cm
}

// Generate optimizes input then delegates to the wrapped model.
func (c *ChatModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	msgs, err := c.opt.Optimize(ctx, input)
	if err != nil {
		return nil, err
	}
	return c.base.Generate(ctx, msgs, opts...)
}

// Stream optimizes input then delegates to the wrapped model.
func (c *ChatModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msgs, err := c.opt.Optimize(ctx, input)
	if err != nil {
		return nil, err
	}
	return c.base.Stream(ctx, msgs, opts...)
}

// WithTools binds tools to the wrapped tool-calling model and returns a new
// ToolCallingChatModel preserving the optimization behavior.
func (c *ToolCallingChatModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	bound, err := c.base.WithTools(tools)
	if err != nil {
		return nil, err
	}
	return &ToolCallingChatModel{
		ChatModel: &ChatModel{base: bound, opt: c.opt},
		base:      bound,
	}, nil
}
