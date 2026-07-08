package copilot

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/embedding"
)

type copilotEmbedRequest struct {
	Model          string   `json:"model"`
	Input          []string `json:"input"`
	EncodingFormat string   `json:"encoding_format,omitempty"`
	Dimensions     *int     `json:"dimensions,omitempty"`
}

type copilotEmbedResponse struct {
	Data  []copilotEmbedData `json:"data"`
	Usage *copilotEmbedUsage `json:"usage,omitempty"`
}

type copilotEmbedData struct {
	Embedding []float64 `json:"embedding"`
	Index     int       `json:"index"`
}

type copilotEmbedUsage struct {
	PromptTokens int `json:"prompt_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

var _ embedding.Embedder = (*CopilotEmbedder)(nil)

type CopilotEmbedder struct {
	httpClient *http.Client
	baseURL    string
	model      string
	token      string
}

type EmbedderConfig struct {
	Model         string `validate:"required"`
	TLSSkipVerify bool   `validate:"omitempty"`
}

func NewEmbedder(cfg *EmbedderConfig, copilotToken, baseURL string, timeout time.Duration) *CopilotEmbedder {
	return &CopilotEmbedder{
		httpClient: newHTTPClient(timeout, cfg.TLSSkipVerify),
		baseURL:    baseURL,
		model:      cfg.Model,
		token:      copilotToken,
	}
}

func (e *CopilotEmbedder) EmbedStrings(ctx context.Context, texts []string, opts ...embedding.Option) ([][]float64, error) {
	payload, err := json.Marshal(copilotEmbedRequest{
		Model:          e.model,
		Input:          texts,
		EncodingFormat: "float",
	})
	if err != nil {
		return nil, errors.Wrap(err, "copilot: failed to marshal embedding request")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/embeddings", bytes.NewReader(payload))
	if err != nil {
		return nil, errors.Wrap(err, "copilot: failed to create embedding request")
	}
	req.Header.Set("Content-Type", "application/json")
	setAuthHeaders(req, e.token)

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "copilot: embedding request failed")
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.Wrap(err, "copilot: failed to read embedding response")
	}

	if resp.StatusCode != http.StatusOK {
		return nil, errors.Errorf("copilot: embedding API returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var embedResp copilotEmbedResponse
	if err := json.Unmarshal(respBody, &embedResp); err != nil {
		return nil, errors.Wrap(err, "copilot: failed to decode embedding response")
	}

	result := make([][]float64, len(embedResp.Data))
	for _, d := range embedResp.Data {
		if d.Index >= 0 && d.Index < len(result) {
			result[d.Index] = d.Embedding
		}
	}
	return result, nil
}
