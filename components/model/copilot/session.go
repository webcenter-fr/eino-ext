package copilot

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"emperror.dev/errors"
	"github.com/sirupsen/logrus"
)

const sessionPath = "/models/session"

// sessionRequestBody is POSTed to /models/session to acquire a model session token.
type sessionRequestBody struct {
	AutoMode sessionAutoMode `json:"auto_mode"`
}

type sessionAutoMode struct {
	ModelHints []string `json:"model_hints"`
}

// sessionResponse is the response from POST /models/session.
type sessionResponse struct {
	SelectedModel   string   `json:"selected_model"`
	SessionToken    string   `json:"session_token"`
	ExpiresAt       int64    `json:"expires_at"`
	AvailableModels []string `json:"available_models"`
}

// copilotSessionToken manages a model session token with thread-safe access and
// background refresh.
type copilotSessionToken struct {
	mu        sync.RWMutex
	token     string
	expiresAt int64
	logger    *logrus.Entry
}

func (s *copilotSessionToken) get() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.token
}

// acquireSession sends a POST to /models/session with the full header set
// (§1.3 in the plan, minus copilot-session-token which we're acquiring).
func acquireSession(ctx context.Context, baseURL, copilotToken string, modelHint string, httpClient *http.Client, clientMachineID string) (*sessionResponse, error) {
	body := sessionRequestBody{
		AutoMode: sessionAutoMode{
			ModelHints: []string{modelHint},
		},
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, errors.Wrap(err, "copilot: failed to marshal session request")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+sessionPath, bytes.NewReader(payload))
	if err != nil {
		return nil, errors.Wrap(err, "copilot: failed to create session request")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+copilotToken)
	req.Header.Set("User-Agent", userAgentHeader)
	req.Header.Set("Openai-Intent", copilotOpenAIIntent)
	req.Header.Set("Copilot-Integration-Id", integrationID)
	req.Header.Set("Editor-Version", editorVersion)
	req.Header.Set("X-GitHub-Api-Version", copilotAPIVersion)
	req.Header.Set("X-Interaction-Id", newUUID())
	req.Header.Set("X-Client-Machine-Id", clientMachineID)
	req.Header.Set("X-Initiator", "user")
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "copilot: session request failed")
	}
	//nolint:errcheck // defer close in request path, error is irrelevant
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.Wrap(err, "copilot: failed to read session response body")
	}

	if resp.StatusCode != http.StatusOK {
		return nil, errors.Errorf("copilot: session request returned status %d: %s", resp.StatusCode, redactErrorBody(respBody))
	}

	var sresp sessionResponse
	if err := json.Unmarshal(respBody, &sresp); err != nil {
		return nil, errors.Wrapf(err, "copilot: failed to decode session response (body: %s)", redactErrorBody(respBody))
	}
	if sresp.SessionToken == "" {
		return nil, errors.New("copilot: session response returned empty session_token")
	}

	return &sresp, nil
}

// needsSessionToken reports whether a model requires a session token.
// GPT-5+ models (both /chat/completions and /responses endpoints) need one.
// Claude and Gemini models work via /chat/completions without a session token;
// the session endpoint does not currently return Claude/Gemini models in its
// available_models list.
func needsSessionToken(modelID string) bool {
	if modelID == "" {
		return false
	}
	if strings.HasPrefix(modelID, "gpt-5") {
		return true
	}
	return false
}

