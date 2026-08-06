package copilot

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sync"
	"time"

	"emperror.dev/errors"
	"github.com/sirupsen/logrus"
)

const (
	tokenURLPath       = "/copilot_internal/v2/token"
	defaultAPIBase     = "https://api.github.com"
	defaultCopilotBase = "https://api.individual.githubcopilot.com"

	userAgentHeader = "copilot/1.0.70 (client/github/cli linux v24.16.0) term/unknown"

	gitHubAPIVersion    = "2025-04-01"
	copilotAPIVersion   = "2026-07-01"
	integrationID       = "copilot-developer-cli"
	editorVersion       = "copilot/1.0.70"

	refreshBufSecs     = 60
	refreshMinSecs     = 1
	backoffInitialSecs = 15
	backoffMaxSecs     = 600
	backoffJitterSecs  = 15

	defaultTimeout = 60 * time.Minute
)

type copilotTokenResponse struct {
	Token     string                `json:"token"`
	ExpiresAt int64                 `json:"expires_at"`
	RefreshIn int                   `json:"refresh_in"`
	Endpoints *copilotTokenEndpoints `json:"endpoints,omitempty"`
}

type copilotTokenEndpoints struct {
	API           string `json:"api,omitempty"`
	OriginTracker string `json:"origin-tracker,omitempty"`
	Proxy         string `json:"proxy,omitempty"`
	Telemetry     string `json:"telemetry,omitempty"`
}

func exchangeGitHubToken(ctx context.Context, githubToken, enterpriseURL string, timeout time.Duration) (*copilotTokenResponse, error) {
	apiBase := defaultAPIBase
	if enterpriseURL != "" {
		apiBase = fmt.Sprintf("https://api.%s", enterpriseURL)
	}
	return exchangeGitHubTokenWithBase(ctx, githubToken, apiBase, timeout)
}

func exchangeGitHubTokenWithBase(ctx context.Context, githubToken, apiBase string, timeout time.Duration) (*copilotTokenResponse, error) {
	url := apiBase + tokenURLPath

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, errors.Wrap(err, "copilot: failed to create token exchange request")
	}
	req.Header.Set("Authorization", "token "+githubToken)
	req.Header.Set("User-Agent", userAgentHeader)
	req.Header.Set("X-GitHub-Api-Version", gitHubAPIVersion)
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "copilot: token exchange request failed")
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, exchangeError(resp.StatusCode, apiBase)
	}

	var tokenResp copilotTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, errors.Wrap(err, "copilot: failed to decode token exchange response")
	}

	if tokenResp.Token == "" {
		return nil, errors.New("copilot: token exchange returned empty token")
	}

	return &tokenResp, nil
}

func startTokenRefresh(
	ctx context.Context,
	cfg *Config,
	tokenResp *copilotTokenResponse,
	onRefresh func(newToken string),
) context.CancelFunc {
	ctx, cancel := context.WithCancel(ctx)

	if cfg.GitHubToken == "" || tokenResp == nil {
		cancel()
		return cancel
	}

	if tokenResp.ExpiresAt <= 0 {
		if cfg.Logger != nil {
			cfg.Logger.Warn("copilot: token has no expiry (expires_at <= 0); background refresh will not be started")
		}
		cancel()
		return cancel
	}

	go func() {
		var currentToken string
		currentExpiresAt := tokenResp.ExpiresAt

		for {
			sleepSecs := int64(currentExpiresAt) - time.Now().Unix() - refreshBufSecs
			if sleepSecs < refreshMinSecs {
				sleepSecs = refreshMinSecs
			}
			sleepDuration := time.Duration(sleepSecs) * time.Second

			select {
			case <-ctx.Done():
				return
			case <-time.After(sleepDuration):
			}

			newResp, err := exchangeGitHubToken(ctx, cfg.GitHubToken, cfg.EnterpriseURL, cfg.Timeout)
			if err == nil {
				currentToken = newResp.Token
				currentExpiresAt = newResp.ExpiresAt
				if onRefresh != nil {
					onRefresh(currentToken)
				}
				continue
			}

			backoffSecs := backoffInitialSecs
			for {
				jitter := time.Duration(cryptoRandIntn(backoffJitterSecs*2)-backoffJitterSecs) * time.Second
				select {
				case <-ctx.Done():
					return
				case <-time.After(time.Duration(backoffSecs)*time.Second + jitter):
				}

				newResp, err = exchangeGitHubToken(ctx, cfg.GitHubToken, cfg.EnterpriseURL, cfg.Timeout)
				if err == nil {
					currentToken = newResp.Token
					currentExpiresAt = newResp.ExpiresAt
					if onRefresh != nil {
						onRefresh(currentToken)
					}
					break
				}

				backoffSecs = int(math.Min(float64(backoffSecs*2), backoffMaxSecs))
				select {
				case <-ctx.Done():
					return
				default:
				}
			}
		}
	}()

	return cancel
}

// ResolveBaseURL returns the Copilot API base URL for the given enterprise URL.
// When enterpriseURL is empty, it returns the default individual-plan Copilot API
// base (https://api.individual.githubcopilot.com). When enterpriseURL is set, it
// returns https://copilot-api.{enterpriseURL}.
//
// Note: this default only works for individual-plan Copilot subscriptions. For
// business/enterprise plans, prefer the exchange response's endpoints.api field
// (see ResolveCopilotToken and NewCopilotChatModel).
func ResolveBaseURL(enterpriseURL string) string {
	if enterpriseURL != "" {
		return fmt.Sprintf("https://copilot-api.%s", enterpriseURL)
	}
	return defaultCopilotBase
}

// directBearerResolution holds the result of resolving a fine-grained PAT for
// direct-bearer mode. It is returned by resolveDirectBearer and consumed by
// ResolveCopilotToken, NewCopilotChatModel, and Check.
type directBearerResolution struct {
	token   string // the PAT itself
	baseURL string // resolved Copilot API base URL
	login   string // GitHub login (from /copilot_internal/user), empty if best-effort
}

// resolveDirectBearer validates a fine-grained PAT via GET /copilot_internal/user
// and resolves the Copilot API base URL. It is the single shared helper for the
// direct-bearer path, called by ResolveCopilotToken, NewCopilotChatModel, and Check.
//
// Returns an error only for 401/403 from validateFineGrainedPAT. Transient
// errors (5xx/network) are logged (when logger is non-nil) and swallowed —
// the caller proceeds and the first Copilot API call will surface the real error.
func resolveDirectBearer(ctx context.Context, pat, enterpriseURL, explicitBase string, timeout time.Duration, logger *logrus.Entry) (*directBearerResolution, error) {
	login, err := validateFineGrainedPAT(ctx, pat, enterpriseURL, timeout, logger)
	if err != nil {
		return nil, err
	}
	baseURL := explicitBase
	if baseURL == "" {
		baseURL = ResolveBaseURL(enterpriseURL)
	}
	return &directBearerResolution{token: pat, baseURL: baseURL, login: login}, nil
}

// ResolvedToken is the result of resolving a GitHub token for a Copilot
// bearer token, including the plan-correct API base URL to use for all
// subsequent Copilot API calls (models, chat/completions, responses).
//
// In direct-bearer mode (fine-grained PAT, github_pat_...), Token holds the
// PAT itself, ExpiresAt is 0 (no exchange → no expiry), and Kind is
// TokenKindFineGrainedPAT.
type ResolvedToken struct {
	Token     string
	BaseURL   string
	ExpiresAt int64
	Plan      Plan
	Kind      TokenKind
}

// ResolveCopilotToken resolves a raw GitHub token into a Copilot bearer
// token and the plan-correct API base URL matching the token's actual Copilot
// plan (individual/business/enterprise). Callers that only need a one-off API
// call (e.g. a pre-flight ListModels check) don't have to guess the host or
// construct a full CopilotModel.
//
// For fine-grained PATs (github_pat_...), the PAT is returned directly as
// Token (direct-bearer mode) with ExpiresAt == 0 and Kind ==
// TokenKindFineGrainedPAT. No token exchange is performed — the
// /copilot_internal/v2/token endpoint 403s for these tokens. The PAT is
// validated via GET /copilot_internal/user (best-effort; 401/403 fail fast).
//
// For classic PATs (ghp_...) and unknown prefixes, the existing exchange path
// is used (unchanged).
//
// Precedence of the returned BaseURL:
//  1. explicit baseURL, when non-empty (caller override — e.g. a proxy/gateway);
//  2. ResolveBaseURL(enterpriseURL) fallback (direct-bearer mode);
//    OR endpoints.api from the token exchange response (exchange mode);
//  3. ResolveBaseURL(enterpriseURL) fallback (exchange mode).
//
// When githubToken is empty the function returns an error: it does not read
// environment variables. Callers wanting env-var discovery should populate
// githubToken from os.Getenv("GITHUB_TOKEN") first, as NewCopilotChatModel does.
func ResolveCopilotToken(ctx context.Context, githubToken, enterpriseURL, baseURL string, timeout time.Duration) (*ResolvedToken, error) {
	if githubToken == "" {
		return nil, errors.New("copilot: githubToken must not be empty")
	}

	kind := DetectTokenKind(githubToken)

	if kind == TokenKindFineGrainedPAT {
		res, err := resolveDirectBearer(ctx, githubToken, enterpriseURL, baseURL, timeout, nil)
		if err != nil {
			return nil, errors.Wrap(err, "copilot: PAT validation failed")
		}
		return &ResolvedToken{
			Token:     res.token,
			BaseURL:   res.baseURL,
			ExpiresAt: 0,
			Plan:      DetectPlan(res.baseURL),
			Kind:      kind,
		}, nil
	}

	tokenResp, err := exchangeGitHubToken(ctx, githubToken, enterpriseURL, timeout)
	if err != nil {
		return nil, errors.Wrap(err, "copilot: token exchange failed")
	}
	resolved := baseURL
	if resolved == "" {
		if tokenResp.Endpoints != nil && tokenResp.Endpoints.API != "" {
			resolved = tokenResp.Endpoints.API
		} else {
			resolved = ResolveBaseURL(enterpriseURL)
		}
	}
	return &ResolvedToken{
		Token:     tokenResp.Token,
		BaseURL:   resolved,
		ExpiresAt: tokenResp.ExpiresAt,
		Plan:      DetectPlan(resolved),
		Kind:      kind,
	}, nil
}

func cryptoRandIntn(n int) int {
	if n <= 0 {
		return 0
	}
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return n / 2
	}
	return int(binary.LittleEndian.Uint64(b[:]) % uint64(n))
}

type copilotLockedToken struct {
	mu    sync.RWMutex
	token string
}

func (t *copilotLockedToken) get() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.token
}

func (t *copilotLockedToken) set(token string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.token = token
}

func exchangeError(statusCode int, apiBase string) error {
	switch statusCode {
	case 401:
		return errors.Errorf("copilot: token exchange returned 401 Unauthorized — the GitHub token is invalid, expired, or lacks the required scope. Note: fine-grained PATs (github_pat_...) are not exchanged; they are used directly as the bearer token. If you are using a fine-grained PAT, ensure it is passed as GitHubToken (the provider auto-detects the prefix).")
	case 403:
		return errors.Errorf("copilot: token exchange returned 403 Forbidden — the account has no Copilot access via token exchange, or the token is a fine-grained PAT (github_pat_...) which GitHub does not permit on /copilot_internal/v2/token. Fine-grained PATs are used directly as the bearer token (direct-bearer mode); ensure the provider detects the github_pat_ prefix. For classic/enterprise exchange, verify Copilot is enabled and the PAT has the required scope.")
	case 404:
		return errors.Errorf("copilot: token exchange returned 404 Not Found at %s — check EnterpriseURL", apiBase)
	case 421:
		return errors.Errorf("copilot: token exchange returned 421 Misdirected Request — wrong API host")
	case 429:
		return errors.Errorf("copilot: token exchange returned 429 Too Many Requests — rate limited, retry later")
	default:
		if statusCode >= 500 {
			return errors.Errorf("copilot: token exchange returned status %d (upstream error)", statusCode)
		}
		return errors.Errorf("copilot: token exchange returned status %d", statusCode)
	}
}
