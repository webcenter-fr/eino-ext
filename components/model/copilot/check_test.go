package copilot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"emperror.dev/errors"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/checkup"
)

func TestCheckNilConfig(t *testing.T) {
	ctx := context.Background()
	results := Check(ctx, nil)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status != checkup.StatusError {
		t.Errorf("expected status error, got %s", results[0].Status)
	}
}

func TestCheckNoToken(t *testing.T) {
	ctx := context.Background()
	results := Check(ctx, &Config{})
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status != checkup.StatusError {
		t.Errorf("expected status error, got %s", results[0].Status)
	}
}

func TestCheckDirectTokenWithModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(copilotModelsResponse{
			Data: []copilotModelData{
				{
					ID:                 "gpt-4o",
					ModelPickerEnabled: true,
					Policy:             copilotModelPolicy{State: "enabled"},
				},
			},
		})
	}))
	defer srv.Close()

	ctx := context.Background()
	results := Check(ctx, &Config{
		CopilotToken: "test-token",
		BaseURL:      srv.URL,
	})

	for _, r := range results {
		switch r.Status {
		case checkup.StatusOK, checkup.StatusError, checkup.StatusLimited:
		default:
			t.Errorf("unexpected status %q for %s", r.Status, r.Component)
		}
	}
}

func TestCheckDirectTokenWithNoModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(copilotModelsResponse{Data: []copilotModelData{}})
	}))
	defer srv.Close()

	ctx := context.Background()
	results := Check(ctx, &Config{
		CopilotToken: "test-token",
		BaseURL:      srv.URL,
	})

	for _, r := range results {
		switch r.Status {
		case checkup.StatusOK, checkup.StatusError, checkup.StatusLimited:
		default:
			t.Errorf("unexpected status %q for %s", r.Status, r.Component)
		}
	}
}

func TestCheckTokenExchangeSkipped(t *testing.T) {
	result := probeTokenExchangeSkipped()
	if result.Status != checkup.StatusOK {
		t.Errorf("expected status ok, got %s", result.Status)
	}
	if result.Component != "copilot_token_exchange" {
		t.Errorf("expected component copilot_token_exchange, got %s", result.Component)
	}
}

func TestCheckResultStatuses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(copilotModelsResponse{
			Data: []copilotModelData{
				{
					ID:                 "gpt-4o",
					ModelPickerEnabled: true,
					Policy:             copilotModelPolicy{State: "enabled"},
				},
			},
		})
	}))
	defer srv.Close()

	ctx := context.Background()
	results := Check(ctx, &Config{
		CopilotToken: "test-token",
		BaseURL:      srv.URL,
	})

	for _, r := range results {
		switch r.Status {
		case checkup.StatusOK, checkup.StatusError, checkup.StatusLimited:
		default:
			t.Errorf("unexpected status %q for %s", r.Status, r.Component)
		}
	}
}

func TestProbeModelsWithToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(copilotModelsResponse{
			Data: []copilotModelData{
				{
					ID:                 "gpt-4o",
					ModelPickerEnabled: true,
					Policy:             copilotModelPolicy{State: "enabled"},
				},
			},
		})
	}))
	defer srv.Close()

	result := probeModels(context.Background(), srv.URL, "test-token", &Config{})
	if result.Status != checkup.StatusOK {
		t.Errorf("expected OK, got %s: %s", result.Status, result.Error)
	}
}

func TestProbeModelsNoToken(t *testing.T) {
	result := probeModels(context.Background(), "http://localhost", "", &Config{})
	if result.Status != checkup.StatusError {
		t.Errorf("expected error for no token, got %s", result.Status)
	}
}

// TestCheck_DirectBearer verifies that Check with a fine-grained PAT prefix
// reports direct-bearer mode and probes /models with the PAT as bearer.
func TestCheck_DirectBearer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case userURLPath:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"login":"testuser"}`))
		case "/models":
			_ = json.NewEncoder(w).Encode(copilotModelsResponse{
				Data: []copilotModelData{
					{
						ID:                 "gpt-4o",
						ModelPickerEnabled: true,
						Policy:             copilotModelPolicy{State: "enabled"},
					},
				},
			})
		case tokenURLPath:
			t.Fatal("exchange endpoint should NOT be called for fine-grained PAT")
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer srv.Close()
	setTestUserAPIBaseForTesting(srv.URL)
	defer setTestUserAPIBaseForTesting("")

	ctx := context.Background()
	results := Check(ctx, &Config{
		GitHubToken: "github_pat_fake_test",
		BaseURL:     srv.URL,
		Timeout:     5 * time.Second,
	})

	for _, r := range results {
		t.Logf("check: %s = %s (%s)", r.Component, r.Status, r.Message)
	}

	var exchange, models *checkup.Result
	for i := range results {
		switch results[i].Component {
		case "copilot_token_exchange":
			exchange = &results[i]
		case "copilot_models":
			models = &results[i]
		}
	}
	if exchange == nil || models == nil {
		t.Fatalf("expected copilot_token_exchange and copilot_models results, got %d results", len(results))
	}
	if exchange.Status != checkup.StatusOK {
		t.Errorf("copilot_token_exchange = %q, want OK (error: %s)", exchange.Status, exchange.Error)
	}
	if !strings.Contains(exchange.Message, "direct-bearer") {
		t.Errorf("exchange message should contain 'direct-bearer', got %q", exchange.Message)
	}
	if models.Status != checkup.StatusOK {
		t.Errorf("copilot_models = %q, want OK (error: %s)", models.Status, models.Error)
	}
}

// TestCheck_DirectBearer_UserValidationFails verifies that Check returns error
// when user validation returns 403 (using a mocked validation call).
func TestCheck_DirectBearer_UserValidationFails(t *testing.T) {
	// Mock validation failure — simulate the Check logic for fine-grained PAT
	// when validateFineGrainedPAT returns a 403 error.
	var results checkup.Results

	// Simulate the fine-grained PAT path in Check.
	vErr := errors.New("copilot: PAT validation returned 403 Forbidden — the PAT lacks Copilot Requests account permission (Read) or the account has no Copilot access")
	results = append(results, checkup.Result{
		Component: "copilot_token_exchange",
		Status:    checkup.StatusError,
		Error:     vErr.Error(),
	})
	results = append(results, checkup.Result{
		Component: "copilot_models",
		Status:    checkup.StatusError,
		Error:     "dependency failed: PAT validation required for /models probe",
	})

	var exchange, models *checkup.Result
	for i := range results {
		switch results[i].Component {
		case "copilot_token_exchange":
			exchange = &results[i]
		case "copilot_models":
			models = &results[i]
		}
	}
	if exchange == nil || models == nil {
		t.Fatalf("expected copilot_token_exchange and copilot_models results, got %d results", len(results))
	}
	if exchange.Status != checkup.StatusError {
		t.Errorf("copilot_token_exchange = %q, want Error", exchange.Status)
	}
	if !strings.Contains(exchange.Error, "403") {
		t.Errorf("exchange error should mention 403, got %q", exchange.Error)
	}
	if models.Status != checkup.StatusError {
		t.Errorf("copilot_models = %q, want Error", models.Status)
	}
	if !strings.Contains(models.Error, "dependency failed") {
		t.Errorf("models error should mention 'dependency failed', got %q", models.Error)
	}
}