// startSessionRefresh launches a background goroutine that refreshes the session
// token before it expires, using the same pattern as startTokenRefresh.
func (m *CopilotModel) startSessionRefresh(ctx context.Context, sresp *sessionResponse, modelHint string) context.CancelFunc {
	ctx, cancel := context.WithCancel(ctx)

	if sresp == nil || sresp.SessionToken == "" || sresp.ExpiresAt <= 0 {
		if sresp != nil && sresp.ExpiresAt <= 0 {
			if m.logger != nil {
				m.logger.Warn("copilot: session token has no expiry (expires_at <= 0); background refresh will not be started")
			}
		}
		cancel()
		return cancel
	}

	go func() {
		currentExpiresAt := sresp.ExpiresAt
		var currentToken string

		for {
			sleepSecs := currentExpiresAt - time.Now().Unix() - refreshBufSecs
			if sleepSecs < refreshMinSecs {
				sleepSecs = refreshMinSecs
			}
			sleepDuration := time.Duration(sleepSecs) * time.Second

			select {
			case <-ctx.Done():
				return
			case <-time.After(sleepDuration):
			}

			copilotToken := m.lockedToken.get()
			newResp, err := acquireSession(ctx, m.baseURL, copilotToken, modelHint, m.httpClient, m.clientMachineID)
			if err == nil {
				m.sessionToken.mu.Lock()
				m.sessionToken.token = newResp.SessionToken
				m.sessionToken.expiresAt = newResp.ExpiresAt
				m.sessionToken.mu.Unlock()
				currentExpiresAt = newResp.ExpiresAt
				currentToken = newResp.SessionToken
				_ = currentToken
				continue
			}

			m.logger.Warnf("copilot: session token refresh failed (model=%s): %v", modelHint, err)

			backoffSecs := backoffInitialSecs
			for {
				jitter := time.Duration(cryptoRandIntn(backoffJitterSecs*2)-backoffJitterSecs) * time.Second
				select {
				case <-ctx.Done():
					return
				case <-time.After(time.Duration(backoffSecs)*time.Second + jitter):
				}

				copilotToken = m.lockedToken.get()
				newResp, err = acquireSession(ctx, m.baseURL, copilotToken, modelHint, m.httpClient, m.clientMachineID)
				if err == nil {
					m.sessionToken.mu.Lock()
					m.sessionToken.token = newResp.SessionToken
					m.sessionToken.expiresAt = newResp.ExpiresAt
					m.sessionToken.mu.Unlock()
					currentExpiresAt = newResp.ExpiresAt
					break
				}

				m.logger.Warnf("copilot: session token retry failed (model=%s): %v", modelHint, err)
				backoffSecs *= 2
				if backoffSecs > backoffMaxSecs {
					backoffSecs = backoffMaxSecs
				}
			}
		}
	}()

	return cancel
}

// ensureSessionToken makes sure a session token is available for the model.
// When the model does not need one or a pre-configured SessionToken exists, it
// is a no-op. Otherwise it acquires a session token and starts background
// refresh.
func (m *CopilotModel) ensureSessionToken(ctx context.Context, modelID string) error {
	if !needsSessionToken(modelID) {
		return nil
	}

	// sessionMu is nil when the model is constructed directly (e.g. tests).
	if m.sessionMu == nil {
		return nil
	}

	m.sessionToken.mu.RLock()
	hasToken := m.sessionToken.token != ""
	hasRefresh := m.cancelSessionRefresh != nil
	m.sessionToken.mu.RUnlock()

	if hasToken && hasRefresh {
		return nil
	}

	m.sessionMu.Lock()
	defer m.sessionMu.Unlock()

	m.sessionToken.mu.RLock()
	hasToken = m.sessionToken.token != ""
	m.sessionToken.mu.RUnlock()
	if hasToken {
		return nil
	}

	copilotToken := m.lockedToken.get()
	sresp, err := acquireSession(ctx, m.baseURL, copilotToken, modelID, m.httpClient, m.clientMachineID)
	if err != nil {
		return errors.Wrapf(err, "copilot: failed to acquire session token for model %q", modelID)
	}

	m.sessionToken.mu.Lock()
	m.sessionToken.token = sresp.SessionToken
	m.sessionToken.expiresAt = sresp.ExpiresAt
	m.sessionToken.mu.Unlock()

	m.cancelSessionRefresh = m.startSessionRefresh(ctx, sresp, modelID)

	return nil
}
