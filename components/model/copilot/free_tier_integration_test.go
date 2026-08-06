//go:build integration

package copilot

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/checkup"
)

func requireFreeTierIntegration(t *testing.T) {
	t.Helper()
	if os.Getenv("COPILOT_INTEGRATION") != "1" {
		t.Skip("COPILOT_INTEGRATION=1 not set")
	}
	if os.Getenv("COPILOT_FREE_TIER") != "1" {
		t.Skip("COPILOT_FREE_TIER=1 not set (free-tier suite)")
	}
	if os.Getenv("GITHUB_TOKEN") == "" {
		t.Skip("GITHUB_TOKEN (fine-grained PAT) not set")
	}
	if !strings.HasPrefix(os.Getenv("GITHUB_TOKEN"), "github_pat_") {
		t.Skip("free-tier suite requires a fine-grained PAT (github_pat_...)")
	}
	if testing.Short() {
		t.Skip("skipping integration tests in short mode")
	}
}

func freeTierToken(t *testing.T) string {
	t.Helper()
	return os.Getenv("GITHUB_TOKEN")
}

// TestIntegration_FreeTier_DetectTokenKind verifies the token is detected as
// a fine-grained PAT.
func TestIntegration_FreeTier_DetectTokenKind(t *testing.T) {
	requireFreeTierIntegration(t)

	kind := DetectTokenKind(freeTierToken(t))
	if kind != TokenKindFineGrainedPAT {
		t.Errorf("expected TokenKindFineGrainedPAT, got %q", kind)
	}
	t.Logf("TokenKind: %s", kind)
}

// TestIntegration_FreeTier_UserValidation verifies validateFineGrainedPAT
// succeeds against the real API (CLI's first call: GET /copilot_internal/user).
func TestIntegration_FreeTier_UserValidation(t *testing.T) {
	requireFreeTierIntegration(t)

	login, err := validateFineGrainedPAT(context.Background(), freeTierToken(t), "", 30*time.Second, nil)
	if err != nil {
		t.Fatalf("validateFineGrainedPAT: %v", err)
	}
	if login == "" {
		t.Error("expected non-empty login")
	}
	t.Logf("login: %s", login)
}

// TestIntegration_FreeTier_BaseURLResolution verifies ResolveCopilotToken
// returns the PAT directly in direct-bearer mode.
func TestIntegration_FreeTier_BaseURLResolution(t *testing.T) {
	requireFreeTierIntegration(t)
	ctx := context.Background()

	tok := freeTierToken(t)
	resolved, err := ResolveCopilotToken(ctx, tok, "", "", 30*time.Second)
	if err != nil {
		t.Fatalf("ResolveCopilotToken: %v", err)
	}
	if resolved.Token == "" {
		t.Fatal("resolved.Token is empty")
	}

	// In direct-bearer mode, the PAT itself is returned as Token.
	if resolved.Token != tok {
		t.Errorf("expected Token == PAT, got Token=%q", resolved.Token)
	}
	if resolved.Kind != TokenKindFineGrainedPAT {
		t.Errorf("expected Kind == TokenKindFineGrainedPAT, got %q", resolved.Kind)
	}
	if resolved.BaseURL != defaultCopilotBase {
		t.Logf("BaseURL=%q differs from default %q", resolved.BaseURL, defaultCopilotBase)
	}
	if resolved.Plan != PlanIndividual {
		t.Errorf("expected PlanIndividual, got %q", resolved.Plan)
	}
	if resolved.ExpiresAt != 0 {
		t.Errorf("expected ExpiresAt == 0 (no exchange), got %d", resolved.ExpiresAt)
	}

	t.Logf("ResolveCopilotToken: BaseURL=%s Plan=%s Kind=%s ExpiresAt=%d", resolved.BaseURL, resolved.Plan, resolved.Kind, resolved.ExpiresAt)

	models, err := ListModels(ctx, resolved.Token, resolved.BaseURL, 30*time.Second)
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	t.Logf("ListModels returned %d models", len(models))
}

