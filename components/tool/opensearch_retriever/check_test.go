package opensearch_retriever

import (
	"context"
	"testing"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/checkup"
)

func TestCheckEmptyConfigs(t *testing.T) {
	ctx := context.Background()
	results := Check(ctx, nil)
	if len(results) != 1 {
		t.Fatalf("expected 1 result for nil configs, got %d", len(results))
	}
	for _, r := range results {
		if r.Status != checkup.StatusError {
			t.Errorf("expected status error for nil configs, got %s for %s", r.Status, r.Component)
		}
	}
}

func TestCheckEmptySlice(t *testing.T) {
	ctx := context.Background()
	results := Check(ctx, []Config{})
	if len(results) != 1 {
		t.Fatalf("expected 1 result for empty configs, got %d", len(results))
	}
	for _, r := range results {
		if r.Status != checkup.StatusError {
			t.Errorf("expected status error for empty configs, got %s for %s", r.Status, r.Component)
		}
	}
}

func TestCheckInvalidConfig(t *testing.T) {
	ctx := context.Background()
	cfg := Config{
		URLs:        []string{},
		Index:       "test",
		ToolName:    "test_tool",
		Description: "test",
	}
	results := Check(ctx, []Config{cfg})
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if r.Status != checkup.StatusError {
		t.Errorf("expected status error for invalid config, got %s", r.Status)
	}
}
