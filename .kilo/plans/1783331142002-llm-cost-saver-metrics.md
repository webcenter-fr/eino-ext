# LLM-based Cost Saver for Activity Sessions

## Problem
Current cost saver implementation uses static estimated human time per task and money by hour, which is too simplistic and doesn't work well. We need a dynamic, LLM-based approach that evaluates session complexity and calculates human time saved and money saved.

## Solution Overview
Implement an LLM-based complexity analyzer that:
1. Triggers on session end (via `EndSession` callback)
2. Collects all activity events for the session
3. Sends a structured summary to a configurable small LLM
4. Receives complexity ratio + time/money savings estimates
5. Exposes Prometheus metrics for `cost_saver_complexity_ratio`, `cost_saver_human_time_saved_seconds`, `cost_saver_money_saved_usd`
6. Falls back to a simple formula when LLM fails

## Implementation Plan

### Phase 1: Event Collection and Session Summary

**File**: `callbacks/activity/costsaver.go` (new file)

1. **Add new event type** for session end:
   ```go
   TypeSessionEnded Type = "session.ended"
   ```

2. **Add SessionEnded payload**:
   ```go
   type SessionEnded struct {
       Duration time.Duration `json:"duration"`
       Cost     float64       `json:"cost"`
       Steps    int           `json:"steps"`
       Tools    int           `json:"tools"`
   }
   ```

3. **Add SessionSummarizer** struct that:
   - Subscribes to a session's activity bus
   - Collects all events (step.ended, tool.called, text.ended, reasoning.ended)
   - Builds a structured summary including:
     - Total tokens (input, output, reasoning, cache read/write)
     - Total cost
     - Number of steps
     - List of tools called with their names
     - Text output summary (truncated to 2000 chars)
     - Reasoning content summary (truncated to 2000 chars)
     - Step finishes and any failures
   - Provides a method `GetSummary(sessionID string) (*SessionSummary, error)`

### Phase 2: LLM Complexity Analyzer

**File**: `callbacks/activity/costsaver.go` (continued)

1. **Add ComplexityAnalyzerConfig**:
   ```go
   type ComplexityAnalyzerConfig struct {
       Model           model.BaseChatModel `json:"-" jsonschema:"-"`
       HumanHourlyRate float64             `json:"human_hourly_rate" jsonschema:"description=Human hourly rate in USD for cost savings calculation,default=50.0"`
       BaseTaskTime    time.Duration       `json:"base_task_time" jsonschema:"description=Base human time for a simple task,default=5m"`
       Timeout         time.Duration       `json:"timeout" jsonschema:"description=LLM call timeout,default=30s"`
   }
   ```

2. **Add SessionSummary** struct (output from SessionSummarizer):
   ```go
   type SessionSummary struct {
       SessionID       string
       Duration        time.Duration
       TotalCost       float64
       TotalTokens     int
       Steps           int
       ToolsCalled     []string
       TextOutput      string
       ReasoningContent string
       FinishReasons   []string
       HadFailures     bool
   }
   ```

3. **Add ComplexityAnalyzer** struct that:
   - Uses a configurable LLM model
   - Accepts `SessionSummary` as input
   - Sends structured prompt (using `//go:embed` prompt file)
   - Parses LLM response to extract:
     - `complexity_ratio` (0.0-1.0)
     - `human_time_saved_seconds`
     - `money_saved_usd`
   - Returns `ComplexityAnalysis` struct

4. **Add prompt template** (`callbacks/activity/prompts/complexity_analysis.md`):
   ```
   Analyze this AI agent session and estimate cost savings.

   Session Summary:
   - Duration: {{.Duration}}
   - Total tokens: {{.TotalTokens}}
   - Steps completed: {{.Steps}}
   - Tools called: {{.ToolsCalled}}
   - Text output: {{.TextOutput}}
   - Reasoning: {{.ReasoningContent}}
   - Finish reasons: {{.FinishReasons}}
   - Had failures: {{.HadFailures}}

   Return JSON with:
   - complexity_ratio (0.0-1.0): How complex was this task compared to human work?
   - human_time_saved_seconds: How much human time was saved?
   - money_saved_usd: How much money was saved (considering human_hourly_rate={{.HumanHourlyRate}})?

   Guidelines:
   - Higher complexity = more tools, more steps, more reasoning
   - Factor in failures (retry attempts reduce savings)
   - Consider task novelty vs repetitive work
   - Complexity > 0.8 indicates highly complex automation work
   ```
   ```go
   //go:embed prompts/complexity_analysis.md
   var complexityAnalysisPrompt string
   ```

5. **Add ComplexityAnalysis** struct:
   ```go
   type ComplexityAnalysis struct {
       ComplexityRatio      float64 `json:"complexity_ratio"`
       HumanTimeSavedSeconds float64 `json:"human_time_saved_seconds"`
       MoneySavedUSD         float64 `json:"money_saved_usd"`
   }
   ```

