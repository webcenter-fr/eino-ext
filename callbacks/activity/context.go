package activity

import "context"

// sessionKeyType is an unexported context key type to avoid collisions.
type sessionKeyType struct{}

var sessionKey = sessionKeyType{}

// WithSession returns a copy of ctx carrying the activity session id. Callers set
// it at run start so the Handler can correlate concurrent graph runs to distinct
// fan-out buckets without cross-talk.
func WithSession(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, sessionKey, id)
}

// SessionFromContext reads the session id set by WithSession. The boolean is
// false when no session was set; an empty string is still a valid bucket the
// Handler publishes to.
func SessionFromContext(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(sessionKey).(string)
	return id, ok
}

// agentKeyType is an unexported context key type to avoid collisions.
type agentKeyType struct{}

var agentKey = agentKeyType{}

// WithAgent returns a copy of ctx carrying the active agent name. In a
// multi-agent (supervisor + sub-agents) run, callers scope each agent's
// execution with WithAgent so the Handler can attribute every emitted event to
// the agent that produced it. The agentattr adk middleware
// (components/middleware/agentattr) wires this up automatically for adk
// ChatModelAgents.
func WithAgent(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, agentKey, name)
}

// AgentFromContext reads the agent name set by WithAgent. The boolean is false
// when no agent was set (single-agent runs), in which case events carry no
// agent attribution.
func AgentFromContext(ctx context.Context) (string, bool) {
	name, ok := ctx.Value(agentKey).(string)
	return name, ok
}
