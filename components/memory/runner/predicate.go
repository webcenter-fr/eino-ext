package runner

import "github.com/cloudwego/eino/schema"

// MessagePredicate decides whether an agent event (identified by its emitting
// agent name and message role) should be streamed to the client and persisted to
// the conversation history.
//
// When a Config.Predicate is nil, the bridge defaults to "assistant-only": every
// message whose role is schema.Assistant is streamed and persisted (see
// runner.go).
type MessagePredicate func(agentName string, role schema.RoleType) bool

// AgentRole matches events emitted by a specific agent with a specific role. It
// covers the common "supervisor + assistant" case where only the top-level
// agent's assistant output should reach the client and the history.
func AgentRole(agentName string, role schema.RoleType) MessagePredicate {
	return func(name string, r schema.RoleType) bool {
		return name == agentName && r == role
	}
}

// Role matches events with the given role, regardless of the emitting agent.
func Role(role schema.RoleType) MessagePredicate {
	return func(_ string, r schema.RoleType) bool {
		return r == role
	}
}

// And returns a predicate that is true only when all preds are true. With no
// arguments it always returns true.
func And(preds ...MessagePredicate) MessagePredicate {
	return func(name string, r schema.RoleType) bool {
		for _, p := range preds {
			if !p(name, r) {
				return false
			}
		}
		return true
	}
}

// Or returns a predicate that is true when any of preds is true. With no
// arguments it always returns false.
func Or(preds ...MessagePredicate) MessagePredicate {
	return func(name string, r schema.RoleType) bool {
		for _, p := range preds {
			if p(name, r) {
				return true
			}
		}
		return false
	}
}

// Not negates p.
func Not(p MessagePredicate) MessagePredicate {
	return func(name string, r schema.RoleType) bool {
		return !p(name, r)
	}
}
