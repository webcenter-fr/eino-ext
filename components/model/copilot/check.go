package copilot

import (
	"context"
	"fmt"
	"time"

	"emperror.dev/errors"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/checkup"
)

const copilotCheckTimeout = 10 * time.Second

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

	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = ResolveBaseURL(cfg.EnterpriseURL)
	}

	var results checkup.Results

	if cfg.CopilotToken != "" {
		results = append(results, probeTokenExchangeSkipped())
	} else {
		results = append(results, probeTokenExchange(ctx, cfg.GitHubToken, cfg.EnterpriseURL, timeout))
	}

	results = append(results, probeModels(ctx, baseURL, cfg, timeout))

	return results
}

func probeTokenExchangeSkipped() checkup.Result {
	return checkup.Result{
		Component: "copilot_token_exchange",
		Status:    checkup.StatusOK,
		Message:   "using pre-obtained Copilot token, token exchange skipped",
	}
}

func probeTokenExchange(ctx context.Context, gitHubToken, enterpriseURL string, timeout time.Duration) checkup.Result {
	resp, err := exchangeGitHubToken(ctx, gitHubToken, enterpriseURL, timeout)
	if err != nil {
		return checkup.Result{
			Component: "copilot_token_exchange",
			Status:    checkup.StatusError,
			Error:     errors.Wrap(err, "failed to exchange GitHub token").Error(),
		}
	}
	return checkup.Result{
		Component: "copilot_token_exchange",
		Status:    checkup.StatusOK,
		Message:   fmt.Sprintf("token obtained, expires at %d", resp.ExpiresAt),
	}
}

func probeModels(ctx context.Context, baseURL string, cfg *Config, timeout time.Duration) checkup.Result {
	token := cfg.CopilotToken
	if token == "" {
		resp, err := exchangeGitHubToken(ctx, cfg.GitHubToken, cfg.EnterpriseURL, timeout)
		if err != nil {
			return checkup.Result{
				Component: "copilot_models",
				Status:    checkup.StatusError,
				Error:     "dependency failed: token exchange required for /models probe",
			}
		}
		token = resp.Token
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
