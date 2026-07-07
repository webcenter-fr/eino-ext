package memory

import "github.com/cloudwego/eino/schema"

// HasBoolMarker returns true if msg carries a boolean Extra marker set to true
// under the given key. It is nil-safe for both the message and its Extra map.
func HasBoolMarker(msg *schema.Message, key string) bool {
	if msg == nil || msg.Extra == nil {
		return false
	}
	b, ok := msg.Extra[key].(bool)
	return ok && b
}

// SetBoolMarker sets a boolean Extra marker to true under the given key,
// allocating the Extra map when needed. It is a no-op for a nil message.
func SetBoolMarker(msg *schema.Message, key string) {
	if msg == nil {
		return
	}
	if msg.Extra == nil {
		msg.Extra = map[string]any{}
	}
	msg.Extra[key] = true
}

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
	SetBoolMarker(msg, IncompleteMarkerKey)
}

// IsIncomplete returns true if msg is marked as incomplete.
func IsIncomplete(msg *schema.Message) bool {
	return HasBoolMarker(msg, IncompleteMarkerKey)
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
	return HasBoolMarker(msg, EphemeralMarkerKey)
}
