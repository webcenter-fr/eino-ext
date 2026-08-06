package s3

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
	if results[0].Component != "s3" {
		t.Errorf("expected component s3, got %s", results[0].Component)
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

func TestCheckClientErrorResults(t *testing.T) {
	r := clientErrorResults("test-instance", context.DeadlineExceeded)
	if len(r) != 5 {
		t.Fatalf("expected 5 results, got %d", len(r))
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

func TestCheckResultStatuses(t *testing.T) {
	r := clientErrorResults("test", context.DeadlineExceeded)
	for _, rr := range r {
		switch rr.Status {
		case checkup.StatusOK, checkup.StatusError, checkup.StatusLimited:
		default:
			t.Errorf("unexpected status %q for %s", rr.Status, rr.Component)
		}
	}
}
