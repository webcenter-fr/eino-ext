package memory

import "github.com/cloudwego/eino/schema"

// IncompleteMarkerKey is the key used in schema.Message.Extra to mark an
// assistant message as incomplete (the generation was interrupted, e.g. by an
// iterator error or a truncated stream). It lets downstream consumers know the
// persisted answer may be partial.
const IncompleteMarkerKey = "__eino_ext_memory_incomplete"

// EphemeralMarkerKey is the key used in schema.Message.Extra to mark a message
// as ephemeral: it is streamed to the client but MUST NOT be persisted to the
// conversation history (e.g. a transient error notice).
const EphemeralMarkerKey = "__eino_ext_memory_ephemeral"

// MarkIncomplete marks msg as incomplete. It creates the Extra map when nil.
func MarkIncomplete(msg *schema.Message) {
	if msg == nil {
		return
	}
	if msg.Extra == nil {
		msg.Extra = map[string]any{}
	}
	msg.Extra[IncompleteMarkerKey] = true
}

// IsIncomplete returns true if msg is marked as incomplete.
func IsIncomplete(msg *schema.Message) bool {
	if msg == nil || msg.Extra == nil {
		return false
	}
	b, ok := msg.Extra[IncompleteMarkerKey].(bool)
	return ok && b
}

// NewEphemeralMessage creates a message marked as ephemeral: it is intended to
// be streamed to the client but never persisted to the conversation history.
func NewEphemeralMessage(role schema.RoleType, content string) *schema.Message {
	return &schema.Message{
		Role:    role,
		Content: content,
		Extra: map[string]any{
			EphemeralMarkerKey: true,
		},
	}
}

// IsEphemeral returns true if msg is marked as ephemeral.
func IsEphemeral(msg *schema.Message) bool {
	if msg == nil || msg.Extra == nil {
		return false
	}
	b, ok := msg.Extra[EphemeralMarkerKey].(bool)
	return ok && b
}
