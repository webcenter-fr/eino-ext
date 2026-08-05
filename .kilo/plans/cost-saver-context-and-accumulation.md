# Plan: Cost saver — context cancellation + cumulative-delta accumulation

## Problem

After the trivial-task fix, the user's real app still misbehaves:
1. The savings value always jumps straight to ~$37 and never ramps (5, 10, 15).
2. Continuing the chat does not update the value.
3. Logs show: `LLM complexity scoring failed, falling back to deterministic
   formula ... context canceled` on every turn.

## Root causes (verified by reading the app)

The app (`/projects/rancher-doc-chat-api-k8s`) has its OWN cost-saver wiring
that calls eino-ext's `activity.ComplexityAnalyzer` directly:

- `internal/server/metrics.go:watchTaskCost` runs per **run** (per chat turn),
  subscribes to the bus, and on the terminal event (`agent.AnswerEnded` /
  `agent.Question`) calls `recordTask(ctx, ...)` then returns. `ctx` is the
  **run's context** (from `RunAgent.func2`).
- `internal/server/metrics.go:recordTask`:
  1. `summary, _ := sessionSummarizer.GetSummary(sessionID)` — **cumulative**
     across the whole session (the bus replays every event for the session).
  2. `analysis, _ := complexityAnalyzer.Analyze(ctx, summary)` — passes the
     run's `ctx`.
  3. `humanSavingsTotal.Add(analysis.MoneySavedUSD)` — ADDs the full cumulative.

### Root cause 1 — LLM analysis is cancelled by the run's dying context
The terminal event fires at the END of the run; the run's context is cancelled
at (or just before) that moment. `recordTask` is called with that already-
cancelling `ctx`, and `ComplexityAnalyzer.Analyze` derives its timeout from
`ctx` (`context.WithTimeout(ctx, timeout)`) and calls `Model.Generate` with
it. The Copilot request therefore aborts with `context canceled` on every
turn → the analyzer ALWAYS falls back to the deterministic formula. The
formula saturates quickly (the real workload has many tokens/tools/steps) to a
fixed ratio, so with `humanHourlyRate:75` and `baseTaskTime:45m` it yields a
fixed ~$37 every turn → no ramp, "doesn't update".

### Root cause 2 — Counter adds the full cumulative every turn
`GetSummary` is cumulative across the session, so `analysis.MoneySavedUSD` is
the cumulative savings so far. `humanSavingsTotal.Add(analysis.MoneySavedUSD)`
re-adds the whole cumulative on every turn → would balloon (37, 74, 111...).
The gauge `cost_saver_money_saved_usd` (SET by `CostSaverCollector`) shows the
cumulative snapshot, which stays flat at the saturated ~$37 — that is what the
user sees as "doesn't update". The desired behavior is a per-turn **delta** so
the counter ramps by each turn's new contribution.

### Config (the $37 source)
`config.app.yaml.sample`: `savings.humanHourlyRate: 75`, `baseTaskTime: 45m`.
Fallback money = `ratio * 2700s * 75 / 3600 = ratio * 56.25`. At saturated
ratio ≈ 0.66 → ~$37.

## Design decisions

