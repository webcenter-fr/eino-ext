package opensearch

import (
	"context"
	"testing"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/checkup"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/osclient"
)

func TestCheckNilConfig(t *testing.T) {
	ctx := context.Background()
	results := Check(ctx, nil)
	if len(results) != 2 {
		t.Fatalf("expected 2 results for nil config, got %d", len(results))
	}
	for _, r := range results {
		if r.Status != checkup.StatusError {
			t.Errorf("expected status error for nil config, got %s for %s", r.Status, r.Component)
		}
	}
}

func TestCheckEmptyURLs(t *testing.T) {
	ctx := context.Background()
	results := Check(ctx, &osclient.Config{URLs: nil})
	if len(results) != 2 {
		t.Fatalf("expected 2 results for empty URLs, got %d", len(results))
	}
	for _, r := range results {
		if r.Status != checkup.StatusError {
			t.Errorf("expected status error for empty URLs, got %s for %s", r.Status, r.Component)
		}
	}
}
