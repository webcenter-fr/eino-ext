package activity

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/sirupsen/logrus"
)

const maxTextLength = 2000

// complexityAnalysisPrompt is the Markdown template for analyzing AI agent session
// complexity and estimating human time saved and money saved in USD.
//
//go:embed prompts/complexity_analysis.md
var complexityAnalysisPrompt string

// SessionSummary represents a summary of a session's activity.
type SessionSummary struct {
	SessionID        string
	Duration         time.Duration
	TotalCost        float64
	TotalTokens      int
	Steps            int
	ToolsCalled      []string
	TextOutput       string
	ReasoningContent string
	FinishReasons    []string
	HadFailures      bool
}

// ComplexityAnalysis represents the result of complexity analysis.
type ComplexityAnalysis struct {
	ComplexityRatio      float64 `json:"complexity_ratio"`
	HumanTimeSavedSeconds float64 `json:"human_time_saved_seconds"`
	MoneySavedUSD         float64 `json:"money_saved_usd"`
}

// ComplexityAnalyzerConfig configures the complexity analyzer.
type ComplexityAnalyzerConfig struct {
	Model           model.BaseChatModel `json:"-" jsonschema:"-"`
	HumanHourlyRate float64             `json:"human_hourly_rate" jsonschema:"description=Human hourly rate in USD for cost savings calculation,default=50.0"`
	BaseTaskTime    time.Duration       `json:"base_task_time" jsonschema:"description=Base human time for a simple task,default=5m"`
	Timeout         time.Duration       `json:"timeout" jsonschema:"description=LLM call timeout,default=30s"`
}

// SessionSummarizer collects and summarizes activity events for a session.
// It uses bus.Replay() to build summaries on-demand at session end time.
type SessionSummarizer struct {
	bus Bus
	mu  sync.Mutex
}

// NewSessionSummarizer creates a new SessionSummarizer.
func NewSessionSummarizer(bus Bus) *SessionSummarizer {
	return &SessionSummarizer{bus: bus}
}

// GetSummary returns a summary of the session's activity.
func (s *SessionSummarizer) GetSummary(sessionID string) (*SessionSummary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	events, err := s.bus.Replay(sessionID)
	if err != nil {
		return nil, errors.Wrap(err, "activity: failed to replay bus for session summary")
	}

	summary := &SessionSummary{
		SessionID:     sessionID,
		ToolsCalled:   make([]string, 0),
		FinishReasons: make([]string, 0),
	}

	var (
		startTime        time.Time
		textBuilder      strings.Builder
		reasoningBuilder strings.Builder
		toolsMap         = make(map[string]struct{})
	)

	// addText appends text to builder, truncating at maxTextLength.
	addText := func(b *strings.Builder, text string) {
		if b.Len() >= maxTextLength {
			return
		}
		if b.Len()+len(text) > maxTextLength {
			remaining := maxTextLength - b.Len()
			b.WriteString(text[:remaining])
			b.WriteString("...")
		} else {
			b.WriteString(text)
		}
	}

	for _, e := range events {
		if startTime.IsZero() {
			startTime = e.Timestamp
		}
		summary.Duration = e.Timestamp.Sub(startTime)

		if se := resolveAny[StepEnded](e.Data); se != nil {
			summary.TotalCost += se.Cost
			summary.TotalTokens += se.Tokens.Input + se.Tokens.Output + se.Tokens.Reasoning
			summary.Steps++
			if se.Finish != "" {
				summary.FinishReasons = append(summary.FinishReasons, se.Finish)
			}
		} else if tc := resolveAny[ToolCalled](e.Data); tc != nil {
			if _, exists := toolsMap[tc.Tool]; !exists {
				toolsMap[tc.Tool] = struct{}{}
				summary.ToolsCalled = append(summary.ToolsCalled, tc.Tool)
			}
		} else if te := resolveAny[TextEnded](e.Data); te != nil {
			addText(&textBuilder, te.Text)
		} else if re := resolveAny[ReasoningEnded](e.Data); re != nil {
			addText(&reasoningBuilder, re.Text)
		} else if sf := resolveAny[StepFailed](e.Data); sf != nil {
			summary.HadFailures = true
		}
	}

	summary.TextOutput = textBuilder.String()
	summary.ReasoningContent = reasoningBuilder.String()

	return summary, nil
}

