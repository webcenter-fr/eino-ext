package copilot

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"emperror.dev/errors"
)

type ModelInfo struct {
	ID                     string
	Name                   string
	MaxContextWindowTokens int
	MaxOutputTokens        int
	SupportsToolCalls      bool
	SupportsStreaming      bool
	SupportsReasoning      bool
	SupportsVision         bool
	MaxPromptImageSize     int
}

type copilotModelData struct {
	ID                     string                            `json:"id"`
	Name                   string                            `json:"name"`
	ModelPickerEnabled     bool                              `json:"model_picker_enabled"`
	Policy                 copilotModelPolicy                `json:"policy"`
	Capabilities           copilotModelCapabilities          `json:"capabilities"`
	MaxPromptImageSize     int                               `json:"max_prompt_image_size"`
}

type copilotModelPolicy struct {
	State string `json:"state"`
}

type copilotModelCapabilities struct {
	Limits   copilotModelLimits  `json:"limits"`
	Supports []copilotModelSupport `json:"supports"`
}

type copilotModelLimits struct {
	MaxContextWindowTokens int `json:"max_context_window_tokens"`
	MaxOutputTokens        int `json:"max_output_tokens"`
}

type copilotModelSupport struct {
	Type string `json:"type"`
}

type copilotModelsResponse struct {
	Data []copilotModelData `json:"data"`
}

func ListModels(ctx context.Context, copilotToken, baseURL string, timeout time.Duration) ([]ModelInfo, error) {
	url := baseURL + "/models"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, errors.Wrap(err, "copilot: failed to create models request")
	}
	req.Header.Set("Authorization", "Bearer "+copilotToken)
	req.Header.Set("User-Agent", userAgentHeader)
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "copilot: models request failed")
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, errors.Errorf("copilot: models request returned status %d", resp.StatusCode)
	}

	var modelsResp copilotModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&modelsResp); err != nil {
		return nil, errors.Wrap(err, "copilot: failed to decode models response")
	}

	var result []ModelInfo
	for _, m := range modelsResp.Data {
		if !m.ModelPickerEnabled {
			continue
		}
		if m.Policy.State == "disabled" {
			continue
		}

		mi := ModelInfo{
			ID:                     m.ID,
			Name:                   m.Name,
			MaxContextWindowTokens: m.Capabilities.Limits.MaxContextWindowTokens,
			MaxOutputTokens:        m.Capabilities.Limits.MaxOutputTokens,
			MaxPromptImageSize:     m.MaxPromptImageSize,
		}

		for _, s := range m.Capabilities.Supports {
			switch s.Type {
			case "tool_calls":
				mi.SupportsToolCalls = true
			case "streaming":
				mi.SupportsStreaming = true
			case "reasoning":
				mi.SupportsReasoning = true
			case "vision":
				mi.SupportsVision = true
			}
		}

		result = append(result, mi)
	}

	return result, nil
}
