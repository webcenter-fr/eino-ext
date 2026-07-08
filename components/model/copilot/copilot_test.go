package copilot

import (
	"context"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
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

	m2, err := m.WithTools(nil)
	if err != nil {
		t.Fatalf("unexpected error for nil tools: %v", err)
	}
	if m2 == nil {
		t.Fatal("expected non-nil model")
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

	if err := m.BindTools(nil); err != nil {
		t.Fatalf("unexpected error for nil tools: %v", err)
	}
}

func TestCopilotModelWithToolsStoresTools(t *testing.T) {
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

	toolInfo := &schema.ToolInfo{
		Name: "test-tool",
		Desc: "a test tool",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"arg1": {Type: schema.String, Required: true},
		}),
	}

	m2, err := m.WithTools([]*schema.ToolInfo{toolInfo})
	if err != nil {
		t.Fatalf("WithTools: unexpected error: %v", err)
	}

	cm := m2.(*CopilotModel)
	if len(cm.tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(cm.tools))
	}
	if cm.tools[0].Name != "test-tool" {
		t.Errorf("expected tool name 'test-tool', got %q", cm.tools[0].Name)
	}
	if cm.toolChoice == nil {
		t.Fatal("expected toolChoice to be set")
	}
}

func TestCopilotModelBindToolsStoresTools(t *testing.T) {
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

	toolInfo := &schema.ToolInfo{
		Name: "test-tool",
		Desc: "a test tool",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"arg1": {Type: schema.String, Required: true},
		}),
	}

	if err := m.BindTools([]*schema.ToolInfo{toolInfo}); err != nil {
		t.Fatalf("BindTools: unexpected error: %v", err)
	}

	if len(m.tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(m.tools))
	}
	if m.tools[0].Name != "test-tool" {
		t.Errorf("expected tool name 'test-tool', got %q", m.tools[0].Name)
	}
	if m.toolChoice == nil {
		t.Fatal("expected toolChoice to be set")
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

func TestNewCopilotChatModelWithReasoning(t *testing.T) {
	ctx := context.Background()
	cfg := &Config{
		CopilotToken:  "token",
		BaseURL:       "http://localhost:0",
		Timeout:       10 * time.Second,
		Model:         "gpt-4o",
		ReasoningEffort: ReasoningEffortHigh,
	}
	m, err := NewCopilotChatModel(ctx, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m == nil {
		t.Fatal("expected non-nil model")
	}
}
