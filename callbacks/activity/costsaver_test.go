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

func TestFallbackComplexityAnalyzer_Analyze(t *testing.T) {
	analyzer := NewFallbackComplexityAnalyzer(50.0, 5*time.Minute)

	tests := []struct {
		name     string
		summary  *SessionSummary
		expected float64
	}{
		{
			name: "simple session",
			summary: &SessionSummary{
				SessionID:   "test-session",
				TotalTokens: 1000,
				ToolsCalled: []string{"tool1"},
				Steps:       1,
			},
			expected: 0.4,
		},
		{
			name: "complex session",
			summary: &SessionSummary{
				SessionID:   "test-session",
				TotalTokens: 5000,
				ToolsCalled: []string{"tool1", "tool2", "tool3"},
				Steps:       5,
			},
			expected: 1.0,
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
			expected: 0.8,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			analysis, err := analyzer.Analyze(context.Background(), tt.summary)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, analysis.ComplexityRatio)
			assert.True(t, analysis.HumanTimeSavedSeconds >= 0)
			assert.True(t, analysis.MoneySavedUSD >= 0)
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