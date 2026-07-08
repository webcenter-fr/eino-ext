// Package chatmodel provides a provider-generic factory for constructing an
// eino model.ToolCallingChatModel from a single, additive Config.
//
// It maps a thinking *level* (Off/Low/Medium/High) onto the underlying
// provider's reasoning configuration and caps output tokens, so the same
// construction logic can be reused across eino projects regardless of the
// concrete provider (Ollama, OpenAI, or an OpenAI-compatible "github-copilot"
// endpoint).
//
// The factory is intentionally minimal and additive: the returned model is a
// plain model.ToolCallingChatModel that callers may further decorate (for
// example with components/model/cachestab).
//
// Provider notes:
//   - OpenAI/github-copilot (openai@v0.1.13) supports only Low/Medium/High reasoning
//     effort. Off means "omit reasoning" (the field is left unset), which keeps
//     non-reasoning models unaffected.
//   - Ollama has no reasoning *levels*; it only exposes a boolean "think"
//     toggle. Any non-Off level therefore collapses to true.
package chatmodel

import (
	"context"
	"crypto/tls"
	"net/http"
	"strings"
	"time"

	"emperror.dev/errors"
	ollama "github.com/cloudwego/eino-ext/components/model/ollama"
	openai "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
	copilot "github.com/webcenter-fr/eino-ext/components/model/copilot"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
	"k8s.io/utils/ptr"
)

// ThinkingLevel is a provider-generic reasoning level. Providers that lack
// levels (Ollama) collapse any non-Off value to "thinking enabled".
type ThinkingLevel string

const (
	// Off disables reasoning. For OpenAI/github-copilot the reasoning effort field is
	// left unset; for Ollama the think toggle is false.
	Off ThinkingLevel = "off"
	// Low maps to the provider's lowest reasoning effort.
	Low ThinkingLevel = "low"
	// Medium maps to the provider's medium reasoning effort and is the default
	// when a truthy-but-unqualified thinking value (e.g. "true") is parsed.
	Medium ThinkingLevel = "medium"
	// High maps to the provider's highest reasoning effort.
	High ThinkingLevel = "high"
)

type Provider string

const (
	OllamaProvider  Provider = "ollama"
	OpenAIProvider  Provider = "openai"
	CopilotProvider Provider = "github-copilot"
)

// OutputTokenMax is the default ceiling for generated output tokens, mirroring
// kilocode's OUTPUT_TOKEN_MAX. Used by CapOutputTokens when no ceiling is given.
const OutputTokenMax = 32_000

// defaultTimeout is the request timeout applied to the openai/github-copilot path when
// Config.Timeout is zero.
const defaultTimeout = 60 * time.Minute

// ParseThinkingLevel parses a provider-generic thinking level from a string.
//
// Parsing is case-insensitive and trims surrounding whitespace. The empty
// string and the aliases "false"/"none" map to Off; "true" maps to the
// documented default of Medium. Any other unrecognized value is an error.
func ParseThinkingLevel(s string) (ThinkingLevel, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "false", "none", "off":
		return Off, nil
	case "true":
		return Medium, nil
	case "low":
		return Low, nil
	case "medium":
		return Medium, nil
	case "high":
		return High, nil
	default:
		return "", errors.Errorf("chatmodel: unknown thinking level: %q", s)
	}
}

// Config describes how to construct a model.ToolCallingChatModel. It is a struct
// of options so future fields can be added without breaking callers.
type Config struct {
	// Plan selects the provider: "ollama", "github-copilot", or "openai".
	// "github-copilot" and "openai" share the OpenAI-compatible construction path.
	Provider Provider `validate:"required" jsonschema:"description=Provider plan: ollama, github-copilot, or openai"`

	// BaseURL is the provider endpoint URL.
	BaseURL string `validate:"required" jsonschema:"description=Provider endpoint URL"`

	// Model is the model ID to use.
	Model string `validate:"required" jsonschema:"description=Model ID to use"`

	// Temperature is the sampling temperature.
	Temperature float32 `validate:"gte=0,lte=2" jsonschema:"description=Sampling temperature"`

	// Thinking is the reasoning level. Off omits reasoning.
	Thinking ThinkingLevel `jsonschema:"description=Reasoning level: off, low, medium, or high"`

	// MaxOutputTokens caps generated output tokens. 0 leaves the provider
	// default unset.
	MaxOutputTokens int `validate:"gte=0" jsonschema:"description=Maximum generated output tokens, 0 leaves provider default"`

	// Timeout is the request timeout for the openai/github-copilot path. 0 uses the
	// package default of 60m.
	Timeout time.Duration `validate:"gte=0" jsonschema:"description=Request timeout for openai/github-copilot path, 0 uses default of 60m"`

	// TLSSkipVerify disables TLS certificate verification. Useful for self-hosted
	// providers with self-signed certificates.
	TLSSkipVerify bool `validate:"omitempty" jsonschema:"description=Skip TLS certificate verification"`

	// APIKey is the provider API key or token (Copilot bearer token for github-copilot).
	APIKey string `validate:"omitempty" jsonschema:"description=Provider API key or token (Copilot bearer token for github-copilot)"`
}

