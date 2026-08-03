package kubernetes

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
	if results[0].Component != "kubernetes" {
		t.Errorf("expected component kubernetes, got %s", results[0].Component)
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

func TestCheckNilConfigInCluster(t *testing.T) {
	ctx := context.Background()
	results := Check(ctx, Configs{
		"bad": nil,
	})
	if len(results) == 0 {
		t.Fatal("expected non-empty results")
	}
	for _, r := range results {
		if r.Status != checkup.StatusError {
			t.Errorf("expected all error results, got %s for %s", r.Status, r.Component)
		}
		if r.Instance != "bad" {
			t.Errorf("expected instance 'bad', got %s", r.Instance)
		}
	}
}

func TestCheckResultStatuses(t *testing.T) {
	ctx := context.Background()
	results := Check(ctx, Configs{
		"bad": nil,
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
	r := clientErrorResults("test-cluster", context.DeadlineExceeded)
	if len(r) != 8 {
		t.Fatalf("expected 8 results, got %d", len(r))
	}
	for i, rr := range r {
		if rr.Instance != "test-cluster" {
			t.Errorf("result %d: expected instance 'test-cluster', got %q", i, rr.Instance)
		}
		if rr.Status != checkup.StatusError {
			t.Errorf("result %d: expected status error, got %q", i, rr.Status)
		}
	}
}

func TestAllComponentNames(t *testing.T) {
	names := allComponentNames()
	if len(names) != 8 {
		t.Errorf("expected 8 component names, got %d", len(names))
	}
	seen := make(map[string]bool)
	for _, name := range names {
		if seen[name] {
			t.Errorf("duplicate component name: %s", name)
		}
		seen[name] = true
	}
}
