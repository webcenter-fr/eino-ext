// Package copilot provides a GitHub Copilot provider implementation.
//
// The Copilot API is OpenAI-compatible. This package makes direct HTTP calls
// using net/http — it does not depend on any openai SDK or ACL library.
package copilot

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/sirupsen/logrus"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
)

var _ model.ToolCallingChatModel = (*CopilotModel)(nil)

// CopilotModel also satisfies the deprecated ChatModel interface for
// backward compatibility with older eino consumers.
//
//nolint:staticcheck
var _ model.ChatModel = (*CopilotModel)(nil)

const copilotGetType = "GitHubCopilot"

// ReasoningEffort controls the reasoning effort level for supported Copilot models.
type ReasoningEffort string

//nolint:revive // ReasoningEffort* consts share this block comment
const (
	// ReasoningEffortNone disables reasoning.
	ReasoningEffortNone    ReasoningEffort = "none"
	ReasoningEffortMinimal ReasoningEffort = "minimal"
	ReasoningEffortLow     ReasoningEffort = "low"
	ReasoningEffortMedium  ReasoningEffort = "medium"
	ReasoningEffortHigh    ReasoningEffort = "high"
	ReasoningEffortXHigh   ReasoningEffort = "xhigh"
	ReasoningEffortMax     ReasoningEffort = "max"
)

// Config holds the configuration for a [CopilotModel].
type Config struct {
	GitHubToken          string          `validate:"omitempty" jsonschema:"description=Fine-grained GitHub PAT (github_pat_...) with Copilot Requests account permission (Read)"`
	CopilotToken         string          `validate:"omitempty" jsonschema:"description=Pre-obtained Copilot bearer token"`
	EnterpriseURL        string          `validate:"omitempty" jsonschema:"description=GitHub Enterprise domain"`
	BaseURL              string          `validate:"omitempty" jsonschema:"description=Override Copilot API base URL"`
	Timeout              time.Duration   `validate:"omitempty,gte=1000000000" jsonschema:"description=API request timeout"`
	TLSSkipVerify        bool            `validate:"omitempty" jsonschema:"description=Skip TLS certificate verification"`
	Model                string          `validate:"omitempty" jsonschema:"description=Model ID to use"`
	Temperature          *float32        `validate:"omitempty,gte=0,lte=2" jsonschema:"description=Sampling temperature (0 to 2)"`
	MaxCompletionTokens  *int            `validate:"omitempty,gte=1" jsonschema:"description=Upper bound on generated tokens"`
	ReasoningEffort      ReasoningEffort `validate:"omitempty" jsonschema:"description=Reasoning effort: none, minimal, low, medium, high, xhigh, max"`
	ForceChatCompletions bool            `validate:"omitempty" jsonschema:"description=Force chat/completions endpoint even for models that would use /responses"`
	SessionToken         string          `validate:"omitempty" jsonschema:"description=Pre-obtained Copilot model session token (JWT)"`
	Logger               *logrus.Entry   `validate:"omitempty" jsonschema:"-"`

	// Chat completion fields backported from kilocode bodyFields.
	FrequencyPenalty *float32 `validate:"omitempty,gte=-2,lte=2" jsonschema:"description=Frequency penalty (-2 to 2)"`
	PresencePenalty  *float32 `validate:"omitempty,gte=-2,lte=2" jsonschema:"description=Presence penalty (-2 to 2)"`
	Seed             *int     `validate:"omitempty" jsonschema:"description=Deterministic sampling seed"`
	Store            *bool    `validate:"omitempty" jsonschema:"description=Store the conversation for later use"`
}

// CopilotModel implements the eino chat model interface for GitHub Copilot.
//
//nolint:revive // CopilotModel is the established public name.
type CopilotModel struct {
	lockedToken   *copilotLockedToken
	baseURL       string
	cfg           *Config
	cancelRefresh context.CancelFunc
	httpClient    *http.Client
	logger        *logrus.Entry

	sessionToken         *copilotSessionToken
	sessionMu            *sync.Mutex
	cancelSessionRefresh context.CancelFunc

	autoModel         *autoModelResolution // nil when auto mode is not in use
	autoMu            *sync.Mutex          // guards autoModel; shared across WithTools copies
	cancelAutoRefresh context.CancelFunc

	modelsCache []ModelInfo
	modelsMu    *sync.RWMutex

	clientMachineID string

	tools      []*schema.ToolInfo
	toolChoice *schema.ToolChoice
}

