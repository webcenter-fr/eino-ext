package activity

import (
	"context"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockModel struct {
	response *schema.Message
	err      error
}

func (m *mockModel) Generate(ctx context.Context, messages []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.response, nil
}

func (m *mockModel) Stream(ctx context.Context, messages []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, nil
}

func TestSessionSummarizer_GetSummary(t *testing.T) {
	bus, err := NewBus(Config{BufferSize: 100})
	require.NoError(t, err)
	defer bus.Close()

	summarizer := NewSessionSummarizer(bus)

	sessionID := "test-session"
	ctx := context.Background()

	bus.Publish(ctx, Event{
		SessionID: sessionID,
		Type:      TypeStepStarted,
		Data:      StepStarted{Agent: "test-agent", Model: "gpt-4"},
	})

	bus.Publish(ctx, Event{
		SessionID: sessionID,
		Type:      TypeToolCalled,
		Data:      ToolCalled{CallID: "call-1", Tool: "test-tool", Input: "{}"},
	})

	bus.Publish(ctx, Event{
		SessionID: sessionID,
		Type:      TypeStepEnded,
		Data: &StepEnded{
			Finish: "stop",
			Cost:   0.01,
			Tokens: Tokens{
				Input:     100,
				Output:    200,
				Reasoning: 50,
				Cache:     CacheTokens{Read: 10, Write: 0},
			},
		},
	})

	bus.Publish(ctx, Event{
		SessionID: sessionID,
		Type:      TypeTextEnded,
		Data:      TextEnded{Text: "Test output"},
	})

	summary, err := summarizer.GetSummary(sessionID)
	require.NoError(t, err)
	assert.Equal(t, sessionID, summary.SessionID)
	assert.Equal(t, 1, summary.Steps)
	assert.Equal(t, 350, summary.TotalTokens)
	assert.Equal(t, 0.01, summary.TotalCost)
	assert.Equal(t, 1, len(summary.ToolsCalled))
	assert.Equal(t, "test-tool", summary.ToolsCalled[0])
	assert.Equal(t, "Test output", summary.TextOutput)
	assert.Equal(t, 1, len(summary.FinishReasons))
	assert.Equal(t, "stop", summary.FinishReasons[0])
}

func TestSessionSummarizer_TruncatedText(t *testing.T) {
	bus, err := NewBus(Config{BufferSize: 100})
	require.NoError(t, err)
	defer bus.Close()

	summarizer := NewSessionSummarizer(bus)

	sessionID := "test-session"
	ctx := context.Background()

	longText := make([]byte, maxTextLength*2)
	for i := range longText {
		longText[i] = 'a'
	}

	bus.Publish(ctx, Event{
		SessionID: sessionID,
		Type:      TypeTextEnded,
		Data:      TextEnded{Text: string(longText)},
	})

	summary, err := summarizer.GetSummary(sessionID)
	require.NoError(t, err)
	assert.Equal(t, maxTextLength+3, len(summary.TextOutput)) // +3 for "..."
}

func TestComplexityAnalyzer_Analyze(t *testing.T) {
	bus, err := NewBus(Config{BufferSize: 100})
	require.NoError(t, err)
	defer bus.Close()

	summarizer := NewSessionSummarizer(bus)
	sessionID := "test-session"
	ctx := context.Background()

	bus.Publish(ctx, Event{
		SessionID: sessionID,
		Type:      TypeStepStarted,
		Data:      StepStarted{Agent: "test-agent", Model: "gpt-4"},
	})

	bus.Publish(ctx, Event{
		SessionID: sessionID,
		Type:      TypeStepEnded,
		Data: &StepEnded{
			Finish: "stop",
			Cost:   0.01,
			Tokens: Tokens{Input: 100, Output: 200},
		},
	})

	summary, err := summarizer.GetSummary(sessionID)
	require.NoError(t, err)

	mockResponse := `{"complexity_ratio": 0.7, "human_time_saved_seconds": 300.0, "money_saved_usd": 5.0}`
	analyzer := NewComplexityAnalyzer(ComplexityAnalyzerConfig{
		Model:           &mockModel{response: &schema.Message{Content: mockResponse}},
		HumanHourlyRate: 50.0,
		BaseTaskTime:    5 * time.Minute,
		Timeout:         30 * time.Second,
	})

	analysis, err := analyzer.Analyze(ctx, summary)
	require.NoError(t, err)
	assert.Equal(t, 0.7, analysis.ComplexityRatio)
	assert.Equal(t, 300.0, analysis.HumanTimeSavedSeconds)
	assert.Equal(t, 5.0, analysis.MoneySavedUSD)
}

func TestComplexityAnalyzer_ModelNil(t *testing.T) {
	analyzer := NewComplexityAnalyzer(ComplexityAnalyzerConfig{
		Model:           nil,
		HumanHourlyRate: 50.0,
		BaseTaskTime:    5 * time.Minute,
	})

	summary := &SessionSummary{
		SessionID: "test-session",
	}

	_, err := analyzer.Analyze(context.Background(), summary)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "requires a model")
}

