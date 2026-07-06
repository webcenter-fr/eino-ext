// Package agentattr provides an adk.ChatModelAgentMiddleware that attributes the
// callbacks/activity event stream to a named agent.
//
// In a multi-agent run (supervisor + sub-agents) the activity Handler emits
// text.*, tool.* and step.* events that, by default, identify only the model.
// Registering this middleware on an agent's Handlers tags every activity event
// produced during that agent's run with the agent's name, so a UI can route a
// supervisor's final answer separately from a sub-agent's intermediate chatter.
//
// It threads activity.WithAgent(ctx, name) onto the context that reaches the
// component callbacks for both the model and tool calls of the agent, without
// introducing an adk event bridge. The single global callbacks.Handler stays in
// place.
package agentattr

import (
	"context"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"

	"github.com/webcenter-fr/eino-ext/callbacks/activity"
)

// Config configures the agent-attribution Middleware.
type Config struct {
	// AgentName is the name tagged onto every activity event produced during the
	// agent's run (envelope Agent field, merged "agent" key in the SSE data, and
	// the agent.switched transition event). Required.
	AgentName string `validate:"required" jsonschema:"description=Name tagged onto activity events produced during this agent's run"`
}

// Middleware is an adk.ChatModelAgentMiddleware that tags the activity event
// stream with an agent name. It embeds *adk.BaseChatModelAgentMiddleware to
// inherit no-op implementations for the hooks it does not override.
type Middleware struct {
	*adk.BaseChatModelAgentMiddleware
	name string
}

var _ adk.ChatModelAgentMiddleware = (*Middleware)(nil)

// New builds a Middleware from the given Config.
//
//	agent, _ := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
//	    Name:  "supervisor",
//	    Model: m,
//	    Handlers: []adk.ChatModelAgentMiddleware{
//	        must(agentattr.New(&agentattr.Config{AgentName: "supervisor"})),
//	    },
//	})
func New(cfg *Config) (*Middleware, error) {
	if cfg == nil {
		cfg = &Config{}
	}
	c := *cfg
	if err := validate.Struct(&c); err != nil {
		return nil, errors.Wrap(err, "invalid agentattr.Config")
	}
	return &Middleware{
		BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{},
		name:                         c.AgentName,
	}, nil
}

// BeforeAgent sets the agent name on the context propagated through the agent's
// run.
func (m *Middleware) BeforeAgent(ctx context.Context, runCtx *adk.ChatModelAgentContext) (context.Context, *adk.ChatModelAgentContext, error) {
	return activity.WithAgent(ctx, m.name), runCtx, nil
}

// BeforeModelRewriteState sets the agent name on the context propagated to the
// model invocation, so the ChatModel callbacks attribute their events.
func (m *Middleware) BeforeModelRewriteState(ctx context.Context, state *adk.ChatModelAgentState, _ *adk.ModelContext) (context.Context, *adk.ChatModelAgentState, error) {
	return activity.WithAgent(ctx, m.name), state, nil
}

// WrapInvokableToolCall sets the agent name on the context passed to the tool
// endpoint, so the Tool callbacks attribute their events.
func (m *Middleware) WrapInvokableToolCall(_ context.Context, endpoint adk.InvokableToolCallEndpoint, _ *adk.ToolContext) (adk.InvokableToolCallEndpoint, error) {
	return func(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
		return endpoint(activity.WithAgent(ctx, m.name), argumentsInJSON, opts...)
	}, nil
}

// WrapStreamableToolCall mirrors WrapInvokableToolCall for streaming tools.
func (m *Middleware) WrapStreamableToolCall(_ context.Context, endpoint adk.StreamableToolCallEndpoint, _ *adk.ToolContext) (adk.StreamableToolCallEndpoint, error) {
	return func(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (*schema.StreamReader[string], error) {
		return endpoint(activity.WithAgent(ctx, m.name), argumentsInJSON, opts...)
	}, nil
}

// WrapEnhancedInvokableToolCall mirrors WrapInvokableToolCall for enhanced tools.
func (m *Middleware) WrapEnhancedInvokableToolCall(_ context.Context, endpoint adk.EnhancedInvokableToolCallEndpoint, _ *adk.ToolContext) (adk.EnhancedInvokableToolCallEndpoint, error) {
	return func(ctx context.Context, arg *schema.ToolArgument, opts ...tool.Option) (*schema.ToolResult, error) {
		return endpoint(activity.WithAgent(ctx, m.name), arg, opts...)
	}, nil
}

// WrapEnhancedStreamableToolCall mirrors WrapInvokableToolCall for enhanced
// streaming tools.
func (m *Middleware) WrapEnhancedStreamableToolCall(_ context.Context, endpoint adk.EnhancedStreamableToolCallEndpoint, _ *adk.ToolContext) (adk.EnhancedStreamableToolCallEndpoint, error) {
	return func(ctx context.Context, arg *schema.ToolArgument, opts ...tool.Option) (*schema.StreamReader[*schema.ToolResult], error) {
		return endpoint(activity.WithAgent(ctx, m.name), arg, opts...)
	}, nil
}

// WrapModel is a no-op pass-through; attribution is handled via context, not by
// wrapping the model.
func (m *Middleware) WrapModel(_ context.Context, cm model.BaseChatModel, _ *adk.ModelContext) (model.BaseChatModel, error) {
	return cm, nil
}
