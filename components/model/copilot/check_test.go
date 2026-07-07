package copilot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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
		json.NewEncoder(w).Encode(copilotModelsResponse{
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
		json.NewEncoder(w).Encode(copilotModelsResponse{Data: []copilotModelData{}})
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
		json.NewEncoder(w).Encode(copilotModelsResponse{
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
