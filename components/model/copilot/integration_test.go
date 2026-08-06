//go:build integration

package copilot

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/checkup"
)

func requireIntegration(t *testing.T) {
	t.Helper()
	if os.Getenv("COPILOT_INTEGRATION") != "1" {
		t.Skip("COPILOT_INTEGRATION=1 not set")
	}
	if os.Getenv("GITHUB_COPILOT_TOKEN") == "" && os.Getenv("GITHUB_TOKEN") == "" {
		t.Skip("neither GITHUB_COPILOT_TOKEN nor GITHUB_TOKEN set")
	}
	if testing.Short() {
		t.Skip("skipping integration tests in short mode")
	}
}

func newIntegrationModel(t *testing.T, modelID string) *CopilotModel {
	t.Helper()
	ctx := context.Background()
	cfg := &Config{
		CopilotToken: os.Getenv("GITHUB_COPILOT_TOKEN"),
		GitHubToken:  os.Getenv("GITHUB_TOKEN"),
		Model:        modelID,
	}
	if url := os.Getenv("COPILOT_API_URL"); url != "" {
		cfg.BaseURL = url
	}
	m, err := NewCopilotChatModel(ctx, cfg)
	if err != nil {
		t.Fatalf("NewCopilotChatModel: %v", err)
	}
	return m
}

func parseFamily(modelID string) string {
	lower := strings.ToLower(modelID)
	switch {
	case strings.HasPrefix(lower, "gpt-5"):
		return "reasoning-gpt5"
	case strings.HasPrefix(lower, "claude-"):
		return "claude"
	case strings.HasPrefix(lower, "gemini-"):
		return "gemini"
	case strings.HasPrefix(lower, "gpt-4") || strings.HasPrefix(lower, "gpt-3"):
		return "standard"
	default:
		return "other"
	}
}

func getTestTemperatures(family string) []struct {
	temp    *float32
	wantErr bool
	errMsg  string
} {
	switch family {
	case "reasoning-gpt5":
		return []struct {
			temp    *float32
			wantErr bool
			errMsg  string
		}{
			{temp: nil, wantErr: false},
			{temp: float32Ptr(1.0), wantErr: false},
			{temp: float32Ptr(0.0), wantErr: true, errMsg: "temperature"},
			{temp: float32Ptr(2.0), wantErr: true, errMsg: "temperature"},
		}
	case "claude":
		return []struct {
			temp    *float32
			wantErr bool
			errMsg  string
		}{
			{temp: float32Ptr(0.0), wantErr: false},
			{temp: float32Ptr(0.5), wantErr: false},
			{temp: float32Ptr(1.0), wantErr: false},
		}
	case "standard":
		return []struct {
			temp    *float32
			wantErr bool
			errMsg  string
		}{
			{temp: nil, wantErr: false},
			{temp: float32Ptr(0.0), wantErr: false},
			{temp: float32Ptr(0.5), wantErr: false},
			{temp: float32Ptr(1.0), wantErr: false},
			{temp: float32Ptr(2.0), wantErr: false},
		}
	default:
		return []struct {
			temp    *float32
			wantErr bool
			errMsg  string
		}{
			{temp: nil, wantErr: false},
			{temp: float32Ptr(1.0), wantErr: false},
		}
	}
}

func float32Ptr(v float32) *float32 { return &v }

// requireGitHubTokenIntegration skips unless a raw GitHub PAT is available
// to exercise the /copilot_internal/v2/token exchange. The acceptance tests
// below depend on the exchange response's endpoints.api field, which only
// exists when a real exchange happens — a pre-obtained CopilotToken skips
// the exchange entirely (NewCopilotChatModel's CopilotToken branch).
//
// Fine-grained PATs (github_pat_...) use direct-bearer mode and are NEVER
// exchanged — this gate skips when the token is a fine-grained PAT, since
// the exchange would 403.
func requireGitHubTokenIntegration(t *testing.T) {
	t.Helper()
	requireIntegration(t)
	if os.Getenv("GITHUB_TOKEN") == "" {
		t.Skip("GITHUB_TOKEN (raw PAT) not set — acceptance tests need the exchange path")
	}
	if strings.HasPrefix(os.Getenv("GITHUB_TOKEN"), "github_pat_") {
		t.Skip("GITHUB_TOKEN is a fine-grained PAT (github_pat_...) — these tokens use direct-bearer mode, not exchange. Classic acceptance tests require a ghp_/gho_ token.")
	}
}