// New constructs a model.ToolCallingChatModel from cfg.
//
// Supported plans are "ollama", "github-copilot", and "openai"; github-copilot
// and openai share the OpenAI-compatible path. Construction errors are wrapped
// with emperror.dev/errors.
func New(ctx context.Context, cfg *Config) (model.ToolCallingChatModel, error) {
	if cfg == nil {
		return nil, errors.New("chatmodel: config must not be nil")
	}
	if err := validate.Struct(cfg); err != nil {
		return nil, errors.Wrap(err, "chatmodel: invalid config")
	}

	switch cfg.Provider {
	case OllamaProvider:
		return newOllama(ctx, cfg)
	case CopilotProvider:
		return newCopilot(ctx, cfg)
	case OpenAIProvider:
		return newOpenAI(ctx, cfg)
	default:
		return nil, errors.Errorf("chatmodel: unsupported plan: %s", cfg.Provider)
	}
}

func newOllama(ctx context.Context, cfg *Config) (model.ToolCallingChatModel, error) {
	options := &ollama.Options{Temperature: cfg.Temperature}
	if cfg.MaxOutputTokens > 0 {
		options.NumPredict = cfg.MaxOutputTokens
	}

	conf := &ollama.ChatModelConfig{
		BaseURL:    cfg.BaseURL,
		Model:      cfg.Model,
		Options:    options,
		HTTPClient: insecureHTTPClient(cfg.TLSSkipVerify, 0),
		Thinking:   &ollama.ThinkValue{Value: cfg.Thinking != Off},
	}

	m, err := ollama.NewChatModel(ctx, conf)
	if err != nil {
		return nil, errors.Wrap(err, "chatmodel: building ollama model")
	}
	return m, nil
}

func newCopilot(ctx context.Context, cfg *Config) (model.ToolCallingChatModel, error) {
	copilotCfg := &copilot.Config{
		CopilotToken:  cfg.APIKey,
		BaseURL:       cfg.BaseURL,
		Timeout:       cfg.Timeout,
		TLSSkipVerify: cfg.TLSSkipVerify,
	}

	m, err := copilot.NewCopilotChatModel(ctx, copilotCfg)
	if err != nil {
		return nil, errors.Wrap(err, "chatmodel: building copilot model")
	}
	return m, nil
}

func newOpenAI(ctx context.Context, cfg *Config) (model.ToolCallingChatModel, error) {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}

	conf := &openai.ChatModelConfig{
		APIKey:     cfg.APIKey,
		BaseURL:    cfg.BaseURL,
		Model:      cfg.Model,
		Timeout:    timeout,
		HTTPClient: insecureHTTPClient(cfg.TLSSkipVerify, timeout),
	}

	// Only set Temperature when explicitly requested (> 0); leaving it unset
	// preserves the provider default and avoids sending temperature=0 to
	// reasoning models that reject it.
	if cfg.Temperature > 0 {
		conf.Temperature = ptr.To(cfg.Temperature)
	}

	// Only set reasoning effort for non-Off levels so non-reasoning models are
	// unaffected.
	if effort, ok := reasoningEffort(cfg.Thinking); ok {
		conf.ReasoningEffort = effort
	}

	// Only cap completion tokens when an explicit value is requested.
	if cfg.MaxOutputTokens > 0 {
		conf.MaxCompletionTokens = ptr.To(cfg.MaxOutputTokens)
	}

	m, err := openai.NewChatModel(ctx, conf)
	if err != nil {
		return nil, errors.Wrap(err, "chatmodel: building openai model")
	}
	return m, nil
}

// reasoningEffort maps a ThinkingLevel onto the OpenAI reasoning effort level.
// It returns ok=false for Off (and any unknown level), signaling the caller to
// leave the reasoning effort unset.
func reasoningEffort(l ThinkingLevel) (openai.ReasoningEffortLevel, bool) {
	switch l {
	case Low:
		return openai.ReasoningEffortLevelLow, true
	case Medium:
		return openai.ReasoningEffortLevelMedium, true
	case High:
		return openai.ReasoningEffortLevelHigh, true
	default:
		return "", false
	}
}

// CapOutputTokens returns the effective output-token limit, bounded by ceiling.
//
// If ceiling <= 0 it defaults to OutputTokenMax. If modelOutputLimit <= 0 the
// model's limit is treated as unknown and ceiling is returned. Otherwise the
// smaller of modelOutputLimit and ceiling is returned. Mirrors kilocode's
// transform.ts output-token capping.
func CapOutputTokens(modelOutputLimit, ceiling int) int {
	if ceiling <= 0 {
		ceiling = OutputTokenMax
	}
	if modelOutputLimit <= 0 {
		return ceiling
	}
	return min(modelOutputLimit, ceiling)
}

// insecureHTTPClient returns an *http.Client with TLS certificate verification
// disabled when skip is true. The client honors the given timeout.
// When skip is false it returns nil, letting the model library use its default
// (which respects the Timeout field).
func insecureHTTPClient(skip bool, timeout time.Duration) *http.Client {
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
