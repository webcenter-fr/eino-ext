// Package copilot provides a GitHub Copilot chat model implementation.
package copilot

import (
	"context"
	"net/http"
	"os"
	"strings"
	"time"

	"emperror.dev/errors"
	openai "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
)

var _ model.ToolCallingChatModel = (*CopilotModel)(nil)
var _ model.ChatModel = (*CopilotModel)(nil)

const copilotGetType = "GitHubCopilot"

type Config struct {
	GitHubToken   string        `validate:"omitempty" jsonschema:"description=GitHub PAT with read:user scope"`
	CopilotToken  string        `validate:"omitempty" jsonschema:"description=Pre-obtained Copilot bearer token"`
	EnterpriseURL string        `validate:"omitempty" jsonschema:"description=GitHub Enterprise domain"`
	BaseURL       string        `validate:"omitempty" jsonschema:"description=Override Copilot API base URL"`
	Timeout       time.Duration `validate:"omitempty,gte=1000000000" jsonschema:"description=API request timeout"`
	TLSSkipVerify bool          `validate:"omitempty" jsonschema:"description=Skip TLS certificate verification"`
}

type CopilotModel struct {
	inner        model.ToolCallingChatModel
	lockedToken  *copilotLockedToken
	baseURL      string
	cfg          *Config
	cancelRefresh context.CancelFunc
}

func NewCopilotChatModel(ctx context.Context, cfg *Config) (*CopilotModel, error) {
	if cfg == nil {
		return nil, errors.New("copilot: config must not be nil")
	}

	if cfg.GitHubToken == "" {
		cfg.GitHubToken = os.Getenv("GITHUB_TOKEN")
	}
	if cfg.CopilotToken == "" {
		cfg.CopilotToken = os.Getenv("GITHUB_COPILOT_TOKEN")
	}
	if cfg.EnterpriseURL == "" {
		cfg.EnterpriseURL = os.Getenv("GITHUB_COPILOT_ENTERPRISE_URL")
	}

	if cfg.GitHubToken == "" && cfg.CopilotToken == "" {
		return nil, errors.New("copilot: at least one of GitHubToken or CopilotToken must be set")
	}

	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultTimeout
	}

	if err := validate.Struct(cfg); err != nil {
		return nil, err
	}

	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = resolveBaseURL(cfg.EnterpriseURL)
	}
	lockedToken := &copilotLockedToken{}
	var cancelRefresh context.CancelFunc

	if cfg.CopilotToken != "" {
		lockedToken.set(cfg.CopilotToken)
	} else {
		tokenResp, err := exchangeGitHubToken(ctx, cfg.GitHubToken, cfg.EnterpriseURL, cfg.Timeout)
		if err != nil {
			return nil, errors.Wrap(err, "copilot: initial token exchange failed")
		}
		lockedToken.set(tokenResp.Token)

		cancelRefresh = startTokenRefresh(ctx, cfg, tokenResp, func(newToken string) {
			lockedToken.set(newToken)
		})
	}

	inner, err := newInnerModel(baseURL, cfg)
	if err != nil {
		if cancelRefresh != nil {
			cancelRefresh()
		}
		return nil, errors.Wrap(err, "copilot: failed to create inner model")
	}

	return &CopilotModel{
		inner:        inner,
		lockedToken:  lockedToken,
		baseURL:      baseURL,
		cfg:          cfg,
		cancelRefresh: cancelRefresh,
	}, nil
}

func newInnerModel(baseURL string, cfg *Config) (model.ToolCallingChatModel, error) {
	var httpClient *http.Client
	if cfg.TLSSkipVerify {
		httpClient = insecureHTTPClient(cfg.Timeout)
	}

	openaiCfg := &openai.ChatModelConfig{
		APIKey:    "",
		BaseURL:   strings.TrimRight(baseURL, "/"),
		Timeout:   cfg.Timeout,
		HTTPClient: httpClient,
	}

	return openai.NewChatModel(context.Background(), openaiCfg)
}

func (m *CopilotModel) authHeaders() model.Option {
	return openai.WithExtraHeader(map[string]string{
		"Authorization":           "Bearer " + m.lockedToken.get(),
		"Copilot-Integration-ID":  "vscode-chat",
		"Editor-Version":          "vscode/1.100.0",
		"Editor-Plugin-Version":   "copilot-chat/0.52.0",
		"User-Agent":              userAgentHeader,
		"Openai-Intent":           "conversation-agent",
	})
}

func (m *CopilotModel) Generate(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	return m.inner.Generate(ctx, in, append([]model.Option{m.authHeaders()}, opts...)...)
}

func (m *CopilotModel) Stream(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return m.inner.Stream(ctx, in, append([]model.Option{m.authHeaders()}, opts...)...)
}

func (m *CopilotModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	innerWithTools, err := m.inner.WithTools(tools)
	if err != nil {
		return nil, err
	}
	return &CopilotModel{
		inner:        innerWithTools,
		lockedToken:  m.lockedToken,
		baseURL:      m.baseURL,
		cfg:          m.cfg,
		cancelRefresh: m.cancelRefresh,
	}, nil
}

func (m *CopilotModel) BindTools(tools []*schema.ToolInfo) error {
	if ch, ok := m.inner.(model.ChatModel); ok {
		return ch.BindTools(tools)
	}
	return errors.New("copilot: inner model does not implement BindTools")
}

func (m *CopilotModel) GetType() string {
	return copilotGetType
}

func (m *CopilotModel) IsCallbacksEnabled() bool {
	return true
}