// exchangeForTest performs a real token exchange for the test's GitHub PAT
// and returns the full response (including endpoints.api), as ground truth.
func exchangeForTest(t *testing.T) *copilotTokenResponse {
	t.Helper()
	resp, err := exchangeGitHubToken(context.Background(), os.Getenv("GITHUB_TOKEN"), "", 30*time.Second)
	if err != nil {
		t.Fatalf("exchangeGitHubToken: %v", err)
	}
	if resp.Endpoints == nil || resp.Endpoints.API == "" {
		t.Fatalf("exchange returned no endpoints.api — cannot validate plan-correct host detection; got %+v", resp)
	}
	return resp
}

// TestIntegration_AutoDetectBaseURL proves the 421 fix: when a raw GitHub
// PAT is used with no explicit BaseURL, NewCopilotChatModel must resolve the
// plan-correct host from the exchange response's endpoints.api and a real
// Generate must succeed (not 421).
func TestIntegration_AutoDetectBaseURL(t *testing.T) {
	requireGitHubTokenIntegration(t)

	truth := exchangeForTest(t)
	t.Logf("plan-correct host (endpoints.api): %s", truth.Endpoints.API)

	ctx := context.Background()
	m, err := NewCopilotChatModel(ctx, &Config{
		GitHubToken: os.Getenv("GITHUB_TOKEN"),
		Model:       "gpt-4o",
	})
	if err != nil {
		t.Fatalf("NewCopilotChatModel: %v", err)
	}
	if m.cancelRefresh != nil {
		defer m.cancelRefresh()
	}

	if m.baseURL != truth.Endpoints.API {
		t.Fatalf("baseURL not auto-detected: got %q, want endpoints.api %q (hardcoded default is %q)",
			m.baseURL, truth.Endpoints.API, defaultCopilotBase)
	}
	if m.baseURL == defaultCopilotBase && truth.Endpoints.API != defaultCopilotBase {
		t.Fatalf("regression: model fell back to the individual-only host %q instead of %q",
			m.baseURL, truth.Endpoints.API)
	}

	msg, err := m.Generate(ctx, []*schema.Message{
		{Role: schema.User, Content: "Reply with the single word: ok"},
	}, model.WithMaxTokens(10))
	if err != nil {
		t.Fatalf("Generate against auto-detected host failed (expected 200, got: %v)", err)
	}
	if msg == nil || msg.Content == "" {
		t.Fatal("expected non-empty Generate content")
	}
	t.Logf("Generate OK on %s: %q", m.baseURL, truncate(msg.Content, 40))

	if truth.Endpoints.API != defaultCopilotBase {
		_, err := ListModels(context.Background(), truth.Token, defaultCopilotBase, 30*time.Second)
		if err == nil {
			t.Fatalf("negative control failed: ListModels against the WRONG host %q unexpectedly succeeded (expected 421); the fix may be masking a real routing problem", defaultCopilotBase)
		}
		if !strings.Contains(err.Error(), "421") {
			t.Errorf("negative control: expected a 421 error against %q, got: %v", defaultCopilotBase, err)
		} else {
			t.Logf("negative control confirmed: same token against %q -> 421 (proves the auto-detection is what avoids the 421)", defaultCopilotBase)
		}
	} else {
		t.Logf("individual plan detected (endpoints.api == default %q): 421 negative control not applicable; auto-detection still asserted via field equality", defaultCopilotBase)
	}
}

