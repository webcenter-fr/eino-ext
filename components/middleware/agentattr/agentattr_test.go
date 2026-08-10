package agentattr

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"

	"github.com/webcenter-fr/eino-ext/callbacks/activity"
)

func TestNewRequiresAgentName(t *testing.T) {
	if _, err := New(&Config{}); err == nil {
		t.Fatal("expected error for empty AgentName")
	}
	if _, err := New(nil); err == nil {
		t.Fatal("expected error for nil config")
	}
	m, err := New(&Config{AgentName: "supervisor"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.name != "supervisor" {
		t.Fatalf("Middleware.name: got %q, want %q", m.name, "supervisor")
	}
	if m.model != "" {
		t.Fatalf("Middleware.model: got %q, want empty", m.model)
	}
	if m.description != "" {
		t.Fatalf("Middleware.description: got %q, want empty", m.description)
	}

	// With Model and Description.
	m, err = New(&Config{AgentName: "x", Model: "m", Description: "d"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.name != "x" {
		t.Fatalf("Middleware.name: got %q, want %q", m.name, "x")
	}
	if m.model != "m" {
		t.Fatalf("Middleware.model: got %q, want %q", m.model, "m")
	}
	if m.description != "d" {
		t.Fatalf("Middleware.description: got %q, want %q", m.description, "d")
	}
}

func TestNewRejectsOversizedModelAndDescription(t *testing.T) {
	tooLong := make([]rune, 257) // 256 is the cap, 257 must fail
	for i := range tooLong {
		tooLong[i] = 'x'
	}
	if _, err := New(&Config{AgentName: "x", Model: string(tooLong)}); err == nil {
		t.Fatal("expected error for Model over 256 runes")
	}
	if _, err := New(&Config{AgentName: "x", Description: string(tooLong)}); err == nil {
		t.Fatal("expected error for Description over 256 runes")
	}
	// 256 runes is the boundary and must succeed.
	atCap := make([]rune, 256)
	for i := range atCap {
		atCap[i] = 'x'
	}
	if _, err := New(&Config{AgentName: "x", Model: string(atCap), Description: string(atCap)}); err != nil {
		t.Fatalf("expected success at 256-rune cap, got: %v", err)
	}
}

func TestMiddlewareInjectsAgentOnContext(t *testing.T) {
	m, err := New(&Config{AgentName: "supervisor"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	assertAgent := func(ctx context.Context, where string) {
		if name, ok := activity.AgentFromContext(ctx); !ok || name != "supervisor" {
			t.Fatalf("%s: agent=%q ok=%v, want supervisor", where, name, ok)
		}
	}

	// BeforeAgent.
	ctx, _, err := m.BeforeAgent(context.Background(), &adk.ChatModelAgentContext{})
	if err != nil {
		t.Fatalf("BeforeAgent: %v", err)
	}
	assertAgent(ctx, "BeforeAgent")
	// For backward compat, AgentMetaFromContext should also work since we now use WithAgentMeta internally.
	if meta, ok := activity.AgentMetaFromContext(ctx); !ok || meta.Name != "supervisor" {
		t.Fatalf("BeforeAgent AgentMetaFromContext: meta=%+v ok=%v, want Name=supervisor", meta, ok)
	}

	// BeforeModelRewriteState.
	ctx, _, err = m.BeforeModelRewriteState(context.Background(), &adk.ChatModelAgentState{}, &adk.ModelContext{})
	if err != nil {
		t.Fatalf("BeforeModelRewriteState: %v", err)
	}
	assertAgent(ctx, "BeforeModelRewriteState")
	if meta, ok := activity.AgentMetaFromContext(ctx); !ok || meta.Name != "supervisor" {
		t.Fatalf("BeforeModelRewriteState AgentMetaFromContext: meta=%+v ok=%v, want Name=supervisor", meta, ok)
	}

	// WrapInvokableToolCall threads the agent onto the endpoint's context.
	endpoint := func(ctx context.Context, _ string, _ ...tool.Option) (string, error) {
		assertAgent(ctx, "WrapInvokableToolCall endpoint")
		if meta, ok := activity.AgentMetaFromContext(ctx); !ok || meta.Name != "supervisor" {
			t.Fatalf("WrapInvokableToolCall AgentMetaFromContext: meta=%+v ok=%v, want Name=supervisor", meta, ok)
		}
		return "ok", nil
	}
	wrapped, err := m.WrapInvokableToolCall(context.Background(), endpoint, &adk.ToolContext{})
	if err != nil {
		t.Fatalf("WrapInvokableToolCall: %v", err)
	}
	if _, err := wrapped(context.Background(), "{}"); err != nil {
		t.Fatalf("wrapped endpoint: %v", err)
	}
}

func TestMiddlewareInjectsAgentMetaOnContext(t *testing.T) {
	m, err := New(&Config{
		AgentName:   "supervisor",
		Model:       "claude-sonnet-5",
		Description: "orchestrates sub-agents",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	assertMeta := func(ctx context.Context, where string) {
		meta, ok := activity.AgentMetaFromContext(ctx)
		if !ok {
			t.Fatalf("%s: AgentMetaFromContext ok=false", where)
		}
		if meta.Name != "supervisor" {
			t.Fatalf("%s: meta.Name=%q, want supervisor", where, meta.Name)
		}
		if meta.Model != "claude-sonnet-5" {
			t.Fatalf("%s: meta.Model=%q, want claude-sonnet-5", where, meta.Model)
		}
		if meta.Description != "orchestrates sub-agents" {
			t.Fatalf("%s: meta.Description=%q, want orchestrates sub-agents", where, meta.Description)
		}
		// Backward compat: AgentFromContext still returns the name.
		name, ok := activity.AgentFromContext(ctx)
		if !ok || name != "supervisor" {
			t.Fatalf("%s: AgentFromContext=%q ok=%v, want supervisor", where, name, ok)
		}
	}

	// BeforeAgent.
	ctx, _, err := m.BeforeAgent(context.Background(), &adk.ChatModelAgentContext{})
	if err != nil {
		t.Fatalf("BeforeAgent: %v", err)
	}
	assertMeta(ctx, "BeforeAgent")

	// BeforeModelRewriteState.
	ctx, _, err = m.BeforeModelRewriteState(context.Background(), &adk.ChatModelAgentState{}, &adk.ModelContext{})
	if err != nil {
		t.Fatalf("BeforeModelRewriteState: %v", err)
	}
	assertMeta(ctx, "BeforeModelRewriteState")
}
