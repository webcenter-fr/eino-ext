# Plan: Cost saver must produce zero savings for trivial tasks

## Problem

The "cost saver" feature ALWAYS produces a nonzero savings estimate, even for
trivial tasks like "hello" or a one-shot tool call. Two shared root causes:

1. **Metrics path over-fires.** `callbacks/activity/metrics/collector.go`
   `Watch` runs the analyzer on every `SessionEnded` with no "real task" guard,
   while `libs/costtrack/tracker.go` only runs it when `snap.isReal`
   (`toolCalled`). The two entry points are inconsistent.
2. **Fallback formula has no zero floor.** `stepFactor = min(1, Steps*0.1)`
   gives `0.1` for the single LLM turn every session has, so `complexityRatio
   >= 0.1` for ANY session → `humanTimeSaved >= 30s`, `moneySaved >= $0.42`
   with `BaseTaskTime=5m`. No threshold below which savings are zero.
3. **LLM prompt presupposes savings** and conflates complexity with
   human-time-saved; it never instructs returning zeros for trivial tasks.
4. **Conceptual flaw:** `complexityRatio * baseTaskTime` assumes every task
   takes a human 5min, so even `0.1` → 30s saved for "hello" (a 1s human task).

## Goal

A trivial session (no tools, single step, low tokens, trivial text) MUST yield
`complexity_ratio=0`, `human_time_saved_seconds=0`, `money_saved_usd=0` from
both the fallback formula (hard guard) and the LLM path (prompt guidance). The
two entry points must be consistent: both skip analysis for sessions that
called no tools.

## Design decisions (resolved)

- **Discriminator for "real work" = at least one tool called.** This matches
  the existing `costtrack` `isReal = toolCalled` semantics already in the
  codebase, so it is the established notion of a real task. A single tool call
  (e.g., "check OpenSearch cluster health") IS real automation and MUST still
  produce nonzero savings. "Trivial" = no tools.