// TestIntegration_ResolveCopilotToken proves ResolveCopilotToken returns a
// usable (token, plan-correct host) pair against the real API.
func TestIntegration_ResolveCopilotToken(t *testing.T) {
	requireGitHubTokenIntegration(t)
	ctx := context.Background()

	truth := exchangeForTest(t)

	resolved, err := ResolveCopilotToken(ctx, os.Getenv("GITHUB_TOKEN"), "", "", 30*time.Second)
	if err != nil {
		t.Fatalf("ResolveCopilotToken: %v", err)
	}
	if resolved.Token == "" {
		t.Fatal("resolved.Token is empty")
	}
	if resolved.BaseURL != truth.Endpoints.API {
		t.Fatalf("resolved.BaseURL = %q, want endpoints.api %q", resolved.BaseURL, truth.Endpoints.API)
	}
	if resolved.ExpiresAt <= time.Now().Unix() {
		t.Errorf("resolved.ExpiresAt %d not in the future", resolved.ExpiresAt)
	}

	models, err := ListModels(ctx, resolved.Token, resolved.BaseURL, 30*time.Second)
	if err != nil {
		t.Fatalf("ListModels with resolved pair failed (expected 200): %v", err)
	}
	if len(models) < 20 {
		t.Errorf("expected ≥20 models from resolved pair, got %d", len(models))
	}
	t.Logf("ResolveCopilotToken -> %s, %d models", resolved.BaseURL, len(models))

	const override = "https://copilot-override.example.invalid"
	resolved2, err := ResolveCopilotToken(ctx, os.Getenv("GITHUB_TOKEN"), "", override, 30*time.Second)
	if err != nil {
		t.Fatalf("ResolveCopilotToken with override: %v", err)
	}
	if resolved2.BaseURL != override {
		t.Fatalf("override lost: resolved2.BaseURL = %q, want explicit %q", resolved2.BaseURL, override)
	}
}

// TestIntegration_Check_GitHubToken proves copilot.Check uses the
// plan-correct host (from endpoints.api) when BaseURL is unset, so the
// /models probe reports OK instead of error/421 for non-individual plans.
func TestIntegration_Check_GitHubToken(t *testing.T) {
	requireGitHubTokenIntegration(t)
	ctx := context.Background()

	results := Check(ctx, &Config{GitHubToken: os.Getenv("GITHUB_TOKEN")})
	for _, r := range results {
		t.Logf("check: %s = %s (%s)", r.Component, r.Status, r.Message)
	}

	var exchange, models *checkup.Result
	for i := range results {
		switch results[i].Component {
		case "copilot_token_exchange":
			exchange = &results[i]
		case "copilot_models":
			models = &results[i]
		}
	}
	if exchange == nil || models == nil {
		t.Fatalf("expected copilot_token_exchange and copilot_models results, got %+v", results)
	}
	if exchange.Status != checkup.StatusOK {
		t.Errorf("copilot_token_exchange = %s, want OK", exchange.Status)
	}
	if models.Status == checkup.StatusError {
		t.Errorf("copilot_models = error (expected OK): %s — checkup is likely still using the wrong host", models.Error)
	}
	if models.Status != checkup.StatusOK {
		t.Errorf("copilot_models = %s, want OK", models.Status)
	}
}


// TestIntegration_ListModels verifies that ListModels returns ≥20 models
// including gpt-5-mini and at least one Claude model, proving the
// integration-id header works. When COPILOT_FREE_TIER=1 the assertions
// are relaxed to match free-tier's reduced catalog.
func TestIntegration_ListModels(t *testing.T) {
	requireIntegration(t)

	baseURL := os.Getenv("COPILOT_API_URL")
	if baseURL == "" {
		baseURL = ResolveBaseURL("")
	}
	var token string
	if tok := os.Getenv("GITHUB_COPILOT_TOKEN"); tok != "" {
		token = tok
	} else if os.Getenv("COPILOT_FREE_TIER") == "1" && strings.HasPrefix(os.Getenv("GITHUB_TOKEN"), "github_pat_") {
		// Free-tier direct-bearer mode: ResolveCopilotToken returns the PAT as Token.
		resolved, err := ResolveCopilotToken(context.Background(), os.Getenv("GITHUB_TOKEN"), "", "", 30*time.Second)
		if err != nil {
			t.Fatalf("ResolveCopilotToken: %v", err)
		}
		token = resolved.Token
		if baseURL == "" {
			baseURL = resolved.BaseURL
		}
	} else {
		resp, err := exchangeGitHubToken(context.Background(), os.Getenv("GITHUB_TOKEN"), "", 30*time.Second)
		if err != nil {
			t.Fatalf("exchangeGitHubToken: %v", err)
		}
		token = resp.Token
		if baseURL == "" {
			if resp.Endpoints != nil && resp.Endpoints.API != "" {
				baseURL = resp.Endpoints.API
			} else {
				baseURL = ResolveBaseURL("")
			}
		}
	}

	models, err := ListModels(context.Background(), token, baseURL, 30 * time.Second)
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}

	minModels := 20
	if os.Getenv("COPILOT_FREE_TIER") == "1" {
		minModels = 1
	}
	if len(models) < minModels {
		t.Errorf("expected ≥%d models, got %d", minModels, len(models))
	}

	foundGPT5 := false
	foundClaude := false
	for _, mi := range models {
		if mi.ID == "gpt-5-mini" {
			foundGPT5 = true
		}
		if strings.HasPrefix(mi.ID, "claude-") {
			foundClaude = true
		}
		// Log model details for debugging.
		t.Logf("model: id=%s family=%s state=%s endpoints=%v reasoning_efforts=%v picker_enabled=%v",
			mi.ID, mi.Family, mi.State, mi.SupportedEndpoints, mi.ReasoningEfforts, mi.ModelPickerEnabled)
	}
	if os.Getenv("COPILOT_FREE_TIER") != "1" {
		if !foundGPT5 {
			t.Error("gpt-5-mini not found in model list")
		}
		if !foundClaude {
			t.Error("no Claude models found in model list")
		}
	}
}

