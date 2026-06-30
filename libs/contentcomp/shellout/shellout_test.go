package shellout

import (
	"context"
	"strings"
	"testing"

	"github.com/webcenter-fr/eino-ext/libs/contentcomp"
)

func TestCollapseRepeatedLines(t *testing.T) {
	in := "start\n" + strings.Repeat("same line\n", 10) + "end"
	out, _, err := Compress(context.Background(), in, WithMinGain(1))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(out, "same line") != 1 {
		t.Fatalf("expected collapsed run, got:\n%s", out)
	}
	if !strings.Contains(out, "identical lines collapsed") {
		t.Fatalf("expected collapse marker, got:\n%s", out)
	}
}

func TestCollapseBlankRuns(t *testing.T) {
	in := "a\n\n\n\n\nb"
	out, _, err := Compress(context.Background(), in, WithMinGain(1))
	if err != nil {
		t.Fatal(err)
	}
	if out != "a\n\nb" {
		t.Fatalf("got %q", out)
	}
}

func TestDropProgressBars(t *testing.T) {
	in := "Downloading foo 12%\nDownloading foo 55%\nDownloading foo 100%\ndone installing package"
	out, _, err := Compress(context.Background(), in, WithMinGain(1))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "Downloading") {
		t.Fatalf("progress lines should be dropped, got:\n%s", out)
	}
	if !strings.Contains(out, "done installing") {
		t.Fatalf("real line dropped, got:\n%s", out)
	}
}

func TestStripCarriageProgress(t *testing.T) {
	in := "building\rbuilding 10%\rbuilding 100%\nok"
	out, _, err := Compress(context.Background(), in, WithMinGain(1))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "building 10%") {
		t.Fatalf("intermediate frame kept: %q", out)
	}
}

func TestUnchangedPassthrough(t *testing.T) {
	in := "this is normal output\nwith two lines"
	out, ref, err := Compress(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if out != in || ref != nil {
		t.Fatalf("expected unchanged passthrough, got %q ref=%v", out, ref)
	}
}

func TestDataPercentNotDropped(t *testing.T) {
	in := "coverage: 87% of statements\nother: 12% done value"
	out, _, err := Compress(context.Background(), in, WithMinGain(1))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "coverage: 87%") {
		t.Fatalf("legitimate data line dropped: %q", out)
	}
}

func TestDeterministicAndReversible(t *testing.T) {
	in := strings.Repeat("noise\n", 20) + "\n\n\n\nresult"
	store := contentcomp.NewMemoryStore()
	out1, ref, err := Compress(context.Background(), in, WithStore(store))
	if err != nil {
		t.Fatal(err)
	}
	out2, _, _ := Compress(context.Background(), in, WithStore(store))
	if out1 != out2 {
		t.Fatalf("non-deterministic: %q vs %q", out1, out2)
	}
	if ref == nil {
		t.Fatal("expected ref for reversibility")
	}
	original, err := store.Get(context.Background(), *ref)
	if err != nil {
		t.Fatal(err)
	}
	if original != in {
		t.Fatal("stored original mismatch")
	}
}

func TestIdempotent(t *testing.T) {
	in := strings.Repeat("dup\n", 8) + "tail"
	out1, _, _ := Compress(context.Background(), in, WithMinGain(1))
	out2, _, _ := Compress(context.Background(), out1, WithMinGain(1))
	if out1 != out2 {
		t.Fatalf("not idempotent:\n%q\n%q", out1, out2)
	}
}

func TestWithPatternsCustomTable(t *testing.T) {
	upper := Pattern{Name: "upper", Apply: strings.ToUpper}
	in := "hello world that is long enough to beat the gain threshold here ok"
	out, _, err := Compress(context.Background(), in, WithPatterns(upper), WithMinGain(0))
	if err != nil {
		t.Fatal(err)
	}
	if out != strings.ToUpper(in) {
		t.Fatalf("custom pattern not applied: %q", out)
	}
}

func TestDefaultPatternsCopy(t *testing.T) {
	a := DefaultPatterns()
	b := DefaultPatterns()
	if len(a) == 0 || len(a) != len(b) {
		t.Fatalf("unexpected default pattern table length: %d", len(a))
	}
	// Mutating the returned slice must not affect the package table.
	a[0] = Pattern{Name: "mutated"}
	if DefaultPatterns()[0].Name == "mutated" {
		t.Fatal("DefaultPatterns must return a copy")
	}
}
