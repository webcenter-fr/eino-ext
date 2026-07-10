package copilot

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"emperror.dev/errors"
)

type ModelInfo struct {
	ID                     string
	Name                   string
	MaxContextWindowTokens int
	MaxOutputTokens        int
	MaxPromptTokens        int
	MaxPromptImageSize     int
	MaxPromptImages        int
	SupportsToolCalls      bool
	SupportsStreaming      bool
	SupportsReasoning      bool
	SupportsVision         bool
	Family                 string
	Version                string
	ReasoningEfforts       []string
	SupportedEndpoints     []string
}

type copilotModelsResponse struct {
	Data []copilotModelData `json:"data"`
}

type copilotModelData struct {
	ID                 string                 `json:"id"`
	Name               string                 `json:"name"`
	ModelPickerEnabled bool                   `json:"model_picker_enabled"`
	Version            string                 `json:"version"`
	SupportedEndpoints []string               `json:"supported_endpoints,omitempty"`
	Policy             copilotModelPolicy     `json:"policy"`
	Capabilities       copilotModelCapabilities `json:"capabilities"`
}

type copilotModelPolicy struct {
	State string `json:"state"`
}

type copilotModelCapabilities struct {
	Family   string              `json:"family"`
	Limits   copilotModelLimits  `json:"limits"`
	Supports copilotModelSupports `json:"supports"`
}

type copilotModelLimits struct {
	MaxContextWindowTokens int                  `json:"max_context_window_tokens"`
	MaxOutputTokens        int                  `json:"max_output_tokens"`
	MaxPromptTokens        int                  `json:"max_prompt_tokens"`
	Vision                 *copilotModelVision  `json:"vision,omitempty"`
}

type copilotModelVision struct {
	MaxPromptImageSize   int      `json:"max_prompt_image_size"`
	MaxPromptImages      int      `json:"max_prompt_images"`
	SupportedMediaTypes  []string `json:"supported_media_types"`
}

// copilotModelSupports matches the real Copilot API /models response shape where
// "supports" is an object (not an array). The previous []copilotModelSupport
// type never populated any capability flag against the live API.
type copilotModelSupports struct {
	ToolCalls        bool     `json:"tool_calls"`
	Streaming        bool     `json:"streaming"`
	Vision           *bool    `json:"vision,omitempty"`
	AdaptiveThinking *bool    `json:"adaptive_thinking,omitempty"`
	StructuredOutputs *bool   `json:"structured_outputs,omitempty"`
	ReasoningEffort  []string `json:"reasoning_effort,omitempty"`
	MaxThinkingBudget *int    `json:"max_thinking_budget,omitempty"`
	MinThinkingBudget *int    `json:"min_thinking_budget,omitempty"`
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

		supports := m.Capabilities.Supports

		// SupportsVision: true if the "vision" flag is set OR if any vision
		// limit media type starts with "image/".
		hasVision := supports.Vision != nil && *supports.Vision
		if !hasVision && m.Capabilities.Limits.Vision != nil {
			for _, mt := range m.Capabilities.Limits.Vision.SupportedMediaTypes {
				if strings.HasPrefix(mt, "image/") {
					hasVision = true
					break
				}
			}
		}

		// SupportsReasoning: true if any reasoning-related capability is set.
		hasReasoning := false
		if supports.AdaptiveThinking != nil && *supports.AdaptiveThinking {
			hasReasoning = true
		}
		if len(supports.ReasoningEffort) > 0 {
			hasReasoning = true
		}
		if supports.MaxThinkingBudget != nil || supports.MinThinkingBudget != nil {
			hasReasoning = true
		}

		mi := ModelInfo{
			ID:                     m.ID,
			Name:                   m.Name,
			MaxContextWindowTokens: m.Capabilities.Limits.MaxContextWindowTokens,
			MaxOutputTokens:        m.Capabilities.Limits.MaxOutputTokens,
			MaxPromptTokens:        m.Capabilities.Limits.MaxPromptTokens,
			SupportsToolCalls:      supports.ToolCalls,
			SupportsStreaming:      supports.Streaming,
			SupportsVision:         hasVision,
			SupportsReasoning:      hasReasoning,
			Family:                 m.Capabilities.Family,
			Version:                m.Version,
			ReasoningEfforts:       supports.ReasoningEffort,
			SupportedEndpoints:     m.SupportedEndpoints,
		}

		if m.Capabilities.Limits.Vision != nil {
			mi.MaxPromptImageSize = m.Capabilities.Limits.Vision.MaxPromptImageSize
			mi.MaxPromptImages = m.Capabilities.Limits.Vision.MaxPromptImages
		}

		result = append(result, mi)
	}

	return result, nil
}