// TestIntegration_FreeTier_ListModels verifies ListModels returns ≥1 model.
func TestIntegration_FreeTier_ListModels(t *testing.T) {
	requireFreeTierIntegration(t)
	ctx := context.Background()

	tok := freeTierToken(t)
	resolved, err := ResolveCopilotToken(ctx, tok, "", "", 30*time.Second)
	if err != nil {
		t.Fatalf("ResolveCopilotToken: %v", err)
	}

	models, err := ListModels(ctx, resolved.Token, resolved.BaseURL, 30*time.Second)
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}

	if len(models) < 1 {
		t.Errorf("expected ≥1 model, got %d", len(models))
	}
	for _, mi := range models {
		t.Logf("model: id=%s state=%s picker_enabled=%v", mi.ID, mi.State, mi.ModelPickerEnabled)
	}
}

// TestIntegration_FreeTier_Generate verifies Generate works with a
// free-tier-available model in direct-bearer mode.
func TestIntegration_FreeTier_Generate(t *testing.T) {
	requireFreeTierIntegration(t)
	ctx := context.Background()

	tok := freeTierToken(t)

	// Build a CopilotModel with GitHubToken only.
	cfg := &Config{
		GitHubToken: tok,
	}

	m, err := NewCopilotChatModel(ctx, cfg)
	if err != nil {
		t.Fatalf("NewCopilotChatModel: %v", err)
	}

	// Direct-bearer mode: no refresh goroutine.
	if m.cancelRefresh != nil {
		t.Error("cancelRefresh should be nil in direct-bearer mode")
	}
	if got := m.lockedToken.get(); got != tok {
		t.Errorf("lockedToken should be PAT, got %q", got)
	}
	if m.baseURL != defaultCopilotBase {
		t.Errorf("baseURL should be %q, got %q", defaultCopilotBase, m.baseURL)
	}
	t.Cleanup(func() {
		if m.cancelRefresh != nil {
			m.cancelRefresh()
		}
	})

	t.Logf("resolved base URL: %s (plan=%s)", m.baseURL, DetectPlan(m.baseURL))

	// Pick the first enabled+ModelPickerEnabled model dynamically.
	models, err := ListModels(ctx, m.lockedToken.get(), m.baseURL, 30*time.Second)
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}

	modelID := ""
	for _, mi := range models {
		if mi.State == "enabled" && mi.ModelPickerEnabled {
			modelID = mi.ID
			t.Logf("selected model: %s", modelID)
			break
		}
	}
	if modelID == "" {
		modelID = "gpt-4o"
		t.Logf("no enabled+picker model found, falling back to %q", modelID)
	}

	msg, err := m.Generate(ctx, []*schema.Message{
		{Role: schema.User, Content: "Reply with the single word: ok"},
	}, model.WithMaxTokens(10), model.WithModel(modelID))
	if err != nil {
		if strings.Contains(err.Error(), "402") || strings.Contains(err.Error(), "quota") || strings.Contains(err.Error(), "403") {
			t.Skipf("free-tier quota exhausted: %v", err)
		}
		t.Fatalf("Generate: %v", err)
	}
	if msg == nil || msg.Content == "" {
		t.Fatal("expected non-empty Generate content")
	}
	t.Logf("Generate OK: %q", truncate(msg.Content, 50))
}

// TestIntegration_FreeTier_Check verifies copilot.Check reports direct-bearer
// mode for fine-grained PATs.
func TestIntegration_FreeTier_Check(t *testing.T) {
	requireFreeTierIntegration(t)
	ctx := context.Background()

	results := Check(ctx, &Config{GitHubToken: freeTierToken(t)})
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
		t.Errorf("copilot_token_exchange = %q, want OK (%s)", exchange.Status, exchange.Error)
	}
	if !strings.Contains(exchange.Message, "direct-bearer") {
		t.Errorf("exchange message should contain 'direct-bearer', got %q", exchange.Message)
	}
	if models.Status == checkup.StatusError {
		t.Errorf("copilot_models = error: %v", models.Error)
	}
	if models.Status != checkup.StatusOK && models.Status != checkup.StatusLimited {
		t.Errorf("copilot_models = %q, want OK or Limited", models.Status)
	}
}
