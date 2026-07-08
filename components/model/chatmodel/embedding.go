package chatmodel

import (
	"context"
	"crypto/tls"
	"net/http"
	"time"

	"emperror.dev/errors"
	"github.com/cloudwego/eino-ext/libs/acl/openai"
	"github.com/cloudwego/eino/components/embedding"
	copilot "github.com/webcenter-fr/eino-ext/components/model/copilot"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
)

type EmbeddingProvider string

const (
	OllamaEmbeddingProvider  EmbeddingProvider = "ollama"
	OpenAIEmbeddingProvider  EmbeddingProvider = "openai"
	CopilotEmbeddingProvider EmbeddingProvider = "github-copilot"
)

type EmbeddingConfig struct {
	Provider      EmbeddingProvider `validate:"required" jsonschema:"description=Provider plan: ollama, github-copilot, or openai"`
	BaseURL       string            `validate:"required" jsonschema:"description=Provider endpoint URL"`
	Model         string            `validate:"required" jsonschema:"description=Model ID to use"`
	Timeout       int               `validate:"gte=0" jsonschema:"description=Request timeout in seconds (0 uses default)"`
	TLSSkipVerify bool              `validate:"omitempty" jsonschema:"description=Skip TLS certificate verification"`
	APIKey        string            `validate:"omitempty" jsonschema:"description=Provider API key or token (Copilot bearer token for github-copilot)"`
}

func NewEmbedding(ctx context.Context, cfg *EmbeddingConfig) (embedding.Embedder, error) {
	if cfg == nil {
		return nil, errors.New("embedding: config must not be nil")
	}
	if err := validate.Struct(cfg); err != nil {
		return nil, errors.Wrap(err, "embedding: invalid config")
	}

	switch cfg.Provider {
	case CopilotEmbeddingProvider:
		return newCopilotEmbedding(cfg)
	case OpenAIEmbeddingProvider:
		return newOpenAIEmbedding(cfg)
	case OllamaEmbeddingProvider:
		return newOllamaEmbedding(cfg)
	default:
		return nil, errors.Errorf("embedding: unsupported plan: %s", cfg.Provider)
	}
}

func newOpenAIEmbedding(cfg *EmbeddingConfig) (embedding.Embedder, error) {
	t := timeoutDuration(cfg.Timeout)

	embCfg := &openai.EmbeddingConfig{
		APIKey:     cfg.APIKey,
		BaseURL:    cfg.BaseURL,
		Model:      cfg.Model,
		HTTPClient: insecureEmbeddingHTTPClient(cfg.TLSSkipVerify, t),
	}

	return openai.NewEmbeddingClient(context.Background(), embCfg)
}

func newCopilotEmbedding(cfg *EmbeddingConfig) (embedding.Embedder, error) {
	t := timeoutDuration(cfg.Timeout)

	copilotCfg := &copilot.EmbedderConfig{
		Model:         cfg.Model,
		TLSSkipVerify: cfg.TLSSkipVerify,
	}

	return copilot.NewEmbedder(copilotCfg, cfg.APIKey, cfg.BaseURL, t), nil
}

func newOllamaEmbedding(cfg *EmbeddingConfig) (embedding.Embedder, error) {
	return nil, errors.New("embedding: ollama embedding not yet implemented")
}

func timeoutDuration(seconds int) time.Duration {
	if seconds <= 0 {
		return defaultTimeout
	}
	return time.Duration(seconds) * time.Second
}

func insecureEmbeddingHTTPClient(skip bool, timeout time.Duration) *http.Client {
	if !skip {
		return nil
	}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
}