// TestIntegration_ModelsTemperatures runs the model × temperature matrix.
func TestIntegration_ModelsTemperatures(t *testing.T) {
	requireIntegration(t)

	ctx := context.Background()

	type modelCase struct {
		modelID string
		family  string
	}
	cases := []modelCase{
		// Reasoning GPT-5
		{modelID: "gpt-5-mini", family: "reasoning-gpt5"},
		{modelID: "gpt-5.4-nano", family: "reasoning-gpt5"},
		// Standard OpenAI
		{modelID: "gpt-4.1", family: "standard"},
		{modelID: "gpt-4o", family: "standard"},
		{modelID: "gpt-3.5-turbo", family: "standard"},
		// Claude
		{modelID: "claude-haiku-4.5", family: "claude"},
	}

	for _, mc := range cases {
		temps := getTestTemperatures(mc.family)

		for _, tc := range temps {
			name := mc.modelID + "_T" + formatFloatTemp(tc.temp)
			t.Run(name, func(t *testing.T) {
				m := newIntegrationModel(t, mc.modelID)

				opts := []model.Option{model.WithMaxTokens(30)}
				if tc.temp != nil {
					opts = append(opts, model.WithTemperature(*tc.temp))
				}

				msg, err := m.Generate(ctx, []*schema.Message{
					{Role: schema.User, Content: "Hi"},
				}, opts...)

				if tc.wantErr {
					if err == nil {
						t.Errorf("expected error containing %q, got nil", tc.errMsg)
					} else if !strings.Contains(err.Error(), tc.errMsg) {
						t.Errorf("expected error containing %q, got: %v", tc.errMsg, err)
					}
					return
				}
				if err != nil {
					t.Fatalf("Generate: %v", err)
				}
				if msg == nil {
					t.Fatal("expected non-nil message")
				}
				if msg.ResponseMeta == nil {
					t.Fatal("expected ResponseMeta")
				}
				if msg.ResponseMeta.FinishReason == "" {
					t.Error("expected non-empty finish reason")
				}
				if msg.Content == "" {
					t.Error("expected non-empty content")
				}
				t.Logf("model=%s temp=%v content=%q finish=%s",
					mc.modelID, tc.temp, truncate(msg.Content, 50),
					msg.ResponseMeta.FinishReason)
			})
		}
	}
}

