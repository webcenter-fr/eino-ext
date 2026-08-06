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

// EmbeddingProvider identifies an embedding model provider.
type EmbeddingProvider string

const (
	// OllamaEmbeddingProvider is the Ollama embedding provider.
	OllamaEmbeddingProvider EmbeddingProvider = "ollama"
	// OpenAIEmbeddingProvider is the OpenAI embedding provider.
	OpenAIEmbeddingProvider EmbeddingProvider = "openai"
	// CopilotEmbeddingProvider is the GitHub Copilot embedding provider.
	CopilotEmbeddingProvider EmbeddingProvider = "github-copilot"
)

// EmbeddingConfig holds configuration for embedding providers.
type EmbeddingConfig struct {
	Provider      EmbeddingProvider `validate:"required" jsonschema:"description=Provider plan: ollama, github-copilot, or openai"`
	BaseURL       string            `jsonschema:"description=Provider endpoint URL, uses provider default when empty"`
	Model         string            `validate:"required" jsonschema:"description=Model ID to use"`
	Timeout       time.Duration     `validate:"gte=0" jsonschema:"description=Request timeout in seconds (0 uses default)"`
	TLSSkipVerify bool              `validate:"omitempty" jsonschema:"description=Skip TLS certificate verification"`
	APIKey        string            `validate:"omitempty" jsonschema:"description=Provider API key or token (Copilot bearer token for github-copilot)"`
}

// NewEmbedding creates a new embedding component from the given configuration.
func NewEmbedding(ctx context.Context, cfg *EmbeddingConfig) (embedding.Embedder, error) {
	if cfg == nil {
		return nil, errors.New("embedding: config must not be nil")
	}
	if err := validate.Struct(cfg); err != nil {
		return nil, errors.Wrap(err, "embedding: invalid config")
	}

	switch cfg.Provider {
	case CopilotEmbeddingProvider:
		return newCopilotEmbedding(ctx, cfg)
	case OpenAIEmbeddingProvider:
		return newOpenAIEmbedding(cfg)
	case OllamaEmbeddingProvider:
		return newOllamaEmbedding(cfg)
	default:
		return nil, errors.Errorf("embedding: unsupported plan: %s", cfg.Provider)
	}
}

func newOpenAIEmbedding(cfg *EmbeddingConfig) (embedding.Embedder, error) {

	embCfg := &openai.EmbeddingConfig{
		APIKey:     cfg.APIKey,
		BaseURL:    cfg.BaseURL,
		Model:      cfg.Model,
		HTTPClient: insecureEmbeddingHTTPClient(cfg.TLSSkipVerify, cfg.Timeout),
	}

	return openai.NewEmbeddingClient(context.Background(), embCfg)
}

func newCopilotEmbedding(ctx context.Context, cfg *EmbeddingConfig) (embedding.Embedder, error) {

	copilotCfg := &copilot.EmbedderConfig{
		Model:         cfg.Model,
		TLSSkipVerify: cfg.TLSSkipVerify,
	}

	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = copilot.ResolveBaseURL("")
	}

	return copilot.NewEmbedder(ctx, copilotCfg, cfg.APIKey, baseURL, cfg.Timeout)
}

func newOllamaEmbedding(cfg *EmbeddingConfig) (embedding.Embedder, error) {
	return nil, errors.New("embedding: ollama embedding not yet implemented")
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
