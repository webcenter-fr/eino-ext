package jsoncrush

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/webcenter-fr/eino-ext/libs/contentcomp"
)

func canon(t *testing.T, s string) string {
	t.Helper()
	c, err := canonical([]byte(s))
	if err != nil {
		t.Fatalf("canonical(%q): %v", s, err)
	}
	return c
}

func TestCrushRoundTripLossless(t *testing.T) {
	input := `[
		{"type":"file","status":"ok","name":"a.go","lines":10},
		{"type":"file","status":"ok","name":"b.go","lines":20},
		{"type":"file","status":"ok","name":"c.go","lines":30}
	]`

	out, refs, err := Crush(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 0 {
		t.Fatalf("expected no refs in lossless mode, got %d", len(refs))
	}
	if !strings.Contains(out, "_defaults") {
		t.Fatalf("expected defaults block, got %s", out)
	}
	if len(out) >= len(input) {
		t.Logf("note: crushed not smaller (%d >= %d)", len(out), len(input))
	}

	expanded, err := Expand(out)
	if err != nil {
		t.Fatal(err)
	}
	if expanded != canon(t, input) {
		t.Fatalf("round-trip mismatch:\n got: %s\nwant: %s", expanded, canon(t, input))
	}
}

func TestCrushDeterministic(t *testing.T) {
	input := `[{"a":1,"b":2},{"a":1,"b":3}]`
	out1, _, _ := Crush(context.Background(), input)
	out2, _, _ := Crush(context.Background(), input)
	if out1 != out2 {
		t.Fatalf("non-deterministic output:\n%s\n%s", out1, out2)
	}
	// Idempotent: crushing the crushed form is a no-op via the compressor guard.
	c := NewCompressor()
	_, changed, err := c.Compress(context.Background(), out1)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("expected idempotent compress on crushed content")
	}
}

func TestNonArrayUnchanged(t *testing.T) {
	for _, in := range []string{
		`{"a":1}`,
		`"just a string"`,
		`not json at all`,
		`[1,2,3]`,
		`[{"a":1},"mixed"]`,
		`[]`,
	} {
		out, refs, err := Crush(context.Background(), in)
		if err != nil {
			t.Fatalf("Crush(%q): %v", in, err)
		}
		if out != in || refs != nil {
			t.Fatalf("expected %q unchanged, got %q", in, out)
		}
	}
}

func TestLossyStageReversible(t *testing.T) {
	input := `[
		{"id":"550e8400-e29b-41d4-a716-446655440000","kind":"row","v":1},
		{"id":"550e8400-e29b-41d4-a716-446655440001","kind":"row","v":2},
		{"id":"550e8400-e29b-41d4-a716-446655440002","kind":"row","v":3}
	]`
	store := contentcomp.NewMemoryStore()
	out, refs, err := Crush(context.Background(), input, WithStore(store))
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) == 0 {
		t.Fatal("expected at least one lossy ref")
	}
	if strings.Contains(out, "550e8400") {
		t.Fatalf("lossy column should be offloaded, still present: %s", out)
	}

	// Plain Expand must refuse (needs store).
	if _, err := Expand(out); err == nil {
		t.Fatal("expected Expand to fail without store on lossy payload")
	}

	expanded, err := ExpandWithStore(context.Background(), out, store)
	if err != nil {
		t.Fatal(err)
	}
	if expanded != canon(t, input) {
		t.Fatalf("lossy round-trip mismatch:\n got: %s\nwant: %s", expanded, canon(t, input))
	}
}

func TestExpandNonCrushedUnchanged(t *testing.T) {
	in := `{"hello":"world"}`
	out, err := Expand(in)
	if err != nil {
		t.Fatal(err)
	}
	if out != in {
		t.Fatalf("expected unchanged, got %s", out)
	}
}

// Sanity: defaults block actually round-trips with json number fidelity.
func TestNumberFidelity(t *testing.T) {
	input := `[{"big":123456789012345678,"k":"a"},{"big":123456789012345678,"k":"b"}]`
	out, _, err := Crush(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	expanded, err := Expand(out)
	if err != nil {
		t.Fatal(err)
	}
	var got []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(expanded), &got); err != nil {
		t.Fatal(err)
	}
	if string(got[0]["big"]) != "123456789012345678" {
		t.Fatalf("number fidelity lost: %s", string(got[0]["big"]))
	}
}