// TestIntegration_Streaming tests streaming for one reasoning + one standard model.
func TestIntegration_Streaming(t *testing.T) {
	requireIntegration(t)

	models := []string{"gpt-5-mini", "gpt-4.1"}
	ctx := context.Background()

	for _, modelID := range models {
		t.Run(modelID, func(t *testing.T) {
			m := newIntegrationModel(t, modelID)
			sr, err := m.Stream(ctx, []*schema.Message{
				{Role: schema.User, Content: "Say hello in one word"},
			}, model.WithMaxTokens(10))
			if err != nil {
				t.Fatalf("Stream: %v", err)
			}

			var content string
			var sawFinish bool
			for {
				chunk, err := sr.Recv()
				if errors.Is(err, context.DeadlineExceeded) || err != nil && strings.Contains(err.Error(), "EOF") {
					break
				}
				if err != nil {
					// Check if the stream ended cleanly.
					if strings.Contains(err.Error(), "stream closed") {
						break
					}
					t.Fatalf("Recv: %v", err)
				}
				if chunk == nil {
					break
				}
				content += chunk.Content
				if chunk.ResponseMeta != nil && chunk.ResponseMeta.FinishReason != "" {
					sawFinish = true
					t.Logf("model=%s finish=%s tokens=%d/%d",
						modelID, chunk.ResponseMeta.FinishReason,
						chunk.ResponseMeta.Usage.PromptTokens,
						chunk.ResponseMeta.Usage.CompletionTokens)
				}
			}
			if content == "" {
				t.Error("expected non-empty content from stream")
			}
			if !sawFinish {
				t.Log("stream did not receive a finish-reason chunk (may be ok for some models)")
			}
			t.Logf("model=%s stream content: %q", modelID, truncate(content, 50))
		})
	}
}

// TestIntegration_EndpointRouting verifies that gpt-5.4-nano routes to
// /responses (it lists only /responses in supported_endpoints).
func TestIntegration_EndpointRouting(t *testing.T) {
	requireIntegration(t)

	t.Run("gpt-5.4-nano_responses", func(t *testing.T) {
		m := newIntegrationModel(t, "gpt-5.4-nano")
		ctx := context.Background()
		msg, err := m.Generate(ctx, []*schema.Message{
			{Role: schema.User, Content: "Hi"},
		}, model.WithMaxTokens(10))
		if err != nil {
			t.Fatalf("Generate for gpt-5.4-nano (should hit /responses): %v", err)
		}
		if msg.Content == "" {
			t.Error("expected non-empty content")
		}
		t.Logf("gpt-5.4-nano response: %q", truncate(msg.Content, 50))
	})

	t.Run("gpt-4.1_chat", func(t *testing.T) {
		m := newIntegrationModel(t, "gpt-4.1")
		ctx := context.Background()
		msg, err := m.Generate(ctx, []*schema.Message{
			{Role: schema.User, Content: "Hi"},
		}, model.WithMaxTokens(10))
		if err != nil {
			t.Fatalf("Generate for gpt-4.1 (should hit /chat/completions): %v", err)
		}
		if msg.Content == "" {
			t.Error("expected non-empty content")
		}
		t.Logf("gpt-4.1 response: %q", truncate(msg.Content, 50))
	})
}

// TestIntegration_DisabledModels — for disabled models, skip with a tracked TODO.
func TestIntegration_DisabledModels(t *testing.T) {
	requireIntegration(t)

	disabledModels := []string{"gpt-5.4-mini", "gemini-3.1-pro-preview"}

	for _, modelID := range disabledModels {
		t.Run(modelID, func(t *testing.T) {
			// Enablement is not yet implemented (requires the CAPI enablement
			// endpoint, plan §2.6 open question). Skip until enablement lands.
			t.Skipf("model %s needs policy enablement (plan §2.6, open question §4.1)", modelID)
		})
	}
}

// requireBusinessIntegration skips unless a raw GitHub PAT is available for
// token exchange, which is required to auto-detect the business/enterprise plan
// host via endpoints.api.
func requireBusinessIntegration(t *testing.T) {
	t.Helper()
	requireIntegration(t)
	if os.Getenv("GITHUB_TOKEN") == "" {
		t.Skip("GITHUB_TOKEN (raw PAT) not set — business/enterprise plan requires token exchange auto-detection")
	}
}