func TestComplexityAnalyzer_InvalidJSON(t *testing.T) {
	bus, err := NewBus(Config{BufferSize: 100})
	require.NoError(t, err)
	defer bus.Close()

	summarizer := NewSessionSummarizer(bus)
	sessionID := "test-session"
	ctx := context.Background()

	bus.Publish(ctx, Event{
		SessionID: sessionID,
		Type:      TypeStepStarted,
		Data:      StepStarted{Agent: "test-agent", Model: "gpt-4"},
	})

	bus.Publish(ctx, Event{
		SessionID: sessionID,
		Type:      TypeStepEnded,
		Data: &StepEnded{
			Finish: "stop",
			Cost:   0.01,
			Tokens: Tokens{Input: 100, Output: 200},
		},
	})

	summary, err := summarizer.GetSummary(sessionID)
	require.NoError(t, err)

	analyzer := NewComplexityAnalyzer(ComplexityAnalyzerConfig{
		Model:           &mockModel{response: &schema.Message{Content: "invalid json"}},
		HumanHourlyRate: 50.0,
		BaseTaskTime:    5 * time.Minute,
		Timeout:         30 * time.Second,
	})

	_, err = analyzer.Analyze(ctx, summary)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse")
}

func TestComplexityAnalyzer_InvalidValues(t *testing.T) {
	bus, err := NewBus(Config{BufferSize: 100})
	require.NoError(t, err)
	defer bus.Close()

	summarizer := NewSessionSummarizer(bus)
	sessionID := "test-session"
	ctx := context.Background()

	bus.Publish(ctx, Event{
		SessionID: sessionID,
		Type:      TypeStepStarted,
		Data:      StepStarted{Agent: "test-agent", Model: "gpt-4"},
	})

	bus.Publish(ctx, Event{
		SessionID: sessionID,
		Type:      TypeStepEnded,
		Data: &StepEnded{
			Finish: "stop",
			Cost:   0.01,
			Tokens: Tokens{Input: 100, Output: 200},
		},
	})

	summary, err := summarizer.GetSummary(sessionID)
	require.NoError(t, err)

	mockResponse := `{"complexity_ratio": 1.5, "human_time_saved_seconds": -1.0, "money_saved_usd": -5.0}`
	analyzer := NewComplexityAnalyzer(ComplexityAnalyzerConfig{
		Model:           &mockModel{response: &schema.Message{Content: mockResponse}},
		HumanHourlyRate: 50.0,
		BaseTaskTime:    5 * time.Minute,
		Timeout:         30 * time.Second,
	})

	_, err = analyzer.Analyze(ctx, summary)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid")
}

func TestComplexityAnalyzer_TrivialZerosPassValidation(t *testing.T) {
	summary := &SessionSummary{
		Steps:       1,
		ToolsCalled: []string{},
		TotalTokens: 20,
		TextOutput:  "Hello!",
	}
	mockResponse := `{"complexity_ratio": 0, "human_time_saved_seconds": 0, "money_saved_usd": 0}`
	analyzer := NewComplexityAnalyzer(ComplexityAnalyzerConfig{
		Model:           &mockModel{response: &schema.Message{Content: mockResponse}},
		HumanHourlyRate: 50.0,
		BaseTaskTime:    5 * time.Minute,
		Timeout:         30 * time.Second,
	})

	analysis, err := analyzer.Analyze(context.Background(), summary)
	require.NoError(t, err)
	assert.Equal(t, 0.0, analysis.ComplexityRatio)
	assert.Equal(t, 0.0, analysis.HumanTimeSavedSeconds)
	assert.Equal(t, 0.0, analysis.MoneySavedUSD)
}

