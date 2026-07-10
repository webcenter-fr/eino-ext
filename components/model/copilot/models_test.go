package copilot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func boolPtr(b bool) *bool { return &b }
func intPtr(i int) *int   { return &i }

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
					Version:            "gpt-4o-2024-08-06",
					SupportedEndpoints: []string{"/chat/completions"},
					Policy:             copilotModelPolicy{State: "enabled"},
					Capabilities: copilotModelCapabilities{
						Family: "gpt-4",
						Limits: copilotModelLimits{
							MaxContextWindowTokens: 128000,
							MaxOutputTokens:        16384,
							MaxPromptTokens:        128000,
							Vision: &copilotModelVision{
								MaxPromptImageSize:  2048,
								MaxPromptImages:     10,
								SupportedMediaTypes: []string{"image/png", "image/jpeg"},
							},
						},
						Supports: copilotModelSupports{
							ToolCalls: true,
							Streaming: true,
							Vision:    boolPtr(true),
						},
					},
				},
				{
					ID:                 "claude-3.5-sonnet",
					Name:               "Claude 3.5 Sonnet",
					ModelPickerEnabled: true,
					Version:            "claude-3.5-sonnet-2024-10-22",
					Policy:             copilotModelPolicy{State: "enabled"},
					Capabilities: copilotModelCapabilities{
						Family: "claude-3",
						Limits: copilotModelLimits{
							MaxContextWindowTokens: 200000,
							MaxOutputTokens:        8192,
							MaxPromptTokens:        200000,
						},
						Supports: copilotModelSupports{
							ToolCalls:        true,
							Streaming:        true,
							AdaptiveThinking: boolPtr(true),
							ReasoningEffort:  []string{"low", "medium", "high"},
							MaxThinkingBudget: intPtr(32000),
						},
					},
				},
				{
					ID:                 "gpt-5",
					Name:               "GPT-5",
					ModelPickerEnabled: true,
					Version:            "gpt-5-2025-06-01",
					SupportedEndpoints: []string{"/chat/completions", "/responses"},
					Policy:             copilotModelPolicy{State: "enabled"},
					Capabilities: copilotModelCapabilities{
						Family: "gpt-5",
						Limits: copilotModelLimits{
							MaxContextWindowTokens: 128000,
							MaxOutputTokens:        16384,
							MaxPromptTokens:        128000,
							Vision: &copilotModelVision{
								MaxPromptImageSize:  2048,
								MaxPromptImages:     10,
								SupportedMediaTypes: []string{"image/png", "image/jpeg"},
							},
						},
						Supports: copilotModelSupports{
							ToolCalls:       true,
							Streaming:       true,
							Vision:          boolPtr(true),
							ReasoningEffort: []string{"low", "medium", "high"},
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

	if len(models) != 3 {
		t.Fatalf("expected 3 models, got %d", len(models))
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
	if m1.MaxPromptImages != 10 {
		t.Errorf("expected 10 max prompt images, got %d", m1.MaxPromptImages)
	}
	if m1.Family != "gpt-4" {
		t.Errorf("expected family 'gpt-4', got %q", m1.Family)
	}
	if m1.Version != "gpt-4o-2024-08-06" {
		t.Errorf("expected version 'gpt-4o-2024-08-06', got %q", m1.Version)
	}

	m2 := models[1]
	if m2.ID != "claude-3.5-sonnet" {
		t.Errorf("expected ID claude-3.5-sonnet, got %q", m2.ID)
	}
	if !m2.SupportsReasoning {
		t.Error("expected claude to support reasoning")
	}
	if len(m2.ReasoningEfforts) != 3 {
		t.Errorf("expected 3 reasoning efforts, got %d", len(m2.ReasoningEfforts))
	}

	m3 := models[2]
	if m3.ID != "gpt-5" {
		t.Errorf("expected ID gpt-5, got %q", m3.ID)
	}
	if !m3.SupportsReasoning {
		t.Error("expected gpt-5 to support reasoning")
	}
	if len(m3.SupportedEndpoints) != 2 {
		t.Errorf("expected 2 supported endpoints, got %d", len(m3.SupportedEndpoints))
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

func TestListModelsVisionViaMediaTypes(t *testing.T) {
	// Vision should be detected via limits.vision.supported_media_types even
	// when the "vision" supports flag is not explicitly true.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(copilotModelsResponse{
			Data: []copilotModelData{
				{
					ID:                 "vision-from-media",
					Name:               "Vision via Media",
					ModelPickerEnabled: true,
					Policy:             copilotModelPolicy{State: "enabled"},
					Capabilities: copilotModelCapabilities{
						Family: "test",
						Limits: copilotModelLimits{
							MaxContextWindowTokens: 128000,
							MaxOutputTokens:        16384,
							MaxPromptTokens:        128000,
							Vision: &copilotModelVision{
								MaxPromptImageSize:  2048,
								MaxPromptImages:     5,
								SupportedMediaTypes: []string{"image/png", "image/jpeg"},
							},
						},
						Supports: copilotModelSupports{
							ToolCalls: true,
							Streaming: true,
							// Vision flag NOT set — must be inferred from media types.
						},
					},
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
	if !models[0].SupportsVision {
		t.Error("expected vision support via media types")
	}
}