// TestIntegration_ClaudeSonnet5 proves claude-sonnet-5 works with business
// (and enterprise) API keys via token exchange auto-detection.
// Set COPILOT_INTEGRATION=1 and GITHUB_TOKEN.
func TestIntegration_ClaudeSonnet5(t *testing.T) {
	requireBusinessIntegration(t)

	const modelID = "claude-sonnet-5"

	ctx := context.Background()
	cfg := &Config{
		Model:       modelID,
		GitHubToken: os.Getenv("GITHUB_TOKEN"),
	}
	if url := os.Getenv("COPILOT_API_URL"); url != "" {
		cfg.BaseURL = url
	}

	m, err := NewCopilotChatModel(ctx, cfg)
	if err != nil {
		t.Fatalf("NewCopilotChatModel: %v", err)
	}
	if m.cancelRefresh != nil {
		defer m.cancelRefresh()
	}
	t.Logf("claude-sonnet-5 base URL: %s", m.baseURL)

	t.Run("ListModels", func(t *testing.T) {
		if err := m.PopulateModelsCache(ctx); err != nil {
			t.Fatalf("PopulateModelsCache: %v", err)
		}
		cached := m.getCachedModels()
		t.Logf("total models in catalog: %d", len(cached))

		found := false
		for _, mi := range cached {
			if mi.ID == modelID {
				found = true
				t.Logf("FOUND %s: family=%s state=%s endpoints=%v reasoning_efforts=%v picker=%v tool_calls=%v streaming=%v reasoning=%v vision=%v",
					mi.ID, mi.Family, mi.State, mi.SupportedEndpoints,
					mi.ReasoningEfforts, mi.ModelPickerEnabled,
					mi.SupportsToolCalls, mi.SupportsStreaming,
					mi.SupportsReasoning, mi.SupportsVision)
				break
			}
			if strings.HasPrefix(mi.ID, "claude-") {
				t.Logf("  claude model: %s family=%s state=%s endpoints=%v",
					mi.ID, mi.Family, mi.State, mi.SupportedEndpoints)
			}
		}
		if !found {
			t.Errorf("%s NOT found in model catalog (%d models)", modelID, len(cached))
		}
	})

	t.Run("SessionDiagnostics", func(t *testing.T) {
		sresp, err := acquireSession(ctx, m.baseURL, m.lockedToken.get(), modelID, m.httpClient, m.clientMachineID)
		if err != nil {
			t.Fatalf("acquireSession: %v", err)
		}
		t.Logf("session selected_model: %s", sresp.SelectedModel)
		t.Logf("session available_models (%d):", len(sresp.AvailableModels))
		for _, am := range sresp.AvailableModels {
			t.Logf("  - %s", am)
		}
		hasModel := false
		for _, am := range sresp.AvailableModels {
			if am == modelID {
				hasModel = true
				break
			}
		}
		if hasModel {
			t.Logf("%s is in session available_models", modelID)
		} else {
			t.Logf("%s NOT in session available_models (session selected %q)", modelID, sresp.SelectedModel)
		}
	})

	t.Run("Generate", func(t *testing.T) {
		msg, err := m.Generate(ctx, []*schema.Message{
			{Role: schema.User, Content: "Reply with exactly one word: hello"},
		}, model.WithMaxTokens(20))
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		if msg == nil {
			t.Fatal("expected non-nil message")
		}
		if msg.Content == "" {
			t.Fatal("expected non-empty content")
		}
		t.Logf("content: %q", msg.Content)
		if msg.ResponseMeta == nil {
			t.Fatal("expected ResponseMeta")
		}
		if msg.ResponseMeta.FinishReason == "" {
			t.Error("expected non-empty finish reason")
		}
		t.Logf("finish: %s tokens=%v",
			msg.ResponseMeta.FinishReason, msg.ResponseMeta.Usage)
	})

	t.Run("Generate_Temperature", func(t *testing.T) {
		temps := []*float32{nil, float32Ptr(0.0), float32Ptr(0.5), float32Ptr(1.0)}
		for _, temp := range temps {
			name := "T" + formatFloatTemp(temp)
			t.Run(name, func(t *testing.T) {
				opts := []model.Option{model.WithMaxTokens(20)}
				if temp != nil {
					opts = append(opts, model.WithTemperature(*temp))
				}
				msg, err := m.Generate(ctx, []*schema.Message{
					{Role: schema.User, Content: "Hi"},
				}, opts...)
				if err != nil {
					t.Fatalf("Generate(temp=%v): %v", temp, err)
				}
				if msg == nil || msg.Content == "" {
					t.Fatal("expected non-empty content")
				}
				t.Logf("temp=%v content=%q finish=%s",
					temp, truncate(msg.Content, 40),
					msg.ResponseMeta.FinishReason)
			})
		}
	})

	t.Run("Stream", func(t *testing.T) {
		sr, err := m.Stream(ctx, []*schema.Message{
			{Role: schema.User, Content: "Say hello in one word"},
		}, model.WithMaxTokens(10))
		if err != nil {
			t.Fatalf("Stream: %v", err)
		}

		var content string
		var sawFinish bool
		for {
			chunk, err := sr.Recv()
			if errors.Is(err, context.DeadlineExceeded) ||
				(err != nil && strings.Contains(err.Error(), "EOF")) {
				break
			}
			if err != nil {
				if strings.Contains(err.Error(), "stream closed") {
					break
				}
				t.Fatalf("Recv: %v", err)
			}
			if chunk == nil {
				break
			}
			content += chunk.Content
			if chunk.ResponseMeta != nil && chunk.ResponseMeta.FinishReason != "" {
				sawFinish = true
				t.Logf("stream finish: %s usage=%v",
					chunk.ResponseMeta.FinishReason,
					chunk.ResponseMeta.Usage)
			}
		}
		if content == "" {
			t.Error("expected non-empty content from stream")
		}
		if !sawFinish {
			t.Log("stream did not receive a finish-reason chunk")
		}
		t.Logf("stream content: %q", truncate(content, 50))
	})

	t.Run("ToolCalling", func(t *testing.T) {
		mWithTools, err := m.WithTools([]*schema.ToolInfo{
			{
				Name: "get_weather",
				Desc: "Get the current weather for a city",
				ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
					"city": {
						Type:     "string",
						Desc:     "The city name",
						Required: true,
					},
				}),
			},
		})
		if err != nil {
			t.Fatalf("WithTools: %v", err)
		}
		msg, err := mWithTools.Generate(ctx, []*schema.Message{
			{Role: schema.User, Content: "What is the weather in Paris?"},
		}, model.WithMaxTokens(50))
		if err != nil {
			t.Fatalf("Generate with tools: %v", err)
		}
		if msg == nil {
			t.Fatal("expected non-nil message")
		}
		t.Logf("content: %q", msg.Content)
		if len(msg.ToolCalls) > 0 {
			t.Logf("tool calls: %d", len(msg.ToolCalls))
			for _, tc := range msg.ToolCalls {
				t.Logf("  - %s(%s)", tc.Function.Name, tc.Function.Arguments)
			}
		}
		if msg.ResponseMeta != nil {
			t.Logf("finish: %s", msg.ResponseMeta.FinishReason)
		}
	})
}

