package copilot

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"emperror.dev/errors"
	"github.com/sirupsen/logrus"
)

const userURLPath = "/copilot_internal/user"

// copilotUserResponse is the (partial) shape of GET /copilot_internal/user.
// Only `login` is known to be present; other fields are parsed best-effort.
type copilotUserResponse struct {
	Login string `json:"login"`
}

// resolveUserAPIBase returns the GitHub API base URL for user validation.
// It respects enterpriseURL when set; otherwise uses defaultAPIBase.
// Tests may override the default via setTestUserAPIBaseForTesting (in _test.go).
func resolveUserAPIBase(enterpriseURL string) string {
	if testUserAPIBase != "" {
		return testUserAPIBase
	}
	if enterpriseURL != "" {
		return fmt.Sprintf("https://api.%s", enterpriseURL)
	}
	return defaultAPIBase
}

// testUserAPIBase allows tests to override the GitHub API base URL for
// validateFineGrainedPAT. Set only in _test.go files via
// setTestUserAPIBaseForTesting. When empty, defaultAPIBase is used.
// This is NOT exported — it is a test-only hook.
var testUserAPIBase string

// validateFineGrainedPAT validates a fine-grained PAT against
// GET /copilot_internal/user (Bearer PAT). Returns:
//   - 200: nil (PAT is valid); `login` is returned for logging.
//   - 401: error "invalid/expired PAT".
//   - 403: error "PAT lacks Copilot Requests permission or account has no Copilot".
//   - other (5xx/network): logged via `logger` (when non-nil) and returns nil
//     (best-effort — do not block direct-bearer mode on transient upstream errors;
//     the first Copilot API call will surface the real error).
//
// Never includes the PAT in any returned error string.
func validateFineGrainedPAT(ctx context.Context, pat, enterpriseURL string, timeout time.Duration, logger *logrus.Entry) (login string, err error) {
	return validateFineGrainedPATToBase(ctx, pat, resolveUserAPIBase(enterpriseURL), timeout, logger)
}

// validateFineGrainedPATToBase performs the I/O of validateFineGrainedPAT
// against a caller-supplied apiBase (e.g. an httptest URL in tests).
func validateFineGrainedPATToBase(ctx context.Context, pat, apiBase string, timeout time.Duration, logger *logrus.Entry) (login string, err error) {
	url := apiBase + userURLPath
	req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if reqErr != nil {
		if logger != nil {
			logger.Warnf("copilot: failed to create /copilot_internal/user request: %v", reqErr)
		}
		return "", nil
	}
	req.Header.Set("Authorization", "Bearer "+pat)
	req.Header.Set("User-Agent", userAgentHeader)
	req.Header.Set("X-GitHub-Api-Version", gitHubAPIVersion)
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: timeout}
	resp, doErr := client.Do(req)
	if doErr != nil {
		if logger != nil {
			logger.Warnf("copilot: /copilot_internal/user request failed: %v", doErr)
		}
		return "", nil
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
		var userResp copilotUserResponse
		if decodeErr := json.NewDecoder(resp.Body).Decode(&userResp); decodeErr != nil {
			if logger != nil {
				logger.Warnf("copilot: failed to decode /copilot_internal/user response: %v", decodeErr)
			}
			return "", nil
		}
		if logger != nil {
			logger.Infof("copilot: validated fine-grained PAT (login=%s)", userResp.Login)
		}
		return userResp.Login, nil
	case 401:
		return "", errors.New("copilot: PAT validation returned 401 Unauthorized — the fine-grained PAT is invalid or expired")
	case 403:
		return "", errors.New("copilot: PAT validation returned 403 Forbidden — the PAT lacks Copilot Requests account permission (Read) or the account has no Copilot access")
	default:
		if logger != nil {
			logger.Warnf("copilot: /copilot_internal/user returned unexpected status %d", resp.StatusCode)
		}
		return "", nil
	}
}
