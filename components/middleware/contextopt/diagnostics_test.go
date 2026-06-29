package contextopt

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestVolatileCheckNonMutating(t *testing.T) {
	var findings []VolatileFinding
	o, err := NewOptimizer(&Config{
		VolatileCheck:    true,
		VolatileObserver: func(_ context.Context, f VolatileFinding) { findings = append(findings, f) },
	})
	if err != nil {
		t.Fatal(err)
	}
	sys := schema.SystemMessage(`You are at 2024-01-02T03:04:05Z with run 550e8400-e29b-41d4-a716-446655440000 and {"session_id": "x"}`)
	original := sys.Content
	msgs := []*schema.Message{sys, schema.UserMessage("hi")}

	out, err := o.Optimize(context.Background(), msgs)
	if err != nil {
		t.Fatal(err)
	}
	// Byte-equal: no mutation.
	if out[0].Content != original {
		t.Fatalf("VolatileCheck mutated content:\n%s", out[0].Content)
	}
	kinds := map[VolatileKind]bool{}
	for _, f := range findings {
		kinds[f.Kind] = true
	}
	for _, k := range []VolatileKind{VolatileTimestamp, VolatileUUID, VolatileIDField} {
		if !kinds[k] {
			t.Fatalf("expected finding of kind %s, got %v", k, findings)
		}
	}
}

func TestVerbositySteerAppendsAtEnd(t *testing.T) {
	o, err := NewOptimizer(&Config{VerbositySteer: "Be concise."})
	if err != nil {
		t.Fatal(err)
	}
	sys := schema.SystemMessage("You are a helpful assistant.")
	msgs := []*schema.Message{sys, schema.UserMessage("hi")}

	out, err := o.Optimize(context.Background(), msgs)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out[0].Content, "You are a helpful assistant.") {
		t.Fatalf("prefix changed: %q", out[0].Content)
	}
	if !strings.HasSuffix(out[0].Content, "Be concise.") {
		t.Fatalf("steer not appended at end: %q", out[0].Content)
	}
	// Input not mutated.
	if sys.Content != "You are a helpful assistant." {
		t.Fatal("input system message mutated")
	}
	// Idempotent.
	out2, err := o.Optimize(context.Background(), out)
	if err != nil {
		t.Fatal(err)
	}
	if out2[0].Content != out[0].Content {
		t.Fatalf("steer not idempotent: %q", out2[0].Content)
	}
}

func TestVerbositySteerDisabledByDefault(t *testing.T) {
	o, err := NewOptimizer(&Config{})
	if err != nil {
		t.Fatal(err)
	}
	sys := schema.SystemMessage("system")
	out, err := o.Optimize(context.Background(), []*schema.Message{sys, schema.UserMessage("hi")})
	if err != nil {
		t.Fatal(err)
	}
	if out[0].Content != "system" {
		t.Fatalf("expected no steer by default, got %q", out[0].Content)
	}
}
