package copilot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestListModelsSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/models" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		json.NewEncoder(w).Encode(copilotModelsResponse{
			Data: []copilotModelData{
				{
					ID:                 "gpt-4o",
					Name:               "GPT-4o",
					ModelPickerEnabled: true,
					Policy:             copilotModelPolicy{State: "enabled"},
					Capabilities: copilotModelCapabilities{
						Limits: copilotModelLimits{
							MaxContextWindowTokens: 128000,
							MaxOutputTokens:        16384,
						},
						Supports: []copilotModelSupport{
							{Type: "tool_calls"},
							{Type: "streaming"},
							{Type: "vision"},
						},
					},
					MaxPromptImageSize: 2048,
				},
				{
					ID:                 "claude-3.5-sonnet",
					Name:               "Claude 3.5 Sonnet",
					ModelPickerEnabled: true,
					Policy:             copilotModelPolicy{State: "enabled"},
					Capabilities: copilotModelCapabilities{
						Limits: copilotModelLimits{
							MaxContextWindowTokens: 200000,
							MaxOutputTokens:        8192,
						},
						Supports: []copilotModelSupport{
							{Type: "tool_calls"},
							{Type: "streaming"},
							{Type: "reasoning"},
						},
					},
				},
			},
		})
	}))
	defer srv.Close()

	ctx := context.Background()
	models, err := ListModels(ctx, "test-token", srv.URL, 5*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(models))
	}

	m1 := models[0]
	if m1.ID != "gpt-4o" {
		t.Errorf("expected ID gpt-4o, got %q", m1.ID)
	}
	if !m1.SupportsToolCalls {
		t.Error("expected gpt-4o to support tool calls")
	}
	if !m1.SupportsStreaming {
		t.Error("expected gpt-4o to support streaming")
	}
	if !m1.SupportsVision {
		t.Error("expected gpt-4o to support vision")
	}
	if m1.SupportsReasoning {
		t.Error("expected gpt-4o NOT to support reasoning")
	}
	if m1.MaxContextWindowTokens != 128000 {
		t.Errorf("expected 128000 context window, got %d", m1.MaxContextWindowTokens)
	}
	if m1.MaxOutputTokens != 16384 {
		t.Errorf("expected 16384 max output, got %d", m1.MaxOutputTokens)
	}
	if m1.MaxPromptImageSize != 2048 {
		t.Errorf("expected 2048 max prompt image size, got %d", m1.MaxPromptImageSize)
	}

	m2 := models[1]
	if m2.ID != "claude-3.5-sonnet" {
		t.Errorf("expected ID claude-3.5-sonnet, got %q", m2.ID)
	}
	if !m2.SupportsReasoning {
		t.Error("expected claude to support reasoning")
	}
}

func TestListModelsFiltersDisabled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(copilotModelsResponse{
			Data: []copilotModelData{
				{
					ID:                 "enabled-model",
					ModelPickerEnabled: true,
					Policy:             copilotModelPolicy{State: "enabled"},
				},
				{
					ID:                 "picker-disabled",
					ModelPickerEnabled: false,
					Policy:             copilotModelPolicy{State: "enabled"},
				},
				{
					ID:                 "policy-disabled",
					ModelPickerEnabled: true,
					Policy:             copilotModelPolicy{State: "disabled"},
				},
				{
					ID:                 "both-disabled",
					ModelPickerEnabled: false,
					Policy:             copilotModelPolicy{State: "disabled"},
				},
			},
		})
	}))
	defer srv.Close()

	ctx := context.Background()
	models, err := ListModels(ctx, "token", srv.URL, 5*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(models))
	}
	if models[0].ID != "enabled-model" {
		t.Errorf("expected 'enabled-model', got %q", models[0].ID)
	}
}

func TestListModelsErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	ctx := context.Background()
	_, err := ListModels(ctx, "bad-token", srv.URL, 5*time.Second)
	if err == nil {
		t.Fatal("expected error for 401 response")
	}
}

func TestListModelsMalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("invalid"))
	}))
	defer srv.Close()

	ctx := context.Background()
	_, err := ListModels(ctx, "token", srv.URL, 5*time.Second)
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestListModelsEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(copilotModelsResponse{Data: []copilotModelData{}})
	}))
	defer srv.Close()

	ctx := context.Background()
	models, err := ListModels(ctx, "token", srv.URL, 5*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(models) != 0 {
		t.Errorf("expected 0 models, got %d", len(models))
	}
}
