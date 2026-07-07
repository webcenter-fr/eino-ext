package prometheus

import (
	"context"
	"testing"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/checkup"
)

func TestCheckEmptyConfigs(t *testing.T) {
	ctx := context.Background()
	results := Check(ctx, Configs{})
	if len(results) != 1 {
		t.Fatalf("expected 1 result for empty configs, got %d", len(results))
	}
	if results[0].Status != checkup.StatusError {
		t.Errorf("expected status error, got %s", results[0].Status)
	}
	if results[0].Component != "prometheus" {
		t.Errorf("expected component prometheus, got %s", results[0].Component)
	}
}

func TestCheckNilConfigs(t *testing.T) {
	ctx := context.Background()
	results := Check(ctx, nil)
	if len(results) != 1 {
		t.Fatalf("expected 1 result for nil configs, got %d", len(results))
	}
	if results[0].Status != checkup.StatusError {
		t.Errorf("expected status error, got %s", results[0].Status)
	}
}

func TestCheckInvalidInstance(t *testing.T) {
	ctx := context.Background()
	results := Check(ctx, Configs{
		"bad": Config{Address: ""},
	})
	for _, r := range results {
		if r.Status != checkup.StatusError {
			t.Errorf("expected all error results for invalid instance, got %s for %s", r.Status, r.Component)
		}
		if r.Instance != "bad" {
			t.Errorf("expected instance 'bad', got %s", r.Instance)
		}
	}
	if len(results) == 0 {
		t.Fatal("expected non-empty results")
	}
	if len(results) != 4 {
		t.Errorf("expected 4 results (one per tool), got %d", len(results))
	}
}

func TestCheckResultStatuses(t *testing.T) {
	ctx := context.Background()
	results := Check(ctx, Configs{
		"bad": Config{Address: ""},
	})
	for _, r := range results {
		switch r.Status {
		case checkup.StatusOK, checkup.StatusError, checkup.StatusLimited:
		default:
			t.Errorf("unexpected status %q for %s", r.Status, r.Component)
		}
	}
}

func TestCheckClientErrorResults(t *testing.T) {
	r := clientErrorResults("test-instance", context.DeadlineExceeded)
	if len(r) != 4 {
		t.Fatalf("expected 4 results, got %d", len(r))
	}
	for i, rr := range r {
		if rr.Instance != "test-instance" {
			t.Errorf("result %d: expected instance 'test-instance', got %q", i, rr.Instance)
		}
		if rr.Status != checkup.StatusError {
			t.Errorf("result %d: expected status error, got %q", i, rr.Status)
		}
	}
}
