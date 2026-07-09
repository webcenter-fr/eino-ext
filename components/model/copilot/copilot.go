// Package copilot provides a GitHub Copilot provider implementation.
//
// The Copilot API is OpenAI-compatible. This package makes direct HTTP calls
// using net/http — it does not depend on any openai SDK or ACL library.
package copilot

import (
	"context"
	"crypto/tls"
	"net/http"
	"os"
	"time"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
)

var _ model.ToolCallingChatModel = (*CopilotModel)(nil)
var _ model.ChatModel = (*CopilotModel)(nil)

const copilotGetType = "GitHubCopilot"

type ReasoningEffort string

const (
	ReasoningEffortLow    ReasoningEffort = "low"
	ReasoningEffortMedium ReasoningEffort = "medium"
	ReasoningEffortHigh   ReasoningEffort = "high"
)

type Config struct {
	GitHubToken         string          `validate:"omitempty" jsonschema:"description=GitHub PAT with read:user scope"`
	CopilotToken        string          `validate:"omitempty" jsonschema:"description=Pre-obtained Copilot bearer token"`
	EnterpriseURL       string          `validate:"omitempty" jsonschema:"description=GitHub Enterprise domain"`
	BaseURL             string          `validate:"omitempty" jsonschema:"description=Override Copilot API base URL"`
	Timeout             time.Duration   `validate:"omitempty,gte=1000000000" jsonschema:"description=API request timeout"`
	TLSSkipVerify       bool            `validate:"omitempty" jsonschema:"description=Skip TLS certificate verification"`
	Model               string          `validate:"omitempty" jsonschema:"description=Model ID to use"`
	Temperature         *float32        `validate:"omitempty,gte=0,lte=2" jsonschema:"description=Sampling temperature (0 to 2)"`
	MaxCompletionTokens *int            `validate:"omitempty,gte=1" jsonschema:"description=Upper bound on generated tokens"`
	ReasoningEffort     ReasoningEffort `validate:"omitempty" jsonschema:"description=Reasoning effort: low, medium, or high"`
}

type CopilotModel struct {
	lockedToken   *copilotLockedToken
	baseURL       string
	cfg           *Config
	cancelRefresh context.CancelFunc
	httpClient    *http.Client

	tools      []*schema.ToolInfo
	toolChoice *schema.ToolChoice
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
		baseURL = ResolveBaseURL(cfg.EnterpriseURL)
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

	httpClient := newHTTPClient(cfg.Timeout, cfg.TLSSkipVerify)

	return &CopilotModel{
		lockedToken:   lockedToken,
		baseURL:       baseURL,
		cfg:           cfg,
		cancelRefresh: cancelRefresh,
		httpClient:    httpClient,
	}, nil
}

func newHTTPClient(timeout time.Duration, skipVerify bool) *http.Client {
	c := &http.Client{Timeout: timeout}
	if skipVerify {
		c.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
	}
	return c
}

func (m *CopilotModel) GetType() string { return copilotGetType }

func (m *CopilotModel) IsCallbacksEnabled() bool { return true }

func (m *CopilotModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	n := *m
	n.tools = tools
	if len(tools) > 0 && n.toolChoice == nil {
		tc := schema.ToolChoiceAllowed
		n.toolChoice = &tc
	}
	return &n, nil
}

func (m *CopilotModel) BindTools(tools []*schema.ToolInfo) error {
	m.tools = tools
	if len(tools) > 0 {
		tc := schema.ToolChoiceAllowed
		m.toolChoice = &tc
	}
	return nil
}