### Phase 3: Fallback Logic

**File**: `callbacks/activity/costsaver.go` (continued)

1. **Add FallbackComplexityAnalyzer** that implements the simple formula:
   ```go
   func (a *FallbackComplexityAnalyzer) Analyze(summary *SessionSummary) (*ComplexityAnalysis, error) {
       tokensFactor := math.Min(1.0, float64(summary.TotalTokens)/10000.0)
       toolFactor := math.Min(1.0, float64(len(summary.ToolsCalled))*0.2)
       stepFactor := math.Min(1.0, float64(summary.Steps)*0.1)
       complexityRatio := math.Min(1.0, tokensFactor+toolFactor+stepFactor)

       humanTimeSaved := time.Duration(complexityRatio * float64(a.baseTaskTime))
       moneySaved := (complexityRatio * float64(a.baseTaskTime).Seconds()) * a.humanHourlyRate / 3600.0

       return &ComplexityAnalysis{
           ComplexityRatio:      complexityRatio,
           HumanTimeSavedSeconds: humanTimeSaved.Seconds(),
           MoneySavedUSD:         moneySaved,
       }, nil
   }
   ```

2. **Add composite analyzer** that:
   - Tries LLM analyzer first
   - Falls back to simple formula on error or timeout
   - Logs the fallback event

### Phase 4: Prometheus Metrics

**File**: `callbacks/activity/metrics/costsaver.go` (new file)

1. **Add CostSaverCollector** struct:
   ```go
   type CostSaverCollector struct {
       complexityRatio      *prometheus.GaugeVec
       humanTimeSavedSeconds *prometheus.GaugeVec
       moneySavedUSD         *prometheus.GaugeVec
       fallbackCount         *prometheus.CounterVec
   }
   ```

2. **Register metrics**:
   ```go
   func NewCostSaverCollector(reg prometheus.Registerer) (*CostSaverCollector, error) {
       c := &CostSaverCollector{
           complexityRatio: prometheus.NewGaugeVec(prometheus.GaugeOpts{
               Name: "cost_saver_complexity_ratio",
               Help: "Complexity ratio of the session (0.0-1.0) as computed by LLM analyzer.",
           }, []string{"session_id", "agent"}),
           humanTimeSavedSeconds: prometheus.NewGaugeVec(prometheus.GaugeOpts{
               Name: "cost_saver_human_time_saved_seconds",
               Help: "Estimated human time saved in seconds.",
           }, []string{"session_id", "agent"}),
           moneySavedUSD: prometheus.NewGaugeVec(prometheus.GaugeOpts{
               Name: "cost_saver_money_saved_usd",
               Help: "Estimated money saved in USD based on human time and hourly rate.",
           }, []string{"session_id", "agent"}),
           fallbackCount: prometheus.NewCounterVec(prometheus.CounterOpts{
               Name: "cost_saver_fallback_count_total",
               Help: "Count of fallback to simple formula when LLM analysis failed.",
           }, []string{"reason"}),
       }
       // Register metrics...
       return c, nil
   }
   ```

3. **Add RecordAnalysis** method:
   ```go
   func (c *CostSaverCollector) RecordAnalysis(sessionID, agent string, analysis *ComplexityAnalysis) {
       c.complexityRatio.WithLabelValues(sessionID, agent).Set(analysis.ComplexityRatio)
       c.humanTimeSavedSeconds.WithLabelValues(sessionID, agent).Set(analysis.HumanTimeSavedSeconds)
       c.moneySavedUSD.WithLabelValues(sessionID, agent).Set(analysis.MoneySavedUSD)
   }
   ```

### Phase 5: Integration with Session End

**File**: `callbacks/activity/metrics/collector.go` (modify existing)

1. **Add CostSaver fields** to `Collector`:
   ```go
   type Collector struct {
       tokens       *prometheus.CounterVec
       cost         *prometheus.CounterVec
       costSaver    *CostSaverCollector       // optional
       summarizer   *SessionSummarizer        // optional
       analyzer     *CompositeComplexityAnalyzer // optional
   }
   ```

2. **Add configuration option**:
   ```go
   type CostSaverConfig struct {
       AnalyzerConfig *ComplexityAnalyzerConfig `json:"analyzer_config"`
       Enabled        bool                      `json:"enabled"`
   }

   func WithCostSaver(cfg CostSaverConfig) Option {
       return func(c *Collector) {
           if cfg.Enabled && cfg.AnalyzerConfig != nil {
               c.costSaver, _ = NewCostSaverCollector(c.reg)
               c.summarizer = NewSessionSummarizer(c.bus)
               c.analyzer = NewCompositeComplexityAnalyzer(cfg.AnalyzerConfig)
           }
       }
   }
   ```