func TestFallbackComplexityAnalyzer_Analyze(t *testing.T) {
	analyzer := NewFallbackComplexityAnalyzer(50.0, 5*time.Minute)

	tests := []struct {
		name          string
		summary       *SessionSummary
		expectedRatio float64
		expectSaved   bool // true => HumanTimeSavedSeconds and MoneySavedUSD must be > 0
	}{
		{
			name: "trivial hello-like session (no tools, single step, low tokens)",
			summary: &SessionSummary{
				SessionID:   "test-session",
				Steps:       1,
				ToolsCalled: []string{},
				TotalTokens: 50,
				TextOutput:  "Hello! How can I help?",
			},
			expectedRatio: 0,
			expectSaved:   false,
		},
		{
			name: "single-step with one tool, no tokens (real one-shot automation)",
			summary: &SessionSummary{
				SessionID:   "test-session",
				Steps:       1,
				ToolsCalled: []string{"opensearch-health"},
				TotalTokens: 0,
			},
			expectedRatio: 0.2, // 0 + 0.2 + 0
			expectSaved:   true,
		},
		{
			name: "simple session",
			summary: &SessionSummary{
				SessionID:   "test-session",
				TotalTokens: 1000,
				ToolsCalled: []string{"tool1"},
				Steps:       1,
			},
			expectedRatio: 0.3, // 0.1 + 0.2 + 0 (was 0.4)
			expectSaved:   true,
		},
		{
			name: "complex session",
			summary: &SessionSummary{
				SessionID:   "test-session",
				TotalTokens: 5000,
				ToolsCalled: []string{"tool1", "tool2", "tool3"},
				Steps:       5,
			},
			expectedRatio: 1.0, // 0.5 + 0.6 + 0.4 -> 1.5 -> 1.0
			expectSaved:   true,
		},
		{
			name: "session with failures",
			summary: &SessionSummary{
				SessionID:   "test-session",
				TotalTokens: 5000,
				ToolsCalled: []string{"tool1", "tool2"},
				Steps:       3,
				HadFailures: true,
			},
			expectedRatio: 0.8, // (0.5+0.4+0.2 -> 1.1 -> 1.0) * 0.8
			expectSaved:   true,
		},
		{
			name: "failures-only no tools (trivial, no automation)",
			summary: &SessionSummary{
				SessionID:   "test-session",
				Steps:       2,
				ToolsCalled: []string{},
				HadFailures: true,
			},
			expectedRatio: 0,
			expectSaved:   false,
		},
		{
			name: "empty summary (no events)",
			summary: &SessionSummary{
				SessionID:   "test-session",
				ToolsCalled: []string{},
			},
			expectedRatio: 0,
			expectSaved:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			analysis, err := analyzer.Analyze(context.Background(), tt.summary)
			require.NoError(t, err)
			assert.InDelta(t, tt.expectedRatio, analysis.ComplexityRatio, 1e-9)
			if tt.expectSaved {
				assert.True(t, analysis.HumanTimeSavedSeconds > 0,
					"expected HumanTimeSavedSeconds > 0 for %s", tt.name)
				assert.True(t, analysis.MoneySavedUSD > 0,
					"expected MoneySavedUSD > 0 for %s", tt.name)
			} else {
				assert.Equal(t, 0.0, analysis.HumanTimeSavedSeconds)
				assert.Equal(t, 0.0, analysis.MoneySavedUSD)
			}
		})
	}
}

func TestCompositeComplexityAnalyzer_LLM(t *testing.T) {
	bus, err := NewBus(Config{BufferSize: 100})
	require.NoError(t, err)
	defer bus.Close()

	summarizer := NewSessionSummarizer(bus)
	sessionID := "test-session"
	ctx := context.Background()

	bus.Publish(ctx, Event{
		SessionID: sessionID,
		Type:      TypeStepStarted,
		Data:      StepStarted{Agent: "test-agent", Model: "gpt-4"},
	})

	bus.Publish(ctx, Event{
		SessionID: sessionID,
		Type:      TypeStepEnded,
		Data: &StepEnded{
			Finish: "stop",
			Cost:   0.01,
			Tokens: Tokens{Input: 100, Output: 200},
		},
	})

	summary, err := summarizer.GetSummary(sessionID)
	require.NoError(t, err)

	mockResponse := `{"complexity_ratio": 0.8, "human_time_saved_seconds": 400.0, "money_saved_usd": 6.0}`
	analyzer := NewCompositeComplexityAnalyzer(ComplexityAnalyzerConfig{
		Model:           &mockModel{response: &schema.Message{Content: mockResponse}},
		HumanHourlyRate: 50.0,
		BaseTaskTime:    5 * time.Minute,
		Timeout:         30 * time.Second,
	})

	analysis, err := analyzer.Analyze(ctx, summary)
	require.NoError(t, err)
	assert.Equal(t, 0.8, analysis.ComplexityRatio)
	assert.Equal(t, 400.0, analysis.HumanTimeSavedSeconds)
	assert.Equal(t, 6.0, analysis.MoneySavedUSD)
}

