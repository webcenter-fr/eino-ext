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

// autoModelResolution holds the result of an auto-mode /models/session call:
// the API-selected model ID and the session token that authorizes its use.
// It is guarded by autoMu on the owning CopilotModel.
type autoModelResolution struct {
	selectedModel string
	sessionToken  string
	expiresAt     int64
}

// acquireAutoSession calls POST /models/session with empty model_hints so
// the Copilot API selects a model automatically from the caller's available
// catalog. It returns the full sessionResponse (selected_model +
// session_token + expires_at + available_models).
//
// If the API rejects empty hints, switch the body to
// `{"auto_mode":{"model_hints":["auto"]}}` (see plan assumption #1).
func acquireAutoSession(ctx context.Context, baseURL, copilotToken string, httpClient *http.Client, clientMachineID string) (*sessionResponse, error) {
	return doSessionRequest(ctx, baseURL, copilotToken, []string{}, true, httpClient, clientMachineID)
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
	return doSessionRequest(ctx, baseURL, copilotToken, []string{modelHint}, false, httpClient, clientMachineID)
}

// doSessionRequest sends POST /models/session with the given model hints and
// returns the session response. When validateSelectedModel is true, an empty
// selected_model in the response is treated as an error.
func doSessionRequest(ctx context.Context, baseURL, copilotToken string, modelHints []string, validateSelectedModel bool, httpClient *http.Client, clientMachineID string) (*sessionResponse, error) {
	body := sessionRequestBody{
		AutoMode: sessionAutoMode{
			ModelHints: modelHints,
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
	if validateSelectedModel && sresp.SelectedModel == "" {
		return nil, errors.New("copilot: session response returned empty selected_model")
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
			m.logger.Warn("copilot: session token has no expiry (expires_at <= 0); background refresh will not be started")
		}
		cancel()
		return cancel
	}

	go func() {
		currentExpiresAt := sresp.ExpiresAt
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

// ensureAutoModel resolves the "auto" model sentinel to a concrete model ID
// via POST /models/session (empty model_hints). It caches the result
// (selected_model + session_token + expires_at) on the model and starts a
// background refresh goroutine. It returns the resolved model ID.
//
// It is a no-op fast path when a cached, unexpired resolution already exists.
// Concurrent calls are serialized via autoMu; the first call performs the
// network round-trip, subsequent calls reuse the cache.
func (m *CopilotModel) ensureAutoModel(ctx context.Context) (string, error) {
	if m.autoMu == nil {
		return "", errors.New("copilot: auto model resolution not initialized (nil autoMu)")
	}

	// Fast path: cached and not near expiry.
	if modelID, ok := m.cachedAutoModel(); ok {
		return modelID, nil
	}

	m.autoMu.Lock()
	defer m.autoMu.Unlock()

	// Re-check after acquiring the lock (double-checked locking).
	if modelID, ok := m.cachedAutoModelLocked(); ok {
		return modelID, nil
	}

	copilotToken := m.lockedToken.get()
	sresp, err := acquireAutoSession(ctx, m.baseURL, copilotToken, m.httpClient, m.clientMachineID)
	if err != nil {
		return "", errors.Wrap(err, "copilot: failed to resolve auto model")
	}

	// Cancel any existing refresh goroutine before starting a new one.
	// Without this, repeated ensureAutoModel calls (e.g. cache expiry
	// followed by a fresh resolution) leak background goroutines.
	if m.cancelAutoRefresh != nil {
		m.cancelAutoRefresh()
	}
	m.applyAutoSession(sresp)
	m.cancelAutoRefresh = m.startAutoModelRefresh(ctx, sresp)
	return sresp.SelectedModel, nil
}

// cachedAutoModel returns the cached selected_model and ok=true when an
// auto resolution exists and is not near expiry. Safe for concurrent use.
func (m *CopilotModel) cachedAutoModel() (string, bool) {
	m.autoMu.Lock()
	defer m.autoMu.Unlock()
	return m.cachedAutoModelLocked()
}

// cachedAutoModelLocked is the lock-held fast path. Caller must hold autoMu.
func (m *CopilotModel) cachedAutoModelLocked() (string, bool) {
	if m.autoModel == nil || m.autoModel.selectedModel == "" {
		return "", false
	}
	if m.autoModel.expiresAt <= 0 {
		return m.autoModel.selectedModel, true // no expiry known → trust until refreshed
	}
	if time.Now().Unix()+refreshBufSecs >= m.autoModel.expiresAt {
		return "", false // near expiry → re-resolve
	}
	return m.autoModel.selectedModel, true
}

// applyAutoSession stores the auto session result and propagates the session
// token to the shared sessionToken field so setCommonRequestHeaders sends it
// on the chat/responses call. Caller must hold autoMu.
func (m *CopilotModel) applyAutoSession(sresp *sessionResponse) {
	m.autoModel = &autoModelResolution{
		selectedModel: sresp.SelectedModel,
		sessionToken:  sresp.SessionToken,
		expiresAt:     sresp.ExpiresAt,
	}
	// Propagate to the shared session-token field so the existing
	// Copilot-Session-Token header logic picks it up unchanged.
	if m.sessionToken != nil {
		m.sessionToken.mu.Lock()
		m.sessionToken.token = sresp.SessionToken
		m.sessionToken.expiresAt = sresp.ExpiresAt
		m.sessionToken.mu.Unlock()
	}
}

// startAutoModelRefresh launches a background goroutine that refreshes the
// auto model resolution (selected_model + session_token) before it expires,
// mirroring startSessionRefresh. Returns a cancel func.
func (m *CopilotModel) startAutoModelRefresh(ctx context.Context, sresp *sessionResponse) context.CancelFunc {
	ctx, cancel := context.WithCancel(ctx)
	if sresp == nil || sresp.SessionToken == "" || sresp.ExpiresAt <= 0 {
		cancel()
		return cancel
	}
	go func() {
		currentExpiresAt := sresp.ExpiresAt
		for {
			sleepSecs := currentExpiresAt - time.Now().Unix() - refreshBufSecs
			if sleepSecs < refreshMinSecs {
				sleepSecs = refreshMinSecs
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Duration(sleepSecs) * time.Second):
			}
			copilotToken := m.lockedToken.get()
			newResp, err := acquireAutoSession(ctx, m.baseURL, copilotToken, m.httpClient, m.clientMachineID)
			if err == nil {
				m.autoMu.Lock()
				m.applyAutoSession(newResp)
				m.autoMu.Unlock()
				currentExpiresAt = newResp.ExpiresAt
				continue
			}
			m.logger.Warnf("copilot: auto model refresh failed: %v", err)
			backoffSecs := backoffInitialSecs
			for {
				jitter := time.Duration(cryptoRandIntn(backoffJitterSecs*2)-backoffJitterSecs) * time.Second
				select {
				case <-ctx.Done():
					return
				case <-time.After(time.Duration(backoffSecs)*time.Second + jitter):
				}
				copilotToken = m.lockedToken.get()
				newResp, err = acquireAutoSession(ctx, m.baseURL, copilotToken, m.httpClient, m.clientMachineID)
				if err == nil {
					m.autoMu.Lock()
					m.applyAutoSession(newResp)
					m.autoMu.Unlock()
					currentExpiresAt = newResp.ExpiresAt
					break
				}
				m.logger.Warnf("copilot: auto model refresh retry failed: %v", err)
				backoffSecs *= 2
				if backoffSecs > backoffMaxSecs {
					backoffSecs = backoffMaxSecs
				}
			}
		}
	}()
	return cancel
}

// ResolvedAutoModel returns the currently cached auto-resolved model ID and
// ok=true, or ""/false when auto mode is not in use or not yet resolved.
// Exposed for diagnostics and tests.
func (m *CopilotModel) ResolvedAutoModel() (string, bool) {
	if m.autoMu == nil {
		return "", false
	}
	m.autoMu.Lock()
	defer m.autoMu.Unlock()
	if m.autoModel == nil {
		return "", false
	}
	return m.autoModel.selectedModel, true
}
