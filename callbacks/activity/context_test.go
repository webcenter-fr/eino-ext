package activity

import (
	"context"
	"testing"
)

func TestWithAgentMetaSetsBothKeys(t *testing.T) {
	ctx := WithAgentMeta(context.Background(), AgentMeta{Name: "x", Model: "m", Description: "d"})

	name, ok := AgentFromContext(ctx)
	if !ok {
		t.Fatal("AgentFromContext: expected ok=true")
	}
	if name != "x" {
		t.Fatalf("AgentFromContext: got %q, want %q", name, "x")
	}

	meta, ok := AgentMetaFromContext(ctx)
	if !ok {
		t.Fatal("AgentMetaFromContext: expected ok=true")
	}
	if meta.Name != "x" {
		t.Fatalf("AgentMetaFromContext.Name: got %q, want %q", meta.Name, "x")
	}
	if meta.Model != "m" {
		t.Fatalf("AgentMetaFromContext.Model: got %q, want %q", meta.Model, "m")
	}
	if meta.Description != "d" {
		t.Fatalf("AgentMetaFromContext.Description: got %q, want %q", meta.Description, "d")
	}
}

func TestWithAgentMetaEmptyNameIsNoOp(t *testing.T) {
	ctx := WithAgentMeta(context.Background(), AgentMeta{})

	if _, ok := AgentFromContext(ctx); ok {
		t.Fatal("AgentFromContext: expected ok=false for no-op")
	}
	if _, ok := AgentMetaFromContext(ctx); ok {
		t.Fatal("AgentMetaFromContext: expected ok=false for no-op")
	}
}

func TestAgentMetaFromContextAbsent(t *testing.T) {
	meta, ok := AgentMetaFromContext(context.Background())
	if ok {
		t.Fatal("AgentMetaFromContext: expected ok=false")
	}
	if meta != (AgentMeta{}) {
		t.Fatalf("AgentMetaFromContext: expected zero value, got %+v", meta)
	}
}

func TestWithAgentStillWorks(t *testing.T) {
	ctx := WithAgent(context.Background(), "x")

	name, ok := AgentFromContext(ctx)
	if !ok {
		t.Fatal("AgentFromContext: expected ok=true")
	}
	if name != "x" {
		t.Fatalf("AgentFromContext: got %q, want %q", name, "x")
	}

	if _, ok := AgentMetaFromContext(ctx); ok {
		t.Fatal("AgentMetaFromContext: expected ok=false for legacy WithAgent caller")
	}
}

func TestWithAgentMetaThenWithAgentOverridesName(t *testing.T) {
	ctx := WithAgentMeta(context.Background(), AgentMeta{Name: "x", Model: "m", Description: "d"})
	ctx = WithAgent(ctx, "y")

	name, ok := AgentFromContext(ctx)
	if !ok {
		t.Fatal("AgentFromContext: expected ok=true")
	}
	if name != "y" {
		t.Fatalf("AgentFromContext: got %q, want %q", name, "y")
	}

	meta, ok := AgentMetaFromContext(ctx)
	if !ok {
		t.Fatal("AgentMetaFromContext: expected ok=true")
	}
	if meta.Name != "x" {
		t.Fatalf("AgentMetaFromContext.Name: got %q, want original %q", meta.Name, "x")
	}
	if meta.Model != "m" {
		t.Fatalf("AgentMetaFromContext.Model: got %q, want %q", meta.Model, "m")
	}
}