// resolveAny resolves data to *T whether data is T, *T, or neither. Returns nil
// if data is not (convertible to) T.
func resolveAny[T any](data any) *T {
	switch v := data.(type) {
	case *T:
		return v
	case T:
		return &v
	default:
		return nil
	}
}

// ComplexityAnalyzer uses an LLM to analyze session complexity.
type ComplexityAnalyzer struct {
	config ComplexityAnalyzerConfig
}

// NewComplexityAnalyzer creates a new ComplexityAnalyzer.
func NewComplexityAnalyzer(config ComplexityAnalyzerConfig) *ComplexityAnalyzer {
	if config.HumanHourlyRate == 0 {
		config.HumanHourlyRate = 50.0
	}
	if config.BaseTaskTime == 0 {
		config.BaseTaskTime = 5 * time.Minute
	}
	if config.Timeout == 0 {
		config.Timeout = 30 * time.Second
	}
	return &ComplexityAnalyzer{config: config}
}

// Analyze analyzes the session summary using an LLM.
func (a *ComplexityAnalyzer) Analyze(ctx context.Context, summary *SessionSummary) (*ComplexityAnalysis, error) {
	if a.config.Model == nil {
		return nil, errors.New("activity: complexity analyzer requires a model")
	}

	prompt := a.buildPrompt(summary)

	timeoutCtx, cancel := context.WithTimeout(ctx, a.config.Timeout)
	defer cancel()

	resp, err := a.config.Model.Generate(timeoutCtx, []*schema.Message{
		{
			Role:    schema.System,
			Content: "You are a helpful assistant that analyzes AI agent sessions and estimates cost savings. Respond with valid JSON only.",
		},
		{
			Role:    schema.User,
			Content: prompt,
		},
	})
	if err != nil {
		return nil, errors.Wrap(err, "activity: LLM complexity analysis failed")
	}

	if resp == nil {
		return nil, errors.New("activity: LLM returned empty response")
	}

	content := resp.Content

	var analysis ComplexityAnalysis
	if err := json.Unmarshal([]byte(content), &analysis); err != nil {
		return nil, errors.Wrap(err, "activity: failed to parse LLM complexity analysis JSON")
	}

	if err := a.validateAnalysis(&analysis); err != nil {
		return nil, errors.Wrap(err, "activity: invalid LLM complexity analysis")
	}

	return &analysis, nil
}

func (a *ComplexityAnalyzer) buildPrompt(summary *SessionSummary) string {
	prompt := complexityAnalysisPrompt
	prompt = strings.Replace(prompt, "{{.Duration}}", summary.Duration.String(), -1)
	prompt = strings.Replace(prompt, "{{.TotalTokens}}", fmt.Sprintf("%d", summary.TotalTokens), -1)
	prompt = strings.Replace(prompt, "{{.Steps}}", fmt.Sprintf("%d", summary.Steps), -1)
	prompt = strings.Replace(prompt, "{{.ToolsCalled}}", fmt.Sprintf("%v", summary.ToolsCalled), -1)
	prompt = strings.Replace(prompt, "{{.TextOutput}}", summary.TextOutput, -1)
	prompt = strings.Replace(prompt, "{{.ReasoningContent}}", summary.ReasoningContent, -1)
	prompt = strings.Replace(prompt, "{{.FinishReasons}}", fmt.Sprintf("%v", summary.FinishReasons), -1)
	prompt = strings.Replace(prompt, "{{.HadFailures}}", fmt.Sprintf("%t", summary.HadFailures), -1)
	prompt = strings.Replace(prompt, "{{.HumanHourlyRate}}", fmt.Sprintf("%.2f", a.config.HumanHourlyRate), -1)
	return prompt
}