- **Fix A location: the app, not the library.** The library's `Analyze(ctx,
  summary)` correctly respects the caller's context; the library's own
  internal callers (`metrics/collector.go`, `costtrack/tracker.go`) pass long-
  lived session contexts and are unaffected. The misuse is the app passing a
  run-scoped context to a post-run analysis. The fix is for the app to pass a
  detached context (`context.Background()`) so the analysis outlives the
  request, bounded by the analyzer's `Timeout` (30s default). This keeps
  library semantics clean and is version-independent (works with the eino-ext
  version they currently pin).
- **Fix B: cumulative-delta.** Track the last cumulative `MoneySavedUSD` per
  session; on each turn add only `max(0, current - last)` to the counter. The
  gauge continues to be SET to the cumulative snapshot (correct). The counter
  becomes a monotonic sum of per-turn deltas — the desired ramp. Negative
  deltas (LLM estimates lower than last turn) are clamped to 0 so the counter
  never decreases.
- **eino-ext prompt: clarify cumulative.** The embedded prompt should state
  the summary is the WHOLE session so far and the estimate must be the TOTAL
  human time saved across everything in the summary (not just the last turn),
  so the delta model is consistent for LLM callers. All `{{.X}}` placeholders
  preserved.

## Change A — Detach context in `recordTask` (app)

**File:** `/projects/rancher-doc-chat-api-k8s/internal/server/metrics.go`

In `recordTask`, replace:
```go
analysis, fallbackReason := complexityAnalyzer.Analyze(ctx, summary)
```
with:
```go
// The cost-saver analysis is a post-run, potentially slow LLM call that must
// outlive the run's request context (which is cancelled the instant the
// terminal event fires). Use a detached context so the Copilot request is
// not aborted with "context canceled"; the analyzer's own Timeout (30s
// default) bounds it.
analysis, fallbackReason := complexityAnalyzer.Analyze(context.Background(), summary)
```

`ctx` is still used by `recordTask`'s signature for future use; the `context`
package is already imported. No other call site changes.

## Change B — Cumulative-delta accumulation (app)

**File:** `/projects/rancher-doc-chat-api-k8s/internal/server/metrics.go`

Add a package-level, mutex-guarded map tracking the last cumulative savings
per session, and add only the positive delta to `humanSavingsTotal`.

After the existing `var` block (near `humanSavingsTotal`), add:
```go
// lastSavingsPerSession tracks the last cumulative MoneySavedUSD recorded for
// each session. The session summary (and thus the analyzer's estimate) is
// cumulative across the whole session, so the savings counter must advance by
// the per-turn delta (current - last), not re-add the full cumulative each
// turn. Guarded by savingsMu.
var (
	savingsMu              sync.Mutex
	lastSavingsPerSession  = make(map[string]float64)
)
```
(`sync` is already imported.)

In `recordTask`, replace:
```go
costSaverCollector.RecordAnalysis(sessionID, "supervisor", analysis)
humanSavingsTotal.Add(analysis.MoneySavedUSD)
```
with:
```go
costSaverCollector.RecordAnalysis(sessionID, "supervisor", analysis)

// analysis.MoneySavedUSD is cumulative across the session (the summary
// replays every event). Add only this turn's delta so the counter ramps
// instead of re-counting prior turns.
savingsMu.Lock()
last := lastSavingsPerSession[sessionID]
delta := analysis.MoneySavedUSD - last
if delta > 0 {
	humanSavingsTotal.Add(delta)
}
lastSavingsPerSession[sessionID] = analysis.MoneySavedUSD
savingsMu.Unlock()
```

### Edge cases
- First turn: `last == 0` → delta = full cumulative (the honest first-turn
  estimate the user wants).
- Later turn with lower cumulative (LLM noise): delta < 0 → clamped, counter
  unchanged (monotonic).
- New session: starts fresh at 0.
- Concurrent runs of the same session: chat turns are sequential, but the
  mutex makes it safe regardless.

## Change C — Prompt: cumulative clarification (eino-ext)

**File:** `/projects/eino-ext/callbacks/activity/prompts/complexity_analysis.md`

Add one line to the "Session Summary" preamble and one to Step 2, preserving
all `{{.X}}` placeholders:

After the `- Had failures: {{.HadFailures}}` / `- Human hourly rate` lines,
append to the intro paragraph a sentence:
> The summary above covers the ENTIRE session so far (all turns, cumulative).
> Estimate the TOTAL human time saved across everything in the summary, not
> just the most recent turn.

And in Step 2, clarify:
> If the task is REAL, estimate how many seconds a competent human would take
> to do ALL the work in this summary manually (cumulative across every turn so
> far). Call this `human_time`.

This keeps the prompt as a Markdown file under `prompts/` embedded via
`//go:embed` (CONTRIBUTING.md convention).

## Validation

eino-ext:
```bash
go build ./...
go vet ./...
go test ./callbacks/activity/...
```

app:
```bash
cd /projects/rancher-doc-chat-api-k8s
go build ./...
go vet ./...
gofmt -l internal/server/metrics.go
go test ./internal/server/...
```

## Compliance

- App AGENTS.md: `go build ./...`, `go vet ./...`, `gofmt -l .`,
  `golangci-lint run ./...`, `go test ./internal/server/...`.
- eino-ext: no license banners, `//go:embed` prompt, naming conventions, no
  AGENTS.md/CONTRIBUTING.md edits.

## Files touched

| File | Change |
|---|---|
| `rancher-doc-chat-api-k8s/internal/server/metrics.go` | Detach ctx in recordTask (Fix A); cumulative-delta counter (Fix B) |
| `eino-ext/callbacks/activity/prompts/complexity_analysis.md` | Cumulative clarification (Change C) |
