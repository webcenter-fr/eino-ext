package contextopt

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"

	"github.com/webcenter-fr/eino-ext/components/contentcomp"
	"github.com/webcenter-fr/eino-ext/components/contentcomp/jsoncrush"
)

func TestReversiblePrune(t *testing.T) {
	store := contentcomp.NewMemoryStore()
	o, err := NewOptimizer(&Config{
		PruneToolOutputs:   true,
		PruneProtectTokens: 10,
		PruneMinimum:       10,
		Backend:            store,
	})
	if err != nil {
		t.Fatal(err)
	}

	big := strings.Repeat("x", 4000)
	msgs := []*schema.Message{
		schema.UserMessage("q1"),
		toolMsg("t1", big),
		schema.UserMessage("q2"),
		schema.UserMessage("q3"),
	}

	out, err := o.pruneToolOutputs(context.Background(), msgs)
	if err != nil {
		t.Fatal(err)
	}

	if out[1].Content == big {
		t.Fatal("expected tool output to be pruned")
	}
	if !isPruned(out[1]) {
		t.Fatal("expected prune marker")
	}
	// Original must be recoverable via the Backend.
	restored, err := o.RestorePruned(context.Background(), out[1])
	if err != nil {
		t.Fatal(err)
	}
	if restored != big {
		t.Fatalf("original not recoverable, got %d bytes", len(restored))
	}

	// Idempotent: a second pass leaves the already-pruned message untouched.
	out2, err := o.pruneToolOutputs(context.Background(), out)
	if err != nil {
		t.Fatal(err)
	}
	if out2[1].Content != out[1].Content {
		t.Fatal("prune not idempotent")
	}
}

func TestContentCompressorBeforeTruncation(t *testing.T) {
	o, err := NewOptimizer(&Config{
		ContentCompressors: []contentcomp.Compressor{jsoncrush.NewCompressor()},
	})
	if err != nil {
		t.Fatal(err)
	}

	raw := `[{"type":"f","ok":true,"n":"a"},{"type":"f","ok":true,"n":"b"},{"type":"f","ok":true,"n":"c"}]`
	msgs := []*schema.Message{
		schema.UserMessage("q"),
		toolMsg("t1", raw),
	}

	out, err := o.applyContentCompressors(context.Background(), msgs)
	if err != nil {
		t.Fatal(err)
	}
	if out[1].Content == raw {
		t.Fatalf("expected jsoncrush to compress tool output")
	}
	if !jsoncrush.IsCrushed(out[1].Content) {
		t.Fatalf("expected crushed content, got %s", out[1].Content)
	}
	// Input message must not be mutated.
	if msgs[1].Content != raw {
		t.Fatal("input message mutated")
	}

	// Idempotent.
	out2, err := o.applyContentCompressors(context.Background(), out)
	if err != nil {
		t.Fatal(err)
	}
	if out2[1].Content != out[1].Content {
		t.Fatal("content compression not idempotent")
	}
}

func TestLegacyTruncationWithoutBackend(t *testing.T) {
	o, err := NewOptimizer(&Config{
		PruneToolOutputs:   true,
		PruneProtectTokens: 10,
		PruneMinimum:       10,
		ToolOutputMaxChars: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	big := strings.Repeat("y", 4000)
	msgs := []*schema.Message{
		schema.UserMessage("q1"),
		toolMsg("t1", big),
		schema.UserMessage("q2"),
		schema.UserMessage("q3"),
	}
	out, err := o.pruneToolOutputs(context.Background(), msgs)
	if err != nil {
		t.Fatal(err)
	}
	if len(out[1].Content) > 100 {
		t.Fatalf("expected destructive truncation, got %d bytes", len(out[1].Content))
	}
	if _, ok := out[1].Extra[PruneRefKey]; ok {
		t.Fatal("no ref expected without Backend")
	}
}