// TestIntegration_ReasoningEffort tests reasoning effort values for a reasoning model.
func TestIntegration_ReasoningEffort(t *testing.T) {
	requireIntegration(t)

	m := newIntegrationModel(t, "gpt-5-mini")
	ctx := context.Background()

	efforts := []ReasoningEffort{ReasoningEffortLow, ReasoningEffortMedium, ReasoningEffortHigh}
	for _, eff := range efforts {
		name := "effort_" + string(eff)
		t.Run(name, func(t *testing.T) {
			opts := []model.Option{
				model.WithMaxTokens(10),
				model.WrapImplSpecificOptFn(func(o *CopilotOptions) {
					o.ReasoningEffort = eff
				}),
			}
			msg, err := m.Generate(ctx, []*schema.Message{
				{Role: schema.User, Content: "Hi"},
			}, opts...)
			if err != nil {
				t.Fatalf("Generate with effort=%s: %v", eff, err)
			}
			if msg.Content == "" {
				t.Error("expected non-empty content")
			}
			t.Logf("effort=%s content=%q", eff, truncate(msg.Content, 50))
		})
	}
}

func formatFloatTemp(v *float32) string {
	if v == nil {
		return "nil"
	}
	switch *v {
	case 0:
		return "0"
	case 0.5:
		return "0.5"
	case 1.0:
		return "1"
	case 1.5:
		return "1.5"
	case 2.0:
		return "2"
	default:
		return fmt.Sprintf("%.1f", *v)
	}
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
