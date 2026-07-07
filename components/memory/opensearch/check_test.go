package opensearch

import (
	"context"
	"testing"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/checkup"
)

func TestCheckNoURLs(t *testing.T) {
	ctx := context.Background()
	results := Check(ctx, Config{URLs: nil})
	if len(results) == 0 {
		t.Fatal("expected non-empty results")
	}
	for _, r := range results {
		if r.Status != checkup.StatusError {
			t.Errorf("expected status error for nil URLs, got %s for %s", r.Status, r.Component)
		}
	}
}

func TestCheckEmptyConfig(t *testing.T) {
	ctx := context.Background()
	results := Check(ctx, Config{})
	if len(results) == 0 {
		t.Fatal("expected non-empty results")
	}
	for _, r := range results {
		if r.Status != checkup.StatusError {
			t.Errorf("expected status error for empty config, got %s for %s", r.Status, r.Component)
		}
	}
}
