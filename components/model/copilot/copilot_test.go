package copilot

import (
	"context"
	"testing"
	"time"
)

func TestNewCopilotChatModelDirectToken(t *testing.T) {
	ctx := context.Background()
	cfg := &Config{
		CopilotToken: "test-copilot-token",
		BaseURL:      "http://localhost:0",
		Timeout:      10 * time.Second,
	}

	m, err := NewCopilotChatModel(ctx, cfg)
	if err != nil {
		t.Fatalf("NewCopilotChatModel: unexpected error: %v", err)
	}
	if m == nil {
		t.Fatal("expected non-nil model")
	}
	if m.lockedToken.get() != "test-copilot-token" {
		t.Errorf("expected token 'test-copilot-token', got %q", m.lockedToken.get())
	}
	if m.baseURL != "http://localhost:0" {
		t.Errorf("expected baseURL 'http://localhost:0', got %q", m.baseURL)
	}
	if m.cancelRefresh != nil {
		t.Error("expected nil cancelRefresh when using direct CopilotToken")
	}
}

func TestNewCopilotChatModelNilConfig(t *testing.T) {
	ctx := context.Background()
	_, err := NewCopilotChatModel(ctx, nil)
	if err == nil {
		t.Fatal("expected error for nil config")
	}
}

func TestNewCopilotChatModelNoToken(t *testing.T) {
	ctx := context.Background()
	cfg := &Config{
		Timeout: 10 * time.Second,
	}
	_, err := NewCopilotChatModel(ctx, cfg)
	if err == nil {
		t.Fatal("expected error for missing token")
	}
}

func TestNewCopilotChatModelInvalidTimeout(t *testing.T) {
	ctx := context.Background()
	cfg := &Config{
		CopilotToken: "token",
		Timeout:      500 * time.Millisecond,
	}
	_, err := NewCopilotChatModel(ctx, cfg)
	if err == nil {
		t.Fatal("expected error for timeout below minimum (1s)")
	}
}

func TestCopilotModelGetType(t *testing.T) {
	ctx := context.Background()
	cfg := &Config{
		CopilotToken: "token",
		BaseURL:      "http://localhost:0",
		Timeout:      10 * time.Second,
	}
	m, err := NewCopilotChatModel(ctx, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := m.GetType(); got != copilotGetType {
		t.Errorf("GetType() = %q, want %q", got, copilotGetType)
	}
}

func TestCopilotModelIsCallbacksEnabled(t *testing.T) {
	ctx := context.Background()
	cfg := &Config{
		CopilotToken: "token",
		BaseURL:      "http://localhost:0",
		Timeout:      10 * time.Second,
	}
	m, err := NewCopilotChatModel(ctx, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !m.IsCallbacksEnabled() {
		t.Error("expected IsCallbacksEnabled to return true")
	}
}

func TestCopilotModelWithTools(t *testing.T) {
	ctx := context.Background()
	cfg := &Config{
		CopilotToken: "token",
		BaseURL:      "http://localhost:0",
		Timeout:      10 * time.Second,
	}
	m, err := NewCopilotChatModel(ctx, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// WithTools with empty tools should fail (inner model validates)
	_, err = m.WithTools(nil)
	if err == nil {
		t.Fatal("expected error for nil tools")
	}
}

func TestCopilotModelBindTools(t *testing.T) {
	ctx := context.Background()
	cfg := &Config{
		CopilotToken: "token",
		BaseURL:      "http://localhost:0",
		Timeout:      10 * time.Second,
	}
	m, err := NewCopilotChatModel(ctx, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	err = m.BindTools(nil)
	if err == nil {
		t.Fatal("expected error for nil tools")
	}
}

func TestNewCopilotChatModelDefaultTimeout(t *testing.T) {
	ctx := context.Background()
	cfg := &Config{
		CopilotToken: "token",
		BaseURL:      "http://localhost:0",
	}
	m, err := NewCopilotChatModel(ctx, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.cfg.Timeout != defaultTimeout {
		t.Errorf("expected default timeout %v, got %v", defaultTimeout, m.cfg.Timeout)
	}
}

func TestCopilotModelEnterpriseBaseURL(t *testing.T) {
	ctx := context.Background()
	cfg := &Config{
		CopilotToken:  "token",
		EnterpriseURL: "mycompany.com",
		Timeout:       10 * time.Second,
	}
	m, err := NewCopilotChatModel(ctx, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.baseURL != "https://copilot-api.mycompany.com" {
		t.Errorf("expected enterprise base URL, got %q", m.baseURL)
	}
}

func TestCopilotModelBaseURLOverride(t *testing.T) {
	ctx := context.Background()
	cfg := &Config{
		CopilotToken:  "token",
		BaseURL:       "https://custom.example.com",
		EnterpriseURL: "mycompany.com",
		Timeout:       10 * time.Second,
	}
	m, err := NewCopilotChatModel(ctx, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.baseURL != "https://custom.example.com" {
		t.Errorf("expected BaseURL override, got %q", m.baseURL)
	}
}
