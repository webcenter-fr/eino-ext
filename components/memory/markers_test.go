package memory

import (
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestMarkIncompleteAndIsIncomplete(t *testing.T) {
	if IsIncomplete(nil) {
		t.Fatal("nil message must not be incomplete")
	}

	msg := schema.AssistantMessage("hi", nil)
	if IsIncomplete(msg) {
		t.Fatal("fresh message must not be incomplete")
	}

	MarkIncomplete(msg)
	if !IsIncomplete(msg) {
		t.Fatal("message must be incomplete after MarkIncomplete")
	}

	// Must create Extra when nil.
	bare := &schema.Message{Role: schema.Assistant}
	MarkIncomplete(bare)
	if !IsIncomplete(bare) {
		t.Fatal("MarkIncomplete must create Extra and mark message")
	}
}

func TestMarkIncomplete_NilSafe(t *testing.T) {
	MarkIncomplete(nil) // must not panic
}

func TestEphemeralMessage(t *testing.T) {
	if IsEphemeral(nil) {
		t.Fatal("nil message must not be ephemeral")
	}

	msg := NewEphemeralMessage(schema.Assistant, "transient")
	if msg.Role != schema.Assistant || msg.Content != "transient" {
		t.Fatalf("unexpected ephemeral message: %#v", msg)
	}
	if !IsEphemeral(msg) {
		t.Fatal("message must be ephemeral")
	}

	if IsEphemeral(schema.AssistantMessage("normal", nil)) {
		t.Fatal("normal message must not be ephemeral")
	}
}
