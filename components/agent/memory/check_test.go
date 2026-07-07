package memory

import (
	"context"
	"testing"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/checkup"
)

func TestCheckNilStoreAndModel(t *testing.T) {
	results := Check(context.Background(), nil, nil)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	for _, r := range results {
		if r.Status != checkup.StatusError {
			t.Errorf("expected status error, got %s for %s", r.Status, r.Component)
		}
	}
}

func TestCheckNilStore(t *testing.T) {
	results := Check(context.Background(), nil, nil)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
}