- **Approach for entry-point consistency (req #1):** Do BOTH, for different
  reasons:
  - Push the zero floor into the **fallback analyzer** (hard guard) and the
    trivial-task guidance into the **LLM prompt**. This is the single source of
    truth for analyzer correctness — no duplication across entry points.
  - Add a **local `toolCalled` guard in `metrics.Watch`** so the metrics path
    skips the analyzer (and any LLM call) entirely for no-tool sessions, matching
    `costtrack`'s skip. This is a one-bool local guard, NOT worth extracting to
    `libs/toolkit/` (would be over-engineering; the shared correctness logic
    already lives in the analyzer).
- **Config:** Do NOT add `MinSteps`/`MinTokens`/`MinTools` to
  `ComplexityAnalyzerConfig`. Keep trivial-task thresholds as **unexported
  constants** to preserve the public API (`NewFallbackComplexityAnalyzer(
  humanHourlyRate, baseTaskTime)` and `NewComplexityAnalyzer` signatures stay
  unchanged). No config → no `validate.Struct` change needed.
- **LLM path uses prompt guidance only**, NOT a hard zero guard, so the LLM
  keeps judgment for direct callers. In both production entry points a no-tool
  session never reaches the LLM analyzer (metrics guard / costtrack `isReal`).

---

## Change 1 — Fallback formula zero floor + stepFactor fix

**File:** `callbacks/activity/costsaver.go`

### 1a. Add constants after `const maxTextLength = 2000` (line 19)

```go
const (
	// complexityTokenDivisor is the token count at which the token factor
	// saturates to 1.0.
	complexityTokenDivisor = 10000.0
	// complexityToolWeight is the per-tool contribution to the tool factor.
	complexityToolWeight = 0.2
	// complexityStepWeight is the per-step contribution (beyond the first
	// complexityFreeSteps) to the step factor.
	complexityStepWeight = 0.1
	// complexityFreeSteps is the number of initial steps that contribute
	// nothing, so a single LLM turn — present in every session, including
	// trivial ones like "hello" — does not by itself imply savings.
	complexityFreeSteps = 1
	// complexityFailurePenalty scales complexityRatio down when the session
	// had failures (retries reduce net human-time savings).
	complexityFailurePenalty = 0.8
)
```

### 1b. Replace `FallbackComplexityAnalyzer.Analyze` (currently lines 260-279)

Signature is UNCHANGED (`func (a *FallbackComplexityAnalyzer) Analyze(_
context.Context, summary *SessionSummary) (*ComplexityAnalysis, error)`). The
`math` import stays used.

```go
// Analyze analyzes the session summary using a simple formula.
//
// A session that called no tools did not replace any human tool-use work, so it
// is treated as trivial: complexityRatio, human-time-saved, and money-saved
// are all zero. This is the zero floor that prevents greetings, chitchat, and
// single-turn factual answers from producing spurious savings.
//
// For sessions that did call tools, the complexity ratio is the sum of:
//   - tokensFactor: min(1, TotalTokens / complexityTokenDivisor)
//   - toolFactor:   min(1, len(ToolsCalled) * complexityToolWeight)
//   - stepFactor:   min(1, max(0, Steps - complexityFreeSteps) * complexityStepWeight)
//
// The first complexityFreeSteps steps are free because every session has at
// least one LLM turn that exists regardless of whether real work was done.
// HadFailures scales the result down by complexityFailurePenalty.
func (a *FallbackComplexityAnalyzer) Analyze(_ context.Context, summary *SessionSummary) (*ComplexityAnalysis, error) {
	// Trivial-task floor: no tools means no automation, no human time saved.
	if len(summary.ToolsCalled) == 0 {
		return &ComplexityAnalysis{
			ComplexityRatio:       0,
			HumanTimeSavedSeconds: 0,
			MoneySavedUSD:         0,
		}, nil
	}

	tokensFactor := math.Min(1.0, float64(summary.TotalTokens)/complexityTokenDivisor)
	toolFactor := math.Min(1.0, float64(len(summary.ToolsCalled))*complexityToolWeight)
	extraSteps := summary.Steps - complexityFreeSteps
	if extraSteps < 0 {
		extraSteps = 0
	}
	stepFactor := math.Min(1.0, float64(extraSteps)*complexityStepWeight)
	complexityRatio := math.Min(1.0, tokensFactor+toolFactor+stepFactor)

	if summary.HadFailures {
		complexityRatio *= complexityFailurePenalty
	}

	humanTimeSaved := time.Duration(complexityRatio * float64(a.baseTaskTime))
	moneySaved := (complexityRatio * float64(a.baseTaskTime.Seconds())) * a.humanHourlyRate / 3600.0

	return &ComplexityAnalysis{
		ComplexityRatio:       complexityRatio,
		HumanTimeSavedSeconds: humanTimeSaved.Seconds(),
		MoneySavedUSD:         moneySaved,
	}, nil
}
```

### Justification of thresholds

- **No-tools → 0 (hard floor):** tools are the established `isReal` signal in
  this repo. A no-tool session replaced no human tool-use work, so savings are
  zero by definition. Fixes root causes 2 and 4 for the fallback.
- **`complexityFreeSteps = 1`:** every session has one LLM turn regardless of
  real work, so the first step must not contribute. `stepFactor` now uses
  `max(0, Steps - 1)`: a single step contributes 0; 2 steps → 0.1; etc. This
  removes the unconditional 0.1 that made every session nonzero.
- **`complexityTokenDivisor = 10000`, `complexityToolWeight = 0.2`,
  `complexityFailurePenalty = 0.8`:** unchanged from the original formula
  (preserved as constants for clarity/testability).

### Recomputed expectations for existing fallback test cases

| Case | Tokens | Tools | Steps | Failures | Old ratio | New ratio |
|---|---|---|---|---|---|---|
| simple session | 1000 | 1 | 1 | no | 0.4 | **0.3** (0.1+0.2+0) |
| complex session | 5000 | 3 | 5 | no | 1.0 | 1.0 (0.5+0.6+0.4→1.5→1.0) |
| session with failures | 5000 | 2 | 3 | yes | 0.8 | 0.8 (0.5+0.4+0.2→1.1→1.0×0.8) |

Only the "simple session" expected value changes: `0.4 → 0.3`. The other two are
unchanged.

---

## Change 2 — LLM prompt rewrite

**File:** `callbacks/activity/prompts/complexity_analysis.md`

Full replacement. All `{{.X}}` placeholders are preserved exactly so
`buildPrompt` (in `costsaver.go`) works unchanged. The `//go:embed` directive
already in `costsaver.go` keeps this compiled into the binary (no runtime I/O),
per CONTRIBUTING.md.

```markdown
You analyze an AI agent session to estimate how much **human** work it
replaced, then express that as cost savings. Savings are NOT guaranteed: many
sessions did nothing a human would have spent meaningful time on, and for those
you MUST return zeros.

Session Summary:
- Duration (AI wall-clock): {{.Duration}}
- Total tokens: {{.TotalTokens}}
- Steps completed: {{.Steps}}
- Tools called: {{.ToolsCalled}}
- Text output: {{.TextOutput}}
- Reasoning: {{.ReasoningContent}}
- Finish reasons: {{.FinishReasons}}
- Had failures: {{.HadFailures}}
- Human hourly rate (USD): {{.HumanHourlyRate}}

Step 1 — Classify the task as trivial or real.
A session is TRIVIAL (no meaningful human work replaced) when ANY of these hold:
- No tools were called AND the output is a greeting, chitchat, a one-line
  factual answer, an acknowledgment, or a clarification question.
- The task is something a competent human would also do in a few seconds
  (e.g., "hello", "thanks", "what is 2+2", "summarize this in one word").

A session is REAL when the AI performed work a competent human would have spent
measurable time on: running queries or commands, calling tools/APIs, multi-step
reasoning over data, producing structured artifacts, debugging, etc.

Step 2 — Estimate human time.
If the task is TRIVIAL, set human_time_saved_seconds = 0 and skip the rest
(savings are zero by definition).
If the task is REAL, estimate how many seconds a competent human would take to
do the SAME task manually (opening tools, running the queries, reading
results, writing the output). Call this `human_time`.

Step 3 — Compute savings.
- complexity_ratio (0.0-1.0): how much of a fully automated, complex task this
  represents. 0.0 = trivial / no automation. 0.5 = moderate multi-step work.
  ~1.0 = heavy, multi-tool, multi-step automation a human would spend many
  minutes on.
- human_time_saved_seconds = max(0, human_time − AI wall-clock seconds). For
  trivial tasks this is 0.
- money_saved_usd = human_time_saved_seconds × ({{.HumanHourlyRate}} / 3600).
  For trivial tasks this is 0.

Guidelines:
- Tool use is the strongest signal of real automation. A session with no tools
  and only small-talk output is almost always trivial → return all zeros.
- More tools, more steps, and more reasoning generally mean more human time
  replaced, but only when they produced real work — not when they just churned.
- Failures/retries reduce net savings: if the AI struggled, the human-time
  advantage shrinks. Lower human_time_saved_seconds and complexity_ratio
  accordingly.
- Do not invent savings. If in doubt about whether a human would spend real
  time, lean toward 0.
- Respond with valid JSON only, no prose, with exactly these keys:
  complexity_ratio, human_time_saved_seconds, money_saved_usd.
```

No change to `validateAnalysis` (it already accepts `0/0/0` within ranges).
No hard guard is added to `ComplexityAnalyzer.Analyze` — the LLM path relies on
this prompt guidance, per the design decision above.

---

## Change 3 — Trivial-task guard in `metrics.WithCostSaver` path

**File:** `callbacks/activity/metrics/collector.go`

### 3a. Update `Watch` loop

Add a per-session `toolCalled` bool, handle `ToolCalled` events (both value and
pointer, mirroring how `SessionEnded` is handled), and require `toolCalled`
before launching `handleSessionEnded`.

Replace the loop body (currently lines 182-206) with:

```go
	currentModel := make(map[string]string) // agent -> most recently started model
	toolCalled := false
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-ch:
			if !ok {
				return
			}
			switch data := e.Data.(type) {
			case activity.StepStarted:
				currentModel[e.Agent] = data.Model
			case activity.StepEnded:
				c.Observe(currentModel[e.Agent], e.Agent, data)
			case activity.ToolCalled:
				toolCalled = true
			case *activity.ToolCalled:
				toolCalled = true
			case activity.SessionEnded:
				if c.costSaver != nil && c.summarizer != nil && c.analyzer != nil && toolCalled {
					go c.handleSessionEnded(ctx, sessionID, e.Agent, data)
				}
			case *activity.SessionEnded:
				if c.costSaver != nil && c.summarizer != nil && c.analyzer != nil && toolCalled {
					go c.handleSessionEnded(ctx, sessionID, e.Agent, *data)
				}
			}
		}
	}
```

`toolCalled` is local to one `Watch` call (one session), so no per-agent map is
needed — a session is the unit of "real work", matching `costtrack`'s
session-wide `isReal`.

### 3b. Update the `Watch` doc comment

Append to the existing paragraph (lines 173-174) that ends with "...record cost
saver metrics.":

> Cost saver analysis only runs for sessions that called at least one tool;
> no-tool sessions are skipped, matching the `costtrack` facade's `isReal`
> guard, so trivial sessions neither invoke the analyzer nor increment
> `cost_saver_runs_total`.

`handleSessionEnded` and `WithCostSaver` are unchanged. No new imports needed
(`activity` is already imported).

---

## Change 4 — Tests: `callbacks/activity/costsaver_test.go`

### 4a. Replace `TestFallbackComplexityAnalyzer_Analyze` (lines 252-302)

Use an `expectSaved` flag so trivial cases assert exact zeros and real cases
assert `> 0`. Note the "simple session" expected ratio changes to `0.3`.

```go
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
				Steps:       1,
				ToolsCalled: []string{},
				TotalTokens: 50,
				TextOutput:   "Hello! How can I help?",
			},
			expectedRatio: 0,
			expectSaved:   false,
		},
		{
			name: "single-step with one tool, no tokens (real one-shot automation)",
			summary: &SessionSummary{
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
			assert.Equal(t, tt.expectedRatio, analysis.ComplexityRatio)
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
```

### 4b. Add LLM-path zero-validation test

Proves the LLM path accepts an all-zero response for a trivial summary (the
prompt guidance target). Add:

```go
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
```

Existing tests (`TestComplexityAnalyzer_Analyze`, `TestCompositeComplexityAnalyzer_LLM`,
`TestCompositeComplexityAnalyzer_Fallback`, `TestComplexityAnalyzerConfig_Defaults`,
etc.) are unaffected: `TestCompositeComplexityAnalyzer_Fallback` builds a
summary with 1 step, 0 tools, 300 tokens — under the new formula that is now a
trivial session returning 0, and its existing assertions are only
`ComplexityRatio >= 0`, `HumanTimeSavedSeconds >= 0`, `MoneySavedUSD >= 0`,
so it still passes. No change needed there.

---

## Change 5 — Tests: `callbacks/activity/metrics/costsaver_test.go`

Add two tests. The `gatherGaugeValue` helper already exists in this file. A
`gatherValue` (counter) helper exists in `collector_test.go` (same package).

```go
func TestCollector_CostSaver_SkipsNoToolSession(t *testing.T) {
	bus, err := activity.NewBus(activity.Config{})
	if err != nil {
		t.Fatalf("NewBus: %v", err)
	}
	t.Cleanup(func() { bus.Close() })

	reg := prometheus.NewRegistry()
	c, err := NewCollector(reg, WithCostSaver(CostSaverConfig{
		Enabled: true,
		AnalyzerConfig: &activity.ComplexityAnalyzerConfig{
			HumanHourlyRate: 50.0,
			BaseTaskTime:    5 * time.Minute,
			// Model nil => CompositeComplexityAnalyzer uses fallback formula.
		},
	}, bus))
	if err != nil {
		t.Fatalf("NewCollector: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go c.Watch(ctx, bus, "s")
	time.Sleep(50 * time.Millisecond) // let Subscribe register

	// No-tool session: one LLM turn, no tools, then session.ended.
	bus.Publish(ctx, activity.Event{SessionID: "s", Agent: "coder", Type: activity.TypeStepStarted, Data: activity.StepStarted{Model: "gpt-5"}})
	bus.Publish(ctx, activity.Event{SessionID: "s", Agent: "coder", Type: activity.TypeStepEnded, Data: activity.StepEnded{Tokens: activity.Tokens{Input: 100, Output: 50}}})
	bus.Publish(ctx, activity.Event{SessionID: "s", Agent: "coder", Type: activity.TypeSessionEnded, Data: activity.SessionEnded{Duration: 2 * time.Second, Steps: 1, Tools: 0}})

	// If the guard were broken, handleSessionEnded would run within tens of ms.
	// Poll 500ms and fail fast if cost_saver_runs_total ever increments.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if got := gatherValue(t, reg, "cost_saver_runs_total", map[string]string{"agent": "coder"}); got > 0 {
			t.Fatalf("cost saver ran for a no-tool session: cost_saver_runs_total = %v", got)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := gatherValue(t, reg, "cost_saver_runs_total", map[string]string{"agent": "coder"}); got != 0 {
		t.Errorf("cost_saver_runs_total = %v, want 0 for no-tool session", got)
	}
}

func TestCollector_CostSaver_RunsForToolSession(t *testing.T) {
	bus, err := activity.NewBus(activity.Config{})
	if err != nil {
		t.Fatalf("NewBus: %v", err)
	}
	t.Cleanup(func() { bus.Close() })

	reg := prometheus.NewRegistry()
	c, err := NewCollector(reg, WithCostSaver(CostSaverConfig{
		Enabled: true,
		AnalyzerConfig: &activity.ComplexityAnalyzerConfig{
			HumanHourlyRate: 50.0,
			BaseTaskTime:    5 * time.Minute,
		},
	}, bus))
	if err != nil {
		t.Fatalf("NewCollector: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go c.Watch(ctx, bus, "s")
	time.Sleep(50 * time.Millisecond)

	// Real session: one tool call, one step (1000 tokens => ratio 0.3 exactly).
	bus.Publish(ctx, activity.Event{SessionID: "s", Agent: "coder", Type: activity.TypeStepStarted, Data: activity.StepStarted{Model: "gpt-5"}})
	bus.Publish(ctx, activity.Event{SessionID: "s", Agent: "coder", Type: activity.TypeToolCalled, Data: activity.ToolCalled{Tool: "opensearch-health"}})
	bus.Publish(ctx, activity.Event{SessionID: "s", Agent: "coder", Type: activity.TypeStepEnded, Data: activity.StepEnded{Tokens: activity.Tokens{Input: 500, Output: 500}}})
	bus.Publish(ctx, activity.Event{SessionID: "s", Agent: "coder", Type: activity.TypeSessionEnded, Data: activity.SessionEnded{Duration: 3 * time.Second, Steps: 1, Tools: 1}})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got := gatherValue(t, reg, "cost_saver_runs_total", map[string]string{"agent": "coder"}); got == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := gatherValue(t, reg, "cost_saver_runs_total", map[string]string{"agent": "coder"}); got != 1 {
		t.Fatalf("cost_saver_runs_total = %v, want 1 for tool session", got)
	}
	// Fallback ratio for 1 tool, 1 step, 1000 tokens = 0.1 + 0.2 + 0 = 0.3
	// => humanTimeSaved = 0.3 * 300s = 90s; moneySaved = 0.3*300*50/3600 = 1.25.
	if got := gatherGaugeValue(t, reg, "cost_saver_complexity_ratio", map[string]string{"session_id": "s", "agent": "coder"}); got != 0.3 {
		t.Errorf("cost_saver_complexity_ratio = %v, want 0.3", got)
	}
	if got := gatherGaugeValue(t, reg, "cost_saver_human_time_saved_seconds", map[string]string{"session_id": "s", "agent": "coder"}); got != 90 {
		t.Errorf("cost_saver_human_time_saved_seconds = %v, want 90", got)
	}
	if got := gatherGaugeValue(t, reg, "cost_saver_money_saved_usd", map[string]string{"session_id": "s", "agent": "coder"}); got != 1.25 {
		t.Errorf("cost_saver_money_saved_usd = %v, want 1.25", got)
	}
}
```

These tests need `context` and `time` imports; add them to the existing import
block if not present (the file currently imports only `testing`, `prometheus`,
`activity`).

---

## Change 6 — Tests: `libs/costtrack/tracker_test.go`

`gatherMetric` and `testTracker` helpers already exist. Add two tests. The
no-tool case is the required new assertion; the tool case is a positive control
proving the `isReal` guard (not a total absence of savings) is the
discriminator.

```go
func TestTracker_NoToolSession_NoHumanSavings(t *testing.T) {
	bus, err := activity.NewBus(activity.Config{})
	if err != nil {
		t.Fatalf("NewBus: %v", err)
	}
	t.Cleanup(func() { bus.Close() })

	holder := new(atomic.Pointer[modelsdev.Catalog])
	holder.Store(&modelsdev.Catalog{})

	reg := prometheus.NewRegistry()
	tracker, err := NewTracker(context.Background(), &Config{
		Bus:             bus,
		PricingProvider: "anthropic",
		Resolve:         func(gw string) (string, string, bool) { return "", "", false },
		CatalogHolder:   holder,
		Registry:        reg,
		Savings: activity.ComplexityAnalyzerConfig{
			HumanHourlyRate: 50.0,
			BaseTaskTime:    5 * time.Minute,
		},
	})
	if err != nil {
		t.Fatalf("NewTracker: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go tracker.Watch(ctx, "s1")
	time.Sleep(50 * time.Millisecond)

	// No-tool session: one LLM turn, then a terminal event (answer.ended)
	// triggers the facade's synthetic session.ended.
	bus.Publish(ctx, activity.Event{SessionID: "s1", Agent: "coder", Type: activity.TypeStepStarted, Data: activity.StepStarted{Model: "claude-opus-4-5"}})
	bus.Publish(ctx, activity.Event{SessionID: "s1", Agent: "coder", Type: activity.TypeStepEnded, Data: activity.StepEnded{Tokens: activity.Tokens{Input: 100, Output: 50}}})
	bus.Publish(ctx, activity.Event{SessionID: "s1", Agent: "coder", Type: activity.Type("answer.ended")})

	// human_savings_usd_total must stay 0; fail fast if it ever increments.
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if got := gatherMetric(t, reg, "human_savings_usd_total", map[string]string{"agent": "coder"}); got > 0 {
			t.Fatalf("human_savings_usd_total incremented for a no-tool session: %v", got)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := gatherMetric(t, reg, "cost_saver_runs_total", map[string]string{"agent": "coder"}); got != 0 {
		t.Errorf("cost_saver_runs_total = %v, want 0 for no-tool session", got)
	}
}

func TestTracker_ToolSession_RecordsHumanSavings(t *testing.T) {
	bus, err := activity.NewBus(activity.Config{})
	if err != nil {
		t.Fatalf("NewBus: %v", err)
	}
	t.Cleanup(func() { bus.Close() })

	holder := new(atomic.Pointer[modelsdev.Catalog])
	holder.Store(&modelsdev.Catalog{})

	reg := prometheus.NewRegistry()
	tracker, err := NewTracker(context.Background(), &Config{
		Bus:             bus,
		PricingProvider: "anthropic",
		Resolve:         func(gw string) (string, string, bool) { return "", "", false },
		CatalogHolder:   holder,
		Registry:        reg,
		Savings: activity.ComplexityAnalyzerConfig{
			HumanHourlyRate: 50.0,
			BaseTaskTime:    5 * time.Minute,
		},
	})
	if err != nil {
		t.Fatalf("NewTracker: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go tracker.Watch(ctx, "s1")
	time.Sleep(50 * time.Millisecond)

	// Real session: one tool call, 1000 tokens => fallback ratio 0.3,
	// moneySaved = 1.25.
	bus.Publish(ctx, activity.Event{SessionID: "s1", Agent: "coder", Type: activity.TypeStepStarted, Data: activity.StepStarted{Model: "claude-opus-4-5"}})
	bus.Publish(ctx, activity.Event{SessionID: "s1", Agent: "coder", Type: activity.TypeToolCalled, Data: activity.ToolCalled{Tool: "opensearch-health"}})
	bus.Publish(ctx, activity.Event{SessionID: "s1", Agent: "coder", Type: activity.TypeStepEnded, Data: activity.StepEnded{Tokens: activity.Tokens{Input: 500, Output: 500}}})
	bus.Publish(ctx, activity.Event{SessionID: "s1", Agent: "coder", Type: activity.Type("answer.ended")})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got := gatherMetric(t, reg, "human_savings_usd_total", map[string]string{"agent": "coder"}); got > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := gatherMetric(t, reg, "human_savings_usd_total", map[string]string{"agent": "coder"}); got != 1.25 {
		t.Errorf("human_savings_usd_total = %v, want 1.25 for tool session", got)
	}
	if got := gatherMetric(t, reg, "cost_saver_runs_total", map[string]string{"agent": "coder"}); got != 1 {
		t.Errorf("cost_saver_runs_total = %v, want 1 for tool session", got)
	}
}
```

The existing `TestPrometheusRecorder_HumanSavings` (direct `RecordAnalysis`
call) is unaffected and stays as-is.

---

## Change 7 — README updates

### `callbacks/activity/metrics/README.md`

In the **Design** section, after the bullet that ends with "...via `Replay()`."
(lines 110-112), add a bullet:

> - The cost saver only analyzes sessions that called at least one tool. A
>   no-tool session (greetings, chitchat, single-turn factual answers) is
>   treated as trivial: the analyzer is not invoked, `cost_saver_runs_total` is
>   not incremented, and the savings gauges remain unset/zero. The fallback
>   formula also returns all zeros for no-tool sessions.

### `libs/costtrack/README.md`

In the **Metrics** table row for `human_savings_usd_total` (line 83), append to
the Description:

> Only incremented for sessions that used at least one tool (a "real" task);
> trivial no-tool sessions yield zero savings.

And add a short subsection after the Metrics table:

> ## Cost saver
>
> `human_savings_usd_total` and `cost_saver_runs_total` are only incremented for
> sessions that called at least one tool — the facade's `isReal` guard. Trivial
> no-tool sessions (greetings, chitchat) produce zero savings and do not run
> the analyzer. This matches the standalone `metrics.WithCostSaver` path.

---

## Edge cases (handled by the design)

| Edge case | Behavior | Why |
|---|---|---|
| Empty summary (no events) | fallback → 0/0/0 | `ToolsCalled` empty → zero floor |
| Only `StepStarted`, no `StepEnded` | fallback → 0/0/0 | no tools → zero floor (Steps=0, Tools=[]) |
| `ToolCalled` without `StepEnded` | fallback → ratio 0.2 (nonzero) | a tool WAS used = real automation; `tokensFactor=0`, `toolFactor=0.2`, `stepFactor=max(0,0-1)*0.1=0` |
| Failures-only, no tools | fallback → 0/0/0 | zero floor precedes the failure penalty |
| Very long trivial text, no tools | fallback → 0/0/0 | zero floor ignores text length |
| Metrics path, no-tool session.ended | skipped entirely | `toolCalled` guard in `Watch` |
| Metrics path, tool session.ended | runs analyzer | `toolCalled == true` |
| costtrack no-tool session.ended | skipped (existing) | `snap.isReal == false` |
| LLM path, no-tool summary (direct call) | LLM returns zeros via prompt guidance | no hard guard by design |

---

## AGENTS.md / CONTRIBUTING.md compliance

- **No license banners** added to any file.
- **`emperror.dev/errors`:** no new error paths in this change; the fallback
  continues to return `nil` error. Existing error wrapping in `costsaver.go` is
  untouched.
- **`validate.Struct`:** NOT applicable — no `Config` fields are added
  (`ComplexityAnalyzerConfig` is unchanged), so no constructor validation
  changes. Thresholds are unexported constants, not config.
- **`//go:embed`** for the prompt is already used (`complexityAnalysisPrompt`);
  the rewrite just changes the embedded file content. No Go change needed for
  embedding.
- **Naming:** unexported constants use camelCase (`complexityTokenDivisor`,
  etc.); `OpenSearch`, `HumanHourlyRate`, `BaseTaskTime` already follow
  conventions. New tool name in tests is `opensearch-health` (lowercase, a tool
  identifier, not an exported Go identifier).
- **No duplication of helpers:** the zero floor lives once in
  `FallbackComplexityAnalyzer.Analyze`; the metrics guard is a local one-bool
  check, not a duplicated helper.
- **Prompts kept in Markdown** under `prompts/` and embedded via `//go:embed`
  (CONTRIBUTING.md "Prompts" convention) — already the case.

---

## Validation steps

```bash
go build ./...
go vet ./...
go test ./callbacks/activity/... ./libs/costtrack/... ./callbacks/activity/metrics/...
```

All three must pass. Specifically:
- `TestFallbackComplexityAnalyzer_Analyze` (updated table) — trivial cases
  return 0/0/0; "simple session" returns 0.3; complex/failures unchanged.
- `TestComplexityAnalyzer_TrivialZerosPassValidation` — LLM zeros accepted.
- `TestCollector_CostSaver_SkipsNoToolSession` — no run for no-tool session.
- `TestCollector_CostSaver_RunsForToolSession` — ratio 0.3, 90s, $1.25, runs=1.
- `TestTracker_NoToolSession_NoHumanSavings` — `human_savings_usd_total` stays 0.
- `TestTracker_ToolSession_RecordsHumanSavings` — `human_savings_usd_total` = 1.25.

## Summary of files touched

| File | Change |
|---|---|
| `callbacks/activity/costsaver.go` | Add constants; rewrite `FallbackComplexityAnalyzer.Analyze` (zero floor + free first step) |
| `callbacks/activity/prompts/complexity_analysis.md` | Full rewrite (trivial-zero guidance, human-time basis, all placeholders kept) |
| `callbacks/activity/metrics/collector.go` | Add `toolCalled` guard in `Watch`; handle `ToolCalled` events; update doc comment |
| `callbacks/activity/costsaver_test.go` | Rewrite fallback test table; add LLM zero-validation test |
| `callbacks/activity/metrics/costsaver_test.go` | Add no-tool skip test + tool-session run test |
| `libs/costtrack/tracker_test.go` | Add no-tool no-savings test + tool-session savings test |
| `callbacks/activity/metrics/README.md` | Document trivial-session skip |
| `libs/costtrack/README.md` | Document tool-gated savings |

No other files are modified. No public API signatures change.
