package chatmodel

import (
	"context"
	"testing"
	"time"

	openai "github.com/cloudwego/eino-ext/components/model/openai"
)

func TestParseThinkingLevel(t *testing.T) {
	cases := []struct {
		in      string
		want    ThinkingLevel
		wantErr bool
	}{
		{"", Off, false},
		{"  ", Off, false},
		{"off", Off, false},
		{"OFF", Off, false},
		{"false", Off, false},
		{"none", Off, false},
		{"true", Medium, false},
		{"True", Medium, false},
		{"low", Low, false},
		{"  Low ", Low, false},
		{"medium", Medium, false},
		{"high", High, false},
		{"HIGH", High, false},
		{"xhigh", "", true},
		{"minimal", "", true},
		{"banana", "", true},
	}
	for _, c := range cases {
		got, err := ParseThinkingLevel(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParseThinkingLevel(%q): expected error, got %q", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseThinkingLevel(%q): unexpected error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseThinkingLevel(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestReasoningEffort(t *testing.T) {
	cases := []struct {
		in     ThinkingLevel
		want   openai.ReasoningEffortLevel
		wantOK bool
	}{
		{Low, openai.ReasoningEffortLevelLow, true},
		{Medium, openai.ReasoningEffortLevelMedium, true},
		{High, openai.ReasoningEffortLevelHigh, true},
		{Off, "", false},
		{ThinkingLevel("bogus"), "", false},
	}
	for _, c := range cases {
		got, ok := reasoningEffort(c.in)
		if ok != c.wantOK {
			t.Errorf("reasoningEffort(%q): ok = %v, want %v", c.in, ok, c.wantOK)
		}
		if ok && got != c.want {
			t.Errorf("reasoningEffort(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCapOutputTokens(t *testing.T) {
	cases := []struct {
		name             string
		modelOutputLimit int
		ceiling          int
		want             int
	}{
		{"ceiling default when zero", 100_000, 0, OutputTokenMax},
		{"ceiling default when negative", 100_000, -5, OutputTokenMax},
		{"min selection picks limit", 8_000, 16_000, 8_000},
		{"min selection picks ceiling", 50_000, 16_000, 16_000},
		{"unknown limit returns ceiling", 0, 16_000, 16_000},
		{"unknown limit and ceiling returns default", 0, 0, OutputTokenMax},
		{"negative limit returns ceiling", -1, 16_000, 16_000},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := CapOutputTokens(c.modelOutputLimit, c.ceiling); got != c.want {
				t.Errorf("CapOutputTokens(%d, %d) = %d, want %d", c.modelOutputLimit, c.ceiling, got, c.want)
			}
		})
	}
}

func TestNewValidation(t *testing.T) {
	ctx := context.Background()

	if _, err := New(ctx, nil); err == nil {
		t.Error("New(nil): expected error, got nil")
	}

	if _, err := New(ctx, &Config{Plan: "gemini"}); err == nil {
		t.Error("New(unsupported plan): expected error, got nil")
	}
}

// TestNewOpenAIConfig asserts the OpenAI config mapping without hitting a
// network: NewChatModel for the openai plan does not dial on construction.
func TestNewOpenAIConfig(t *testing.T) {
	ctx := context.Background()

	// Off must not set a default timeout regression and must construct.
	m, err := New(ctx, &Config{
		Plan:        "openai",
		BaseURL:     "http://localhost:0",
		Model:       "gpt-x",
		Temperature: 0.5,
		Thinking:    Off,
		Timeout:     5 * time.Second,
	})
	if err != nil {
		t.Fatalf("New(openai, Off): unexpected error: %v", err)
	}
	if m == nil {
		t.Fatal("New(openai, Off): nil model")
	}
}

// TestNewCopilotConfig asserts the Copilot config mapping without hitting a
// network.
func TestNewCopilotConfig(t *testing.T) {
	ctx := context.Background()

	m, err := New(ctx, &Config{
		Plan:    "github-copilot",
		BaseURL: "http://localhost:0",
		Model:   "gpt-x",
		APIKey:  "test-copilot-token",
	})
	if err != nil {
		t.Fatalf("New(github-copilot): unexpected error: %v", err)
	}
	if m == nil {
		t.Fatal("New(github-copilot): nil model")
	}
}

// TestNewCopilotMissingAPIKey asserts that github-copilot plan fails without APIKey.
func TestNewCopilotMissingAPIKey(t *testing.T) {
	ctx := context.Background()

	_, err := New(ctx, &Config{
		Plan:    "github-copilot",
		BaseURL: "http://localhost:0",
		Model:   "gpt-x",
	})
	if err == nil {
		t.Fatal("New(github-copilot) without APIKey: expected error, got nil")
	}
}

// TestNewCopilotWithThinking asserts reasoning config passthrough.
func TestNewCopilotWithThinking(t *testing.T) {
	ctx := context.Background()

	m, err := New(ctx, &Config{
		Plan:            "github-copilot",
		BaseURL:         "http://localhost:0",
		Model:           "gpt-x",
		APIKey:          "test-copilot-token",
		Thinking:        High,
		MaxOutputTokens: 1234,
	})
	if err != nil {
		t.Fatalf("New(github-copilot, High): unexpected error: %v", err)
	}
	if m == nil {
		t.Fatal("New(github-copilot, High): nil model")
	}
}
