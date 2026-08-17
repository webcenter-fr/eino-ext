package alertmanager

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

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
	if results[0].Component != "alertmanager" {
		t.Errorf("expected component alertmanager, got %s", results[0].Component)
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
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	expected := map[string]bool{
		"alertmanager_instance_list": false,
		"alertmanager_alert":         false,
		"alertmanager_alert_write":   false,
	}
	for _, r := range results {
		if r.Instance != "bad" {
			t.Errorf("expected instance 'bad', got %s", r.Instance)
		}
		if r.Status != checkup.StatusError {
			t.Errorf("expected error result for %s, got %s", r.Component, r.Status)
		}
		if _, ok := expected[r.Component]; !ok {
			t.Errorf("unexpected component %s", r.Component)
		}
		expected[r.Component] = true
	}
	for name, seen := range expected {
		if !seen {
			t.Errorf("missing result for %s", name)
		}
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

func TestAllComponentNames(t *testing.T) {
	names := allComponentNames()
	if len(names) != 3 {
		t.Fatalf("expected 3 names, got %d", len(names))
	}
	seen := make(map[string]bool)
	for _, name := range names {
		if seen[name] {
			t.Errorf("duplicate name: %s", name)
		}
		seen[name] = true
	}
}

func TestProbeInstanceWriteToolStatus(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()

	c, err := NewClient(ctx, Config{Address: server.URL})
	require.NoError(t, err)

	results := probeInstance(ctx, c, "t")
	var write checkup.Result
	for _, r := range results {
		if r.Component == alertWriteToolName {
			write = r
		}
	}
	if write.Component == "" {
		t.Fatal("expected a result for alertmanager_alert_write")
	}
	if write.Status != checkup.StatusOK {
		t.Errorf("expected StatusOK, got %q", write.Status)
	}
	if write.Message != "guidance tool, no external call required" {
		t.Errorf("unexpected message %q", write.Message)
	}
}