// NewCopilotChatModel creates a new Copilot chat model from the given config.
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

	// Default to a discard logger when none is provided so the model never
	// panics on a nil logger. Callers can inject a *logrus.Entry to receive
	// structured diagnostics (e.g. ForceChatCompletions routing decisions).
	logger := cfg.Logger
	if logger == nil {
		l := logrus.New()
		l.SetOutput(io.Discard)
		logger = logrus.NewEntry(l)
	}

	// Log once at construction time so operators have immediate visibility
	// when ForceChatCompletions is set — this flag silently downgrades all
	// GPT-5+ models from /responses to /chat/completions on every call.
	if cfg.ForceChatCompletions {
		logger.Warn("copilot: ForceChatCompletions is enabled; all GPT-5+ models will use /chat/completions instead of /responses")
	}

	baseURL := cfg.BaseURL

	lockedToken := &copilotLockedToken{}
	var cancelRefresh context.CancelFunc

	if cfg.CopilotToken != "" {
		lockedToken.set(cfg.CopilotToken)
		if baseURL == "" {
			baseURL = ResolveBaseURL(cfg.EnterpriseURL)
		}
	} else {
		kind := DetectTokenKind(cfg.GitHubToken)

		switch kind {
		case TokenKindCopilotOAuth:
			// gho_ token passed as GitHubToken — treat as CopilotToken
			// (direct-bearer, no exchange, no refresh).
			logger.Infof("copilot: gho_ token passed as GitHubToken; treating as CopilotToken (direct-bearer, no exchange)")
			// No refresh goroutine (token is long-lived OAuth token).
			cancelRefresh = nil
			lockedToken.set(cfg.GitHubToken)
			if baseURL == "" {
				baseURL = ResolveBaseURL(cfg.EnterpriseURL)
			}
		case TokenKindFineGrainedPAT:
			res, err := resolveDirectBearer(ctx, cfg.GitHubToken, cfg.EnterpriseURL, baseURL, cfg.Timeout, logger)
			if err != nil {
				return nil, errors.Wrap(err, "copilot: PAT validation failed")
			}
			lockedToken.set(res.token)
			baseURL = res.baseURL
			// No refresh goroutine in direct-bearer mode (PAT is long-lived).
			cancelRefresh = nil
			logger.Infof("copilot: direct-bearer mode (fine-grained PAT, login=%s) resolved base URL %s (plan=%s)", res.login, baseURL, DetectPlan(baseURL))
		default:
			// Classic PAT (ghp_...) or unknown prefix → exchange path.
			tokenResp, err := exchangeGitHubToken(ctx, cfg.GitHubToken, cfg.EnterpriseURL, cfg.Timeout)
			if err != nil {
				return nil, errors.Wrap(err, "copilot: initial token exchange failed")
			}
			lockedToken.set(tokenResp.Token)

			if baseURL == "" {
				if tokenResp.Endpoints != nil && tokenResp.Endpoints.API != "" {
					baseURL = tokenResp.Endpoints.API
				} else {
					baseURL = ResolveBaseURL(cfg.EnterpriseURL)
				}
			}

			logger.Infof("copilot: resolved base URL %s (plan=%s)", baseURL, DetectPlan(baseURL))

			cancelRefresh = startTokenRefresh(ctx, cfg, tokenResp, func(newToken string) {
				lockedToken.set(newToken)
			})
		}
	}

	httpClient := newHTTPClient(cfg.Timeout, cfg.TLSSkipVerify)

	sessionToken := &copilotSessionToken{logger: logger}
	if cfg.SessionToken != "" {
		sessionToken.token = cfg.SessionToken
	}

	clientMachineID := newUUID()

	return &CopilotModel{
		lockedToken:     lockedToken,
		baseURL:         baseURL,
		cfg:             cfg,
		cancelRefresh:   cancelRefresh,
		httpClient:      httpClient,
		logger:          logger,
		sessionToken:    sessionToken,
		sessionMu:       &sync.Mutex{},
		autoMu:          &sync.Mutex{},
		modelsMu:        &sync.RWMutex{},
		clientMachineID: clientMachineID,
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

// GetType returns the component type identifier.
func (m *CopilotModel) GetType() string { return copilotGetType }

// IsCallbacksEnabled implements components.Checker. CopilotModel does not
// self-instrument eino callbacks (Generate/Stream call the Copilot HTTP API
// directly with no callbacks.OnStart/OnEnd/OnEndWithStreamOutput calls), so
// this must be false: it tells eino's compose/adk graph layer to wrap this
// model with its own callback injection instead of trusting a
// self-instrumentation that never happens. Returning true here silently
// drops every ComponentOfChatModel activity/callback event for every
// Copilot call.
func (m *CopilotModel) IsCallbacksEnabled() bool { return false }

// WithTools returns a copy of the model with the given tools configured.
func (m *CopilotModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	n := *m // safe: mutex fields are pointers (shared across copies), rest are values or safe-to-copy pointers
	n.tools = tools
	if len(tools) > 0 {
		tc := schema.ToolChoiceAllowed
		n.toolChoice = &tc
	}
	return &n, nil
}

// BindTools configures the tools available for the next call.
func (m *CopilotModel) BindTools(tools []*schema.ToolInfo) error {
	m.tools = tools
	if len(tools) > 0 {
		tc := schema.ToolChoiceAllowed
		m.toolChoice = &tc
	}
	return nil
}

func newUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
