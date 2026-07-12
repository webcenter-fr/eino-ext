//go:build integration

package copilot

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
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


// TestIntegration_ListModels verifies that ListModels returns ≥20 models
// including gpt-5-mini and at least one Claude model, proving the
// integration-id header works.
func TestIntegration_ListModels(t *testing.T) {
	requireIntegration(t)

	baseURL := os.Getenv("COPILOT_API_URL")
	if baseURL == "" {
		baseURL = ResolveBaseURL("")
	}
	var token string
	if tok := os.Getenv("GITHUB_COPILOT_TOKEN"); tok != "" {
		token = tok
	} else {
		tok2 := os.Getenv("GITHUB_TOKEN")
		resp, err := exchangeGitHubToken(context.Background(), tok2, "", 30 * time.Second)
		if err != nil {
			t.Fatalf("exchangeGitHubToken: %v", err)
		}
		token = resp.Token
	}

	models, err := ListModels(context.Background(), token, baseURL, 30 * time.Second)
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}

	if len(models) < 20 {
		t.Errorf("expected ≥20 models, got %d", len(models))
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
	if !foundGPT5 {
		t.Error("gpt-5-mini not found in model list")
	}
	if !foundClaude {
		t.Error("no Claude models found in model list")
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

	disabledModels := []string{"claude-sonnet-5", "gpt-5.4-mini", "gemini-3.1-pro-preview"}

	for _, modelID := range disabledModels {
		t.Run(modelID, func(t *testing.T) {
			// Enablement is not yet implemented (requires the CAPI enablement
			// endpoint, plan §2.6 open question). Skip until enablement lands.
			t.Skipf("model %s needs policy enablement (plan §2.6, open question §4.1)", modelID)
		})
	}
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
