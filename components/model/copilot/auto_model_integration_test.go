//go:build integration

package copilot

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// TestIntegration_FreeTier_AutoModel verifies that ModelAuto ("auto") works
// against the real free-tier Copilot API: the session endpoint selects a
// model, and a Generate call succeeds using the resolved model.
func TestIntegration_FreeTier_AutoModel(t *testing.T) {
	requireFreeTierIntegration(t)

	ctx := context.Background()
	tok := freeTierToken(t)

	cfg := &Config{
		GitHubToken: tok,
		Model:       ModelAuto,
	}

	m, err := NewCopilotChatModel(ctx, cfg)
	if err != nil {
		t.Fatalf("NewCopilotChatModel: %v", err)
	}
	t.Cleanup(func() { cleanupAutoRefresh(m) })

	msg, err := m.Generate(ctx, []*schema.Message{
		{Role: schema.User, Content: "Reply with the single word: ok"},
	}, model.WithMaxTokens(10))
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

	resolved, ok := m.ResolvedAutoModel()
	if !ok {
		t.Fatal("expected ResolvedAutoModel to return true")
	}
	if resolved == "" {
		t.Fatal("expected non-empty resolved auto model ID")
	}
	t.Logf("resolved auto model: %s", resolved)
}
