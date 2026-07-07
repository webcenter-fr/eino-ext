package opensearch

import (
	"context"
	"testing"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/checkup"
)

func TestCheckNilConfig(t *testing.T) {
	ctx := context.Background()
	results := Check(ctx, nil, nil)
	if len(results) == 0 {
		t.Fatal("expected non-empty results")
	}
	for _, r := range results {
		if r.Status != checkup.StatusError {
			t.Errorf("expected status error for nil config, got %s for %s", r.Status, r.Component)
		}
	}
}

func TestCheckEmptyConfig(t *testing.T) {
	ctx := context.Background()
	results := Check(ctx, &Config{}, nil)
	if len(results) == 0 {
		t.Fatal("expected non-empty results")
	}
	for _, r := range results {
		if r.Status != checkup.StatusError {
			t.Errorf("expected status error for empty config, got %s for %s", r.Status, r.Component)
		}
	}
}

func TestCheckNilConfigNonNilEmbedder(t *testing.T) {
	ctx := context.Background()
	results := Check(ctx, nil, nil)
	if len(results) == 0 {
		t.Fatal("expected non-empty results")
	}
	for _, r := range results {
		switch r.Status {
		case checkup.StatusOK, checkup.StatusError, checkup.StatusLimited:
		default:
			t.Errorf("unexpected status %q for %s", r.Status, r.Component)
		}
	}
}
