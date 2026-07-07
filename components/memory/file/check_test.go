package file

import (
	"context"
	"os"
	"testing"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/checkup"
)

func TestCheckDirAccessible(t *testing.T) {
	tmp := t.TempDir()
	cfg := FileMemoryConfig{Dir: tmp, MaxWindowSize: 10}
	results := Check(context.Background(), cfg)
	if len(results) < 5 {
		t.Fatalf("expected at least 5 results, got %d", len(results))
	}
	for _, r := range results {
		switch r.Status {
		case checkup.StatusOK, checkup.StatusError, checkup.StatusLimited:
		default:
			t.Errorf("unexpected status %q for %s", r.Status, r.Component)
		}
	}
}

func TestCheckDirNotWritable(t *testing.T) {
	tmp := t.TempDir()
	roDir := tmp + "/readonly"
	if err := os.Mkdir(roDir, 0o555); err != nil {
		t.Fatalf("failed to create readonly dir: %v", err)
	}
	defer os.Chmod(roDir, 0o755)

	cfg := FileMemoryConfig{Dir: roDir + "/sub", MaxWindowSize: 10}
	results := Check(context.Background(), cfg)
	for _, r := range results {
		if r.Status != checkup.StatusError {
			t.Errorf("expected all error results for non-writable dir, got %s for %s", r.Status, r.Component)
		}
	}
}