func TestCompositeComplexityAnalyzer_Fallback(t *testing.T) {
	bus, err := NewBus(Config{BufferSize: 100})
	require.NoError(t, err)
	defer bus.Close()

	summarizer := NewSessionSummarizer(bus)
	sessionID := "test-session"
	ctx := context.Background()

	bus.Publish(ctx, Event{
		SessionID: sessionID,
		Type:      TypeStepStarted,
		Data:      StepStarted{Agent: "test-agent", Model: "gpt-4"},
	})

	bus.Publish(ctx, Event{
		SessionID: sessionID,
		Type:      TypeStepEnded,
		Data: &StepEnded{
			Finish: "stop",
			Cost:   0.01,
			Tokens: Tokens{Input: 100, Output: 200},
		},
	})

	summary, err := summarizer.GetSummary(sessionID)
	require.NoError(t, err)

	analyzer := NewCompositeComplexityAnalyzer(ComplexityAnalyzerConfig{
		Model:           nil,
		HumanHourlyRate: 50.0,
		BaseTaskTime:    5 * time.Minute,
	})

	analysis, err := analyzer.Analyze(ctx, summary)
	require.NoError(t, err)
	assert.True(t, analysis.ComplexityRatio >= 0)
	assert.True(t, analysis.HumanTimeSavedSeconds >= 0)
	assert.True(t, analysis.MoneySavedUSD >= 0)
}

func TestComplexityAnalyzerConfig_Defaults(t *testing.T) {
	config := ComplexityAnalyzerConfig{}
	analyzer := NewComplexityAnalyzer(config)

	assert.Equal(t, 50.0, analyzer.config.HumanHourlyRate)
	assert.Equal(t, 5*time.Minute, analyzer.config.BaseTaskTime)
	assert.Equal(t, 30*time.Second, analyzer.config.Timeout)
}

func TestComplexityAnalyzer_FencedAndProseJSON(t *testing.T) {
	summary := &SessionSummary{}

	tests := []struct {
		name      string
		content   string
		wantRatio float64
		wantTime  float64
		wantMoney float64
	}{
		{
			name:      "fenced JSON with language tag",
			content:   "```json\n{\"complexity_ratio\": 0.7, \"human_time_saved_seconds\": 300.0, \"money_saved_usd\": 5.0}\n```",
			wantRatio: 0.7,
			wantTime:  300.0,
			wantMoney: 5.0,
		},
		{
			name:      "fenced JSON without language tag",
			content:   "```\n{\"complexity_ratio\": 0.6, \"human_time_saved_seconds\": 200.0, \"money_saved_usd\": 4.0}\n```",
			wantRatio: 0.6,
			wantTime:  200.0,
			wantMoney: 4.0,
		},
		{
			name:      "JSON with leading prose",
			content:   "Sure! Here is the complexity analysis:\n{\"complexity_ratio\": 0.5, \"human_time_saved_seconds\": 100.0, \"money_saved_usd\": 2.0}",
			wantRatio: 0.5,
			wantTime:  100.0,
			wantMoney: 2.0,
		},
		{
			name:      "JSON with prose before and after",
			content:   "Here is the result:\n{\"complexity_ratio\": 0.4, \"human_time_saved_seconds\": 50.0, \"money_saved_usd\": 1.0}\nDone.",
			wantRatio: 0.4,
			wantTime:  50.0,
			wantMoney: 1.0,
		},
		{
			name:      "fenced JSON with prose around the fence",
			content:   "Analysis:\n```json\n{\"complexity_ratio\": 0.3, \"human_time_saved_seconds\": 25.0, \"money_saved_usd\": 0.5}\n```\nDone.",
			wantRatio: 0.3,
			wantTime:  25.0,
			wantMoney: 0.5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			analyzer := NewComplexityAnalyzer(ComplexityAnalyzerConfig{
				Model:           &mockModel{response: &schema.Message{Content: tt.content}},
				HumanHourlyRate: 50.0,
				BaseTaskTime:    5 * time.Minute,
				Timeout:         30 * time.Second,
			})
			analysis, err := analyzer.Analyze(context.Background(), summary)
			require.NoError(t, err)
			assert.Equal(t, tt.wantRatio, analysis.ComplexityRatio)
			assert.Equal(t, tt.wantTime, analysis.HumanTimeSavedSeconds)
			assert.Equal(t, tt.wantMoney, analysis.MoneySavedUSD)
		})
	}
}

func TestComplexityAnalyzer_UnparseableResponses(t *testing.T) {
	summary := &SessionSummary{}

	tests := []struct {
		name    string
		content string
	}{
		{name: "fenced invalid inner text without JSON brackets", content: "```json\nnot valid json at all\n```"},
		{name: "backticks only no JSON", content: "```\n```"},
		{name: "pure prose without any JSON", content: "I cannot analyze this session."},
		{name: "empty response", content: ""},
		{name: "whitespace-only response", content: "   \n\t  "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			analyzer := NewComplexityAnalyzer(ComplexityAnalyzerConfig{
				Model:           &mockModel{response: &schema.Message{Content: tt.content}},
				HumanHourlyRate: 50.0,
				BaseTaskTime:    5 * time.Minute,
				Timeout:         30 * time.Second,
			})
			_, err := analyzer.Analyze(context.Background(), summary)
			require.Error(t, err, "expected error for %q", tt.name)
			assert.Contains(t, err.Error(), "failed to parse")
		})
	}
}