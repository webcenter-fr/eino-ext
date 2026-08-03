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
		return nil, errors.Errorf("copilot: token exchange returned status %d", resp.StatusCode)
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

	go func() {
		currentToken := tokenResp.Token
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

// ResolvedToken is the result of exchanging a GitHub token for a Copilot
// bearer token, including the plan-correct API base URL to use for all
// subsequent Copilot API calls (models, chat/completions, responses).
type ResolvedToken struct {
	Token     string
	BaseURL   string
	ExpiresAt int64
}

// ResolveCopilotToken exchanges a raw GitHub token for a short-lived Copilot
// bearer token and the API base URL matching the token's actual Copilot plan
// (individual/business/enterprise), so callers that only need a one-off API
// call (e.g. a pre-flight ListModels check) don't have to guess the host or
// construct a full CopilotModel.
//
// Precedence of the returned BaseURL (mirrors NewCopilotChatModel):
//  1. explicit baseURL, when non-empty (caller override — e.g. a proxy/gateway);
//  2. endpoints.api from the token exchange response (plan-correct host);
//  3. ResolveBaseURL(enterpriseURL) fallback.
//
// When githubToken is empty the function returns an error: it does not read
// environment variables. Callers wanting env-var discovery should populate
// githubToken from os.Getenv("GITHUB_TOKEN") first, as NewCopilotChatModel does.
func ResolveCopilotToken(ctx context.Context, githubToken, enterpriseURL, baseURL string, timeout time.Duration) (*ResolvedToken, error) {
	if githubToken == "" {
		return nil, errors.New("copilot: githubToken must not be empty")
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