func (a *ComplexityAnalyzer) validateAnalysis(analysis *ComplexityAnalysis) error {
	if analysis.ComplexityRatio < 0 || analysis.ComplexityRatio > 1 {
		return errors.New("complexity_ratio must be between 0 and 1")
	}
	if analysis.HumanTimeSavedSeconds < 0 {
		return errors.New("human_time_saved_seconds must be non-negative")
	}
	if analysis.MoneySavedUSD < 0 {
		return errors.New("money_saved_usd must be non-negative")
	}
	return nil
}

// FallbackComplexityAnalyzer uses a simple formula when LLM analysis fails.
type FallbackComplexityAnalyzer struct {
	humanHourlyRate float64
	baseTaskTime    time.Duration
}

// NewFallbackComplexityAnalyzer creates a new FallbackComplexityAnalyzer.
func NewFallbackComplexityAnalyzer(humanHourlyRate float64, baseTaskTime time.Duration) *FallbackComplexityAnalyzer {
	if humanHourlyRate == 0 {
		humanHourlyRate = 50.0
	}
	if baseTaskTime == 0 {
		baseTaskTime = 5 * time.Minute
	}
	return &FallbackComplexityAnalyzer{
		humanHourlyRate: humanHourlyRate,
		baseTaskTime:    baseTaskTime,
	}
}

// Analyze analyzes the session summary using a simple formula.
func (a *FallbackComplexityAnalyzer) Analyze(_ context.Context, summary *SessionSummary) (*ComplexityAnalysis, error) {
	tokensFactor := math.Min(1.0, float64(summary.TotalTokens)/10000.0)
	toolFactor := math.Min(1.0, float64(len(summary.ToolsCalled))*0.2)
	stepFactor := math.Min(1.0, float64(summary.Steps)*0.1)
	complexityRatio := math.Min(1.0, tokensFactor+toolFactor+stepFactor)

	if summary.HadFailures {
		complexityRatio *= 0.8
	}

	humanTimeSaved := time.Duration(complexityRatio * float64(a.baseTaskTime))
	moneySaved := (complexityRatio * float64(a.baseTaskTime.Seconds())) * a.humanHourlyRate / 3600.0

	return &ComplexityAnalysis{
		ComplexityRatio:       complexityRatio,
		HumanTimeSavedSeconds: humanTimeSaved.Seconds(),
		MoneySavedUSD:         moneySaved,
	}, nil
}

// CompositeComplexityAnalyzer tries LLM analysis and falls back to simple formula.
type CompositeComplexityAnalyzer struct {
	llm       *ComplexityAnalyzer
	fallback  *FallbackComplexityAnalyzer
}

// NewCompositeComplexityAnalyzer creates a new CompositeComplexityAnalyzer.
func NewCompositeComplexityAnalyzer(config ComplexityAnalyzerConfig) *CompositeComplexityAnalyzer {
	return &CompositeComplexityAnalyzer{
		llm:      NewComplexityAnalyzer(config),
		fallback: NewFallbackComplexityAnalyzer(config.HumanHourlyRate, config.BaseTaskTime),
	}
}

// Analyze analyzes the session summary using LLM, falling back to formula on error.
func (a *CompositeComplexityAnalyzer) Analyze(ctx context.Context, summary *SessionSummary) (*ComplexityAnalysis, error) {
	if a.llm.config.Model == nil {
		logrus.Debug("activity: LLM model not configured, using fallback complexity analyzer")
		return a.fallback.Analyze(ctx, summary)
	}

	analysis, err := a.llm.Analyze(ctx, summary)
	if err != nil {
		logrus.WithError(err).Debug("activity: LLM complexity analysis failed, using fallback")
		return a.fallback.Analyze(ctx, summary)
	}

	return analysis, nil
}