package copilot

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"emperror.dev/errors"
	"github.com/sirupsen/logrus"
)

const maxModelPickerLog = 5

// ModelInfo describes a Copilot model's capabilities and limits.
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
	State                  string
	ModelPickerEnabled     bool
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

// flexibleBool handles JSON fields that can be a boolean or the string
// "unsupported" (Copilot API returns "unsupported" for adaptive_thinking on
// some models).  Marshals to false for "unsupported", the raw bool value
// otherwise, and nil on absence.
type flexibleBool struct {
	Set   bool
	Value bool
}

func (fb *flexibleBool) UnmarshalJSON(data []byte) error {
	var b bool
	if json.Unmarshal(data, &b) == nil {
		fb.Set = true
		fb.Value = b
		return nil
	}
	var s string
	if json.Unmarshal(data, &s) != nil {
		return nil
	}
	if s == "unsupported" {
		fb.Set = true
		fb.Value = false
	}
	return nil
}

func (fb flexibleBool) MarshalJSON() ([]byte, error) {
	if !fb.Set {
		return []byte("null"), nil
	}
	return json.Marshal(fb.Value)
}

// copilotModelSupports matches the real Copilot API /models response shape where
// "supports" is an object (not an array). The previous []copilotModelSupport
// type never populated any capability flag against the live API.
type copilotModelSupports struct {
	ToolCalls         bool         `json:"tool_calls"`
	Streaming         bool         `json:"streaming"`
	Vision            *bool        `json:"vision,omitempty"`
	AdaptiveThinking  flexibleBool `json:"adaptive_thinking,omitempty"`
	StructuredOutputs *bool        `json:"structured_outputs,omitempty"`
	ReasoningEffort   []string     `json:"reasoning_effort,omitempty"`
	MaxThinkingBudget *int         `json:"max_thinking_budget,omitempty"`
	MinThinkingBudget *int         `json:"min_thinking_budget,omitempty"`
}

// ListModels fetches available models from GET /models with the
// Copilot-Integration-Id header to return the full catalog.
func ListModels(ctx context.Context, copilotToken, baseURL string, timeout time.Duration) ([]ModelInfo, error) {
	return listModelsWithClient(ctx, copilotToken, baseURL, &http.Client{Timeout: timeout})
}

// listModelsWithClient is the inner implementation of ListModels that accepts
// an http.Client so callers can reuse a shared transport (TLS settings, etc.).
func listModelsWithClient(ctx context.Context, copilotToken, baseURL string, client *http.Client) ([]ModelInfo, error) {
	url := baseURL + "/models"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, errors.Wrap(err, "copilot: failed to create models request")
	}
	// Send copilot-integration-id to receive the full 32-model catalog.
	// Without this header, the API returns only legacy models (~7) and
	// premium models (GPT-5, Claude, Gemini) are hidden.
	req.Header.Set("Authorization", "Bearer "+copilotToken)
	req.Header.Set("User-Agent", userAgentHeader)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Copilot-Integration-Id", integrationID)
	req.Header.Set("Editor-Version", editorVersion)
	req.Header.Set("X-GitHub-Api-Version", copilotAPIVersion)

	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "copilot: models request failed")
	}
	//nolint:errcheck // defer close in request path, error is irrelevant
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.Wrap(err, "copilot: failed to read models response body")
	}

	if resp.StatusCode != http.StatusOK {
		return nil, errors.Errorf("copilot: models request returned status %d: %s", resp.StatusCode, redactErrorBody(body))
	}

	// A proxy/gateway error page (HTML, plain text, auth challenge) served with
	// a 200 will not parse into our schema; surface the content-type and body
	// instead of silently decoding into an empty model list.
	var modelsResp copilotModelsResponse
	if err := json.Unmarshal(body, &modelsResp); err != nil {
		return nil, errors.Wrapf(err, "copilot: failed to decode models response (content-type %q, body: %s)", resp.Header.Get("Content-Type"), redactErrorBody(body))
	}

	var result []ModelInfo
	var skippedPicker int
	for _, m := range modelsResp.Data {
		if !m.ModelPickerEnabled {
			if skippedPicker < maxModelPickerLog {
				logrus.Debugf("copilot ListModels: model %q has model_picker_enabled=false", m.ID)
				skippedPicker++
			}
		}
		if m.Policy.State == "disabled" {
			logrus.Debugf("copilot ListModels: model %q has policy.state=disabled", m.ID)
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
		if supports.AdaptiveThinking.Set && supports.AdaptiveThinking.Value {
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
			State:                  m.Policy.State,
			ModelPickerEnabled:     m.ModelPickerEnabled,
		}

		if m.Capabilities.Limits.Vision != nil {
			mi.MaxPromptImageSize = m.Capabilities.Limits.Vision.MaxPromptImageSize
			mi.MaxPromptImages = m.Capabilities.Limits.Vision.MaxPromptImages
		}

		result = append(result, mi)
	}

	return result, nil
}
