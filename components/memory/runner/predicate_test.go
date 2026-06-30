package runner

import (
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestAgentRole(t *testing.T) {
	p := AgentRole("supervisor", schema.Assistant)
	if !p("supervisor", schema.Assistant) {
		t.Fatal("expected match for supervisor assistant")
	}
	if p("supervisor", schema.Tool) {
		t.Fatal("role mismatch must not match")
	}
	if p("worker", schema.Assistant) {
		t.Fatal("agent mismatch must not match")
	}
}

func TestRole(t *testing.T) {
	p := Role(schema.Assistant)
	if !p("anything", schema.Assistant) {
		t.Fatal("expected role match regardless of agent")
	}
	if p("anything", schema.Tool) {
		t.Fatal("unexpected role match")
	}
}

func TestAndOrNot(t *testing.T) {
	isSup := AgentRole("supervisor", schema.Assistant)
	isAssistant := Role(schema.Assistant)

	if !And(isSup, isAssistant)("supervisor", schema.Assistant) {
		t.Fatal("And should be true when both true")
	}
	if And(isSup, isAssistant)("worker", schema.Assistant) {
		t.Fatal("And should be false when one false")
	}

	if !Or(isSup, isAssistant)("worker", schema.Assistant) {
		t.Fatal("Or should be true when one true")
	}
	if Or(isSup, Role(schema.Tool))("worker", schema.User) {
		t.Fatal("Or should be false when all false")
	}

	if Not(isAssistant)("x", schema.Assistant) {
		t.Fatal("Not should invert true")
	}
	if !Not(isAssistant)("x", schema.Tool) {
		t.Fatal("Not should invert false")
	}

	// Empty variadics.
	if !And()("x", schema.Assistant) {
		t.Fatal("And() with no preds should be true")
	}
	if Or()("x", schema.Assistant) {
		t.Fatal("Or() with no preds should be false")
	}
}
