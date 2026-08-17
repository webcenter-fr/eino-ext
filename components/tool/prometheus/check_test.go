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
	if len(results) == 0 {
		t.Fatal("expected non-empty results")
	}
	if len(results) != 5 {
		t.Fatalf("expected 5 results (3 prometheus tools + 2 alertmanager limited), got %d", len(results))
	}

	for _, r := range results {
		if r.Instance != "bad" {
			t.Errorf("expected instance 'bad', got %s", r.Instance)
		}
		switch r.Component {
		case "prometheus_alert", "prometheus_alert_write":
			if r.Status != checkup.StatusLimited {
				t.Errorf("expected limited alertmanager result for %s, got %s", r.Component, r.Status)
			}
		default:
			if r.Status != checkup.StatusError {
				t.Errorf("expected error result for %s, got %s", r.Component, r.Status)
			}
		}
	}
}

func TestAlertmanagerClientErrorResults(t *testing.T) {
	r := alertmanagerClientErrorResults("test-instance", context.DeadlineExceeded)
	if len(r) != 2 {
		t.Fatalf("expected 2 results, got %d", len(r))
	}
	components := map[string]bool{}
	for _, rr := range r {
		if rr.Instance != "test-instance" {
			t.Errorf("expected instance 'test-instance', got %q", rr.Instance)
		}
		if rr.Status != checkup.StatusError {
			t.Errorf("expected status error, got %q", rr.Status)
		}
		components[rr.Component] = true
	}
	if !components["prometheus_alert"] {
		t.Error("missing prometheus_alert result")
	}
	if !components["prometheus_alert_write"] {
		t.Error("missing prometheus_alert_write result")
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
	if len(r) != 3 {
		t.Fatalf("expected 3 results, got %d", len(r))
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
