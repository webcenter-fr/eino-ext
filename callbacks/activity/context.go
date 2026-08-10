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

// AgentMeta carries the display metadata for the active agent: its canonical
// name, the model that powers it, and a short human-readable description of
// what the agent does. It is set on the run context by WithAgentMeta so the
// Handler can enrich agent.switched events (and any other payload that wants
// the metadata) without callers having to thread it through each event.
type AgentMeta struct {
	Name        string
	Model       string
	Description string
}

// agentMetaKeyType is an unexported context key type to avoid collisions.
type agentMetaKeyType struct{}

var agentMetaKey = agentMetaKeyType{}

// WithAgentMeta returns a copy of ctx carrying the active agent's full
// metadata (name, model, description). It ALSO sets the legacy agentKey to
// meta.Name so existing AgentFromContext callers and the Handler's publish
// path keep working unchanged. meta.Name must be non-empty; if it is empty the
// function is a no-op (returns ctx unchanged) to preserve the "empty agent =
// unattributed" invariant the Handler relies on.
func WithAgentMeta(ctx context.Context, meta AgentMeta) context.Context {
	if meta.Name == "" {
		return ctx
	}
	ctx = context.WithValue(ctx, agentMetaKey, meta)
	return context.WithValue(ctx, agentKey, meta.Name)
}

// AgentMetaFromContext reads the agent metadata set by WithAgentMeta. The
// boolean is false when no metadata was set (name-only WithAgent callers, or
// single-agent runs); in that case the returned AgentMeta is the zero value.
// Callers that only need the name should prefer the cheaper AgentFromContext.
func AgentMetaFromContext(ctx context.Context) (AgentMeta, bool) {
	meta, ok := ctx.Value(agentMetaKey).(AgentMeta)
	return meta, ok
}
