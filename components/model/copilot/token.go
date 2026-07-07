package copilot

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"net/http"
	"sync"
	"time"

	"emperror.dev/errors"
)

const (
	tokenURLPath       = "/copilot_internal/v2/token"
	defaultAPIBase     = "https://api.github.com"
	defaultCopilotBase = "https://api.githubcopilot.com"

	userAgentHeader = "GitHubCopilotChat/0.52.0"
	apiVersion      = "2025-04-01"

	refreshBufSecs     = 60
	refreshMinSecs     = 1
	backoffInitialSecs = 15
	backoffMaxSecs     = 600
	backoffJitterSecs  = 15

	defaultTimeout = 60 * time.Minute
)

type copilotTokenResponse struct {
	Token     string `json:"token"`
	ExpiresAt int64  `json:"expires_at"`
	RefreshIn int    `json:"refresh_in"`
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
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
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

			newResp, err := exchangeGitHubToken(context.Background(), cfg.GitHubToken, cfg.EnterpriseURL, cfg.Timeout)
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
				jitter := time.Duration(rand.Intn(backoffJitterSecs*2)-backoffJitterSecs) * time.Second
				select {
				case <-ctx.Done():
					return
				case <-time.After(time.Duration(backoffSecs)*time.Second + jitter):
				}

				newResp, err = exchangeGitHubToken(context.Background(), cfg.GitHubToken, cfg.EnterpriseURL, cfg.Timeout)
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

func resolveBaseURL(enterpriseURL string) string {
	if enterpriseURL != "" {
		return fmt.Sprintf("https://copilot-api.%s", enterpriseURL)
	}
	return defaultCopilotBase
}

func insecureHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
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