3. **Modify Watch** to handle `session.ended` events:
   ```go
   func (c *Collector) Watch(ctx context.Context, bus activity.Bus, sessionID string) {
       // ... existing code ...
       switch data := e.Data.(type) {
       case activity.SessionEnded:
           if c.costSaver != nil && c.summarizer != nil && c.analyzer != nil {
               go c.handleSessionEnded(ctx, sessionID, e.Agent, data)
           }
       // ... existing cases ...
       }
   }
   ```

4. **Add handleSessionEnded** method:
   ```go
   func (c *Collector) handleSessionEnded(ctx context.Context, sessionID, agent string, ended activity.SessionEnded) {
       summary, err := c.summarizer.GetSummary(sessionID)
       if err != nil {
           logrus.WithError(err).Warn("failed to get session summary for cost saver")
           return
       }

       analysis, err := c.analyzer.Analyze(ctx, summary)
       if err != nil {
           logrus.WithError(err).Warn("LLM complexity analysis failed, using fallback")
           return // analyzer handles fallback internally
       }

       c.costSaver.RecordAnalysis(sessionID, agent, analysis)
   }
   ```

### Phase 6: Emit Session Ended Events

**File**: `callbacks/activity/handler.go` (modify existing)

1. **Add session tracking** to Handler:
   ```go
   type Handler struct {
       bus        Bus
       pricer     Pricer
       ids        atomic.Uint64
       lastAgent  sync.Map
       sessionStart sync.Map // sessionID -> start time
   }
   ```

2. **Track session start** in `OnStart`:
   ```go
   func (h *Handler) OnStart(ctx context.Context, info *callbacks.RunInfo, input callbacks.CallbackInput) context.Context {
       sessionID, _ := SessionFromContext(ctx)
       if sessionID != "" {
           if _, loaded := h.sessionStart.LoadOrStore(sessionID, time.Now()); !loaded {
               // New session started
           }
       }
       // ... existing code ...
   }
   ```

3. **Emit session.ended** after last step:
   - Detect session end by monitoring activity in the bus
   - After N seconds of inactivity or explicit callback
   - Calculate duration, total cost, step count, tool count
   - Publish `SessionEnded` event

## Testing Strategy

### Unit Tests

1. **SessionSummarizer tests**:
   - Mock bus with known events
   - Verify summary extraction
   - Test edge cases (empty session, long text, failures)

2. **ComplexityAnalyzer tests**:
   - Mock LLM responses
   - Verify JSON parsing
   - Test error handling and fallback

3. **Fallback analyzer tests**:
   - Verify formula calculations
   - Test boundary conditions (zero tokens, many tools)

4. **CostSaverCollector tests**:
   - Verify metric registration
   - Test metric values

### Integration Tests

1. **End-to-end flow**:
   - Create session with activity
   - End session
   - Verify complexity analysis runs
   - Check Prometheus metrics

2. **LLM failure scenarios**:
   - Timeout
   - Invalid JSON response
   - Network error
   - Verify fallback works

## Configuration Example

```go
// Create cost saver analyzer
costSaverConfig := activity.CostSaverConfig{
    Enabled: true,
    AnalyzerConfig: &activity.ComplexityAnalyzerConfig{
        Model:           mySmallLLM,  // e.g., gpt-4o-mini or claude-haiku
        HumanHourlyRate: 50.0,       // $50/hour
        BaseTaskTime:    5 * time.Minute,
        Timeout:         30 * time.Second,
    },
}

// Create collector with cost saver
coll := metrics.NewCollector(prometheus.DefaultRegisterer,
    metrics.WithCostSaver(costSaverConfig))
```

## Metrics Documentation

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `cost_saver_complexity_ratio` | Gauge | `session_id`, `agent` | Complexity ratio (0.0-1.0) computed by LLM analyzer |
| `cost_saver_human_time_saved_seconds` | Gauge | `session_id`, `agent` | Estimated human time saved in seconds |
| `cost_saver_money_saved_usd` | Gauge | `session_id`, `agent` | Estimated money saved in USD |
| `cost_saver_fallback_count_total` | Counter | `reason` | Count of fallbacks (reason: timeout, error, invalid_json) |

## Open Questions

1. **Session end detection**: How do we reliably detect when a session has ended? Options:
   - Timeout-based (no events for X seconds)
   - Explicit callback from user/agent
   - Memory agent's `EndSession` as trigger

2. **Session summary storage**: Should we store session summaries in memory or persist them? Memory is simpler but limits analysis to active sessions.

3. **Metric cardinality**: Using `session_id` as a label could cause high cardinality. Consider using agent-level aggregation instead.

## Dependencies

- Existing `prometheus/client_golang` dependency
- Existing `cloudwego/eino` model interface
- No new external dependencies required

## Backward Compatibility

- Cost saver is opt-in via configuration (default disabled)
- Existing Collector API unchanged
- No breaking changes to activity events