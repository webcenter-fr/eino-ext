package copilot

import (
	"context"
	"fmt"
	"time"

	"emperror.dev/errors"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/checkup"
)

const copilotCheckTimeout = 10 * time.Second

// Check performs a health check for the Copilot model by exchanging the
// configured credentials for a model session token and verifying the auth
// user identity.
func Check(ctx context.Context, cfg *Config) checkup.Results {
	if cfg == nil {
		return checkup.Results{{
			Component: "copilot",
			Status:    checkup.StatusError,
			Error:     "config must not be nil",
		}}
	}

	if cfg.GitHubToken == "" && cfg.CopilotToken == "" {
		return checkup.Results{{
			Component: "copilot",
			Status:    checkup.StatusError,
			Error:     "at least one of GitHubToken or CopilotToken must be set",
		}}
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}

	var results checkup.Results
	var resolvedBase string
	var resolvedToken string

	if cfg.CopilotToken != "" {
		results = append(results, probeTokenExchangeSkipped())
		resolvedBase = cfg.BaseURL
		if resolvedBase == "" {
			resolvedBase = ResolveBaseURL(cfg.EnterpriseURL)
		}
	} else {
		kind := DetectTokenKind(cfg.GitHubToken)
		if kind == TokenKindFineGrainedPAT {
			res, err := resolveDirectBearer(ctx, cfg.GitHubToken, cfg.EnterpriseURL, cfg.BaseURL, timeout, nil)
			if err != nil {
				results = append(results, checkup.Result{
					Component: "copilot_token_exchange",
					Status:    checkup.StatusError,
					Error:     err.Error(),
				})
				results = append(results, checkup.Result{
					Component: "copilot_models",
					Status:    checkup.StatusError,
					Error:     "dependency failed: PAT validation required for /models probe",
				})
				return results
			}
			msg := "direct-bearer mode (fine-grained PAT); no token exchange"
			if res.login != "" {
				msg = fmt.Sprintf("direct-bearer mode (fine-grained PAT, login=%s); no token exchange", res.login)
			}
			results = append(results, checkup.Result{
				Component: "copilot_token_exchange",
				Status:    checkup.StatusOK,
				Message:   msg,
			})
			resolvedToken = res.token
			resolvedBase = res.baseURL
		} else {
			resolved, err := ResolveCopilotToken(ctx, cfg.GitHubToken, cfg.EnterpriseURL, cfg.BaseURL, timeout)
			if err != nil {
				results = append(results, checkup.Result{
					Component: "copilot_token_exchange",
					Status:    checkup.StatusError,
					Error:     err.Error(),
				})
				results = append(results, checkup.Result{
					Component: "copilot_models",
					Status:    checkup.StatusError,
					Error:     "dependency failed: token exchange required for /models probe",
				})
				return results
			}
			results = append(results, checkup.Result{
				Component: "copilot_token_exchange",
				Status:    checkup.StatusOK,
				Message:   fmt.Sprintf("token obtained, expires at %d", resolved.ExpiresAt),
			})
			resolvedToken = resolved.Token
			resolvedBase = resolved.BaseURL
		}
	}

	results = append(results, probeModels(ctx, resolvedBase, resolvedToken, cfg))
	return results
}

func probeTokenExchangeSkipped() checkup.Result {
	return checkup.Result{
		Component: "copilot_token_exchange",
		Status:    checkup.StatusOK,
		Message:   "using pre-obtained Copilot token, token exchange skipped",
	}
}

func probeModels(ctx context.Context, baseURL, token string, cfg *Config) checkup.Result {
	if token == "" {
		token = cfg.CopilotToken
	}
	if token == "" {
		return checkup.Result{
			Component: "copilot_models",
			Status:    checkup.StatusError,
			Error:     "dependency failed: no token available for /models probe",
		}
	}

	models, err := ListModels(ctx, token, baseURL, copilotCheckTimeout)
	if err != nil {
		return checkup.Result{
			Component: "copilot_models",
			Status:    checkup.StatusError,
			Error:     errors.Wrap(err, "failed to list models").Error(),
		}
	}

	if len(models) == 0 {
		return checkup.Result{
			Component: "copilot_models",
			Status:    checkup.StatusLimited,
			Message:   "GET /models returned 200 but no models found",
		}
	}

	return checkup.Result{
		Component: "copilot_models",
		Status:    checkup.StatusOK,
		Message:   fmt.Sprintf("%d models available", len(models)),
	}
}
