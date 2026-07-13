# Real-time LLM Cost Tracking Helper (supervisor + agents)

> Self-contained implementation plan. An implementation agent can execute it
> without extra context. All file paths are relative to the repo root
> (`/projects/eino-ext`). Follow `CONTRIBUTING.md` + `AGENTS.md` at every step.

## 0. Context & findings (read before coding)

### 0.1 What already exists in eino-ext (do NOT reimplement)
- `callbacks/activity/` — typed event bus + `Handler` (a `callbacks.Handler`).
  - `event.go`: `Event`, `Type*`, `Tokens{Input,Output,Reasoning,Cache{Read,Write}}`,
    `StepStarted{Agent,Model}`, `StepEnded{Finish,Cost,Tokens,Estimated}`,
    `ToolCalled`, `CompactionStarted{Reason}/Delta/Ended{Text}`, `SessionEnded{...}`.
  - `handler.go`: `NewHandlerWithConfig(bus, opts...)`, `WithPricer(Pricer)`,
    `WithTokenCounter(TokenCounter)`. `stepEnded(...)` builds `StepEnded` from
    `model.TokenUsage`; when usage is nil and a `TokenCounter` is set, it estimates
    tokens via chars/4 and sets `Estimated=true`. Cache = `PromptTokenDetails.CachedTokens`.
  - `bus.go`: per-session fan-out + `Replay(sessionID)`.
  - `costsaver.go`: `SessionSummarizer`, `ComplexityAnalyzer` (LLM),
    `FallbackComplexityAnalyzer` (formula), `CompositeComplexityAnalyzer`,
    `SessionSummary{TotalCost,TotalTokens,Steps,ToolsCalled,TextOutput,...}`,
    `ComplexityAnalysis{ComplexityRatio,HumanTimeSavedSeconds,MoneySavedUSD}`.
- `callbacks/activity/metrics/` — Prometheus collectors (`prometheus/client_golang`):
  - `collector.go`: `Collector` (`llm_tokens_total{model,agent,type}`,
    `llm_cost_usd_total{model,agent}`), `Observe(...)`, `Watch(ctx,bus,sessionID)`,
    `WithCostSaver(CostSaverConfig, bus)`.
  - `costsaver.go`: `CostSaverCollector`
    (`cost_saver_complexity_ratio`, `cost_saver_human_time_saved_seconds`,
    `cost_saver_money_saved_usd`, `cost_saver_fallback_count_total{reason}`).
- `libs/modelsdev/` — `Catalog`, `Cost{Input,Output,CacheRead,CacheWrite}`,
  `CatalogPricer{Catalog,Resolve}` implementing `activity.Pricer`.
  `costOf`: `Input/1e6*Cost.Input + Output/1e6*Cost.Output +
  Cache.Read/1e6*Cost.CacheRead + Cache.Write/1e6*Cost.CacheWrite`
  (reasoning is a subset of Output, never priced separately). `Load(ctx,opts)` does
  a bounded network refresh with embedded-snapshot fallback; `Catalog.Fresh`.
- `libs/counter/` — `DefaultTokenCounter` (chars/4) implementing `activity.TokenCounter`.
- `components/middleware/contextopt/` — intra-run compaction (trim/prune + optional
  LLM summary). Compaction events (`compaction.started/delta/ended`) are emitted
  on the activity bus; the rancher project also calls a `PublishCompaction` helper.
- Existing tests use a minimal `fakeModel`/`mockModel` implementing
  `model.BaseChatModel` (`Generate` + `Stream`). See
  `components/middleware/contextopt/summarizer_test.go:13` and
  `callbacks/activity/costsaver_test.go:14`.

### 0.2 Root cause: why the rancher project "did not work"
Project: `/projects/rancher-doc-chat-api-k8s` (`module github.com/hm-it/rancher-doc-chat-api-k8s`).
Two independent defects:

1. **Tokens/cost/savings stuck at zero.** The gateway (GitHub Copilot) does not
   return token usage on streaming responses even with
   `stream_options.include_usage`. With no fallback token counter, `StepEnded.Tokens`
   was all-zero → `CatalogPricer.Cost` returned 0 → every cost/savings metric was 0.
   **Fix already landed** in commit `cb81ae3` ("fix:: fix activity") by adding
   `activity.WithTokenCounter(counter.DefaultTokenCounter)` to the global handler
   (`internal/server/server.go:177`). This is the canonical fix to backport into any
   helper: a `CostTracker` MUST always wire a `TokenCounter` fallback.

2. **Built-in `metrics.Collector.Watch` cost-saver path is dead.**
   - `collector.go:148` switches on `case *activity.SessionEnded` (POINTER), but
     payloads are value types (`SessionEnded{}`), so the case never matches even if
     a `session.ended` event were published.
   - Worse: **nothing ever publishes `session.ended`** — `grep` of
     `callbacks/activity/handler.go` shows no `TypeSessionEnded` publication, so the
     `WithCostSaver` path can never fire end-to-end through the built-in `Watch`.
   - The rancher project worked around both by reimplementing the trigger on its own
     `answer.ended`/`question` terminal events in `internal/server/metrics.go`
     (`watchTaskCost` + `recordTask`), bypassing `metrics.WithCostSaver`.

### 0.3 "shera" library
`shera` does **not** exist as a Go module (pkg.go.dev search returns 0 results) and
is referenced in neither codebase. eino-ext already exports Prometheus metrics via
`github.com/prometheus/client_golang` (the `metrics` sub-package). **Decision: do not
introduce shera; reuse `prometheus/client_golang`.** A `// "shera" was investigated
and rejected (non-existent)` note is added to the facade README so the decision is
discoverable.

### 0.4 Kilocode mechanisms to backport (verified in `/projects/kilocode`)
Read these files before implementing; mirror the *ideas*, not the TS code:

- `packages/opencode/src/session/session.ts:410-477` — per-step cost:
  - `adjustedInput = inputTokens - cacheRead - cacheWrite` (avoid double-charging
    cache as input — AI SDK v6 folds cache into `inputTokens`).
  - cache-write fallbacks across providers (Anthropic `metadata.anthropic.cacheCreationInputTokens`,
    Vertex, Bedrock, Venice).
  - **Provider-reported cost precedence** (OpenRouter/Kilo) when present → use it,
    skip the formula.
  - Formula via `Decimal`: `input*in + output*out + cacheRead*cr + cacheWrite*cw +
    reasoning*out` (÷1e6), reasoning charged at output rate (models.dev TODO).
- `packages/opencode/src/kilocode/session/cost-propagation.ts` — subagent cost
  propagates into the parent assistant message's `cost` field on release; a per-key
  promise chain serializes concurrent read-modify-writes (parallel tool calls).
  Recursive aggregation without a tree walk (`childCost` sums assistant `cost`).
- `packages/opencode/src/kilocode/session/model-usage.ts` — recursive CTE over the
  session *family* tree, grouping by `(providerID, modelID)`, summing
  cost/tokens/cache, ordered by cost. `empty()` totals struct.
- `packages/opencode/src/kilocode/plugins/model-usage.ts` — `formatRate` =
  `cacheRead / (input + cacheRead + cacheWrite)` (cache-hit %), `formatCost` with
  `<$0.000001` clamp, Intl currency formatting.
- `packages/opencode/src/kilocode/session/compaction.ts` — compaction is a
  `compaction` part (auto/overflow flag) that retargets the prompt queue; compaction
  events are first-class on the activity stream (already mirrored in eino-ext's
  `CompactionStarted{Reason}` etc.).
- `packages/kilo-telemetry/src/telemetry.ts:157-174` — `trackLlmCompletion` emits
  per-completion `{inputTokens,outputTokens,cacheReadTokens,cacheWriteTokens,cost,
  duration}`. Mirror as the per-step `StepEnded` payload (already present).

The unimplemented-but-designed API in `libs/modelsdev/README.md:74-186` is the spec
to implement: `CostBreakdown{Input,Output,CacheRead,CacheWrite,Total,Savings}`,
`CatalogPricer.Breakdown(...)` (ok bool), `ContextUsage{Used,Window}` +
`(*Catalog).Usage(...)`. **Implement exactly this**, then surface `Savings` as a
new Prometheus counter (the README's "Prometheus surfacing (optional)" section).

## 1. Architecture decisions (locked)

1. **Deliverable = fill gaps + new `libs/costtrack` facade.** Build on existing
   `activity`/`modelsdev`/`metrics`; no greenfield reimplementation.
2. **Helper lives in a new top-level package `libs/costtrack/`** — a facade wrapping
   the activity bus, `modelsdev.CatalogPricer`, the `metrics.Collector` +
   `CostSaverCollector`, plus a per-session real-time totals aggregator and a
   `prometheus.Registry`/`http.Handler`. It exposes a builder API.
3. **Prometheus via `prometheus/client_golang`** on a dedicated registry (never
   `prometheus.DefaultRegisterer`, mirroring the rancher `metricsRegistry` pattern
   so embedding/testing doesn't pollute the process-wide registry).
4. **Fix the dead cost-saver path** by (a) fixing the pointer/value type switch in
   `metrics/collector.go`, and (b) giving `libs/costtrack` an explicit
   terminal-event trigger (configurable `TerminalTypes`, defaulting to
   `answer.ended`+`question` like the rancher project, OR `session.ended` when the
   caller publishes one). The facade publishes a synthetic `session.ended` on
   terminal events so the existing `WithCostSaver`/`Watch` path works unchanged.
5. **Always wire a `TokenCounter` fallback** (default `counter.DefaultTokenCounter`)
   on the activity handler, so tokens/cost never silently go zero when a gateway
   omits streaming usage (the exact rancher failure).
6. **Acceptance test = both** bus-only unit tests (fast, deterministic) AND one
   full `adk` supervisor + 2 fake sub-agents integration test using a scripted
   fake `ToolCallingChatModel`.
7. Follow all `CONTRIBUTING.md`/`AGENTS.md` rules: `validate.Struct(cfg)` after
   defaults in every `New...`, `ctx context.Context` first param threaded through,
   `emperror.dev/errors` wrapping, `//go:embed` for prompts, no license banners,
   no comment noise, official casing (`OpenSearch`/`GitHub`/`URL`/`ID`/`JSON`),
   `fmt.Sprintf` over `+`.

## 2. Data model

### 2.1 `libs/modelsdev` additions (implement the documented design)
In `libs/modelsdev/pricer.go` (and `catalog.go` as needed):

```go
// CostBreakdown is where one step's money went. Total == today's Cost(). Savings
// is the counterfactual USD a cache hit avoided (NOT subtracted from Total).
type CostBreakdown struct {
    Input      float64
    Output     float64
    CacheRead  float64
    CacheWrite float64
    Total      float64
    Savings    float64 // tokens.Cache.Read/1e6 * max(0, cost.Input-cost.CacheRead)
}

// ContextUsage reports how full the context window is.
type ContextUsage struct {
    Used   int
    Window int
}
func (u ContextUsage) Remaining() int      // max(0, Window-Used)
func (u ContextUsage) Fraction() float64    // Used/Window, 0 when Window<=0
func (u ContextUsage) NearLimit(threshold float64) bool // Fraction() >= threshold
```
- `(*Catalog) Usage(provider, id string, usedTokens int) (ContextUsage, bool)` —
  thin wrapper over `Limits`; `ok` mirrors `Limits`.
- `func (p CatalogPricer) Breakdown(gatewayModel string, t activity.Tokens) (CostBreakdown, bool)`
  — `ok` mirrors today's resolve/lookup/cost-nil failure modes; breakdown all-zero
  on `ok=false`. `Savings = tokens.Cache.Read/perMillion * max(0, cost.Input-cost.CacheRead)`.
- `Cost(gatewayModel, t)` becomes `Breakdown(...).Total` (or 0 if `!ok`):
  **zero behavior change** for existing callers (a table-driven BC test must prove it).
- `CatalogPricer` still implements `activity.Pricer.Cost(model, tokens) float64`
  (unchanged surface); the breakdown is opt-in.

### 2.2 `callbacks/activity/metrics` additions + fixes
- **Fix `collector.go:148`**: change `case *activity.SessionEnded:` to
  `case activity.SessionEnded:` (value). Add a parallel `case *activity.SessionEnded`
  too if defensive (use `resolveAny`-style handling like `costsaver.go` does, so both
  pointer and value match — port `resolveAny` to be exported or duplicated).
- New `CostSaverCollector` metrics (in `costsaver.go`) per the modelsdev README
  "Prometheus surfacing" section:
  - `llm_cost_savings_usd_total{model,agent}` (Counter) — sum of per-step
    `CostBreakdown.Savings`. Added to `Collector` (needs a `Pricer`/`Breakdown`
    reference) and recorded in `Observe` when a breakdown-aware pricer is configured.
  - Optional `llm_cost_usd_total{model,agent,component=input|output|cache_read|cache_write}`
    labeled variant — add as a *separate* counter `llm_cost_usd_by_component_total`
    (do NOT alter the existing unlabeled `llm_cost_usd_total` to avoid breaking
    dashboards).
- `Collector` gains an optional `BreakdownFunc func(model string, t activity.Tokens) (modelsdev.CostBreakdown, bool)`
  wired from `modelsdev.CatalogPricer.Breakdown`; when set, `Observe` records
  `llm_cost_savings_usd_total` and the by-component counter. When nil, behavior is
  unchanged (BC).
- `WithCostSaver` already wires `SessionSummarizer` + `CompositeComplexityAnalyzer`;
  keep it. The facade (§2.3) guarantees a `session.ended` is published so this path
  fires.

### 2.3.0 OpenTelemetry decision (locked)
OpenTelemetry is **out of scope** for this plan. eino-ext has no OTel
instrumentation today (`go.sum` OTel entries are transitive-only; no `.go` file
imports OTel), and the cost metrics are already fully served by Prometheus
counters/gauges/histograms. A "general metric scope" (repo-wide OTel metrics +
traces for agent execution) is a separate cross-cutting decision that belongs in
its own plan. To avoid a rewrite when that day comes, the facade emits all
metrics through a small **pluggable `Recorder` interface** (§2.3.1) with the
Prometheus collector as the default implementation. An OTel metrics adapter can be
added later by implementing `Recorder` — no facade changes required. The README
documents this seam and links "Future: OTel traces/metrics" as a follow-up plan.

### 2.3 `libs/costtrack` facade (new package)
Public surface (all `New...` take `ctx context.Context` first, validate after defaults):

```go
package costtrack

// Recorder is the abstraction over the metric backend. The default
// implementation is PrometheusRecorder (wraps metrics.Collector +
// CostSaverCollector + the facade's counters). An OpenTelemetry adapter can be
// added later by implementing Recorder — see §2.3.0. All Recording methods are
// no-ops on a nil Recorder, so a caller that mishandles construction degrades
// safely (mirrors metrics.Collector.Observe's nil-receiver discipline).
type Recorder interface {
    ObserveStep(model, agent string, se activity.StepEnded)           // tokens + cost
    ObserveBreakdown(model, agent string, b modelsdev.CostBreakdown)  // cost savings + by-component
    RecordTask(sessionID, agent string, cost float64, real bool)      // agent_tasks_total + histogram + human savings
    RecordCompaction(agent string)                                     // llm_compactions_total
    RecordAnalysis(sessionID, agent string, a *activity.ComplexityAnalysis)
    RecordFallback(reason string)
    SetRealtimeCost(sessionID, agent string, cost float64)           // live gauge (cleared on terminal)
}

// PrometheusRecorder is the default Recorder. Constructed in NewTracker from the
// dedicated prometheus.Registry; it owns the metrics.NewCollector +
// CostSaverCollector + facade counters from §2.3.
type PrometheusRecorder struct{ /* unexported */ }

// Config for NewTracker. Validate+jsonschema tags; defaults applied in New.
type Config struct {
    Bus            activity.Bus             `json:"-"`
    Resolve        modelsdev.NameResolver  `json:"-"`   // required
    CatalogHolder  *atomic.Pointer[modelsdev.Catalog] `json:"-"` // required
    PricingProvider string                  `json:"pricingProvider" validate:"required"`
    TokenCounter   activity.TokenCounter    `json:"-"`   // default counter.DefaultTokenCounter
    Savings        activity.ComplexityAnalyzerConfig `json:"-"` // optional; Model may be nil
    TerminalTypes  []activity.Type          `json:"-"`   // default {answer.ended, question}; session.ended also honored
    Registry       prometheus.Registerer   `json:"-"`   // default: new private registry
    Recorder       Recorder                 `json:"-"`   // default: PrometheusRecorder built from Registry
}

type Tracker struct { ... }

// NewTracker applies defaults (TokenCounter=counter.DefaultTokenCounter, a new
// private prometheus.Registry, TerminalTypes={answer.ended,question}, and
// Recorder=PrometheusRecorder built from that registry) then calls
// validate.Struct(cfg). When cfg.Recorder is non-nil, NewTracker uses it as-is
// (caller-managed; PrometheusHandler then returns nil / panics-with-doc since a
// non-Prometheus Recorder exposes no /metrics — document this).
func NewTracker(ctx context.Context, cfg *Config) (*Tracker, error)

// ActivityHandler returns the activity.Handler to register globally
// (callbacks.AppendGlobalHandlers). It is preconfigured with the catalog pricer
// and the token-counter fallback (so cost/tokens never silently zero).
func (t *Tracker) ActivityHandler() *activity.Handler

// Pricer exposes the dynamic catalog pricer (for callers that build their own handler).
func (t *Tracker) Pricer() activity.Pricer

// MetricsRegistry is the dedicated Prometheus registry holding all collectors.
func (t *Tracker) MetricsRegistry() prometheus.Registerer

// PrometheusHandler returns an http.Handler for /metrics (promhttp.HandlerFor).
func (t *Tracker) PrometheusHandler() http.Handler

// Snapshot returns real-time per-session + global totals (thread-safe).
func (t *Tracker) Snapshot(sessionID string) Snapshot

// Watch must be run in a goroutine per session; it aggregates real-time totals,
// feeds the Recorder (default: Prometheus), and on a terminal event publishes a
// synthetic session.ended (so the built-in cost-saver path fires) and records
// task-level metrics + human savings via the Recorder. Cancel ctx to stop.
func (t *Tracker) Watch(ctx context.Context, sessionID string)
```

`Snapshot` (the "user-friendly" real-time view):

```go
type Snapshot struct {
    SessionID   string
    Duration    time.Duration
    Steps       int
    Models      []ModelUsage // grouped by (provider, model), ordered by cost desc (kilocode model-usage.ts)
    Totals      Usage
    Compactions int         // count of compaction.ended events
    HadFailures bool
    Estimated   bool         // true if any step used the token-counter fallback
}

type Usage struct {
    Cost   float64
    Savings float64        // cache savings (from CostBreakdown.Savings)
    Tokens activity.Tokens // input/output/reasoning/cache{read,write}
    Steps  int
}

type ModelUsage struct {
    Provider string
    Model   string
    Usage
    CacheRate float64 // cache.Read / (input + cache.Read + cache.Write)  (kilocode formatRate)
}
```

Internal aggregation (port kilocode `cost-propagation.ts` + `model-usage.ts` ideas):
- A `sync.Mutex`-guarded `map[sessionKey]*sessionState` where `sessionKey` =
  `(rootSessionID, agent)` (subagent cost propagates up to the supervisor's
  session — the activity bus already correlates by `Event.Agent` + the agentattr
  middleware). For parallel tool calls, serialize per-key updates with a
  `singlekeymutex` (or `sync.Mutex` per key map, mirroring kilocode's per-key lock).
- On `step.ended`: compute `CostBreakdown` via `CatalogPricer.Breakdown`, add to
  `Usage.Cost`/`Savings`/`Tokens`, increment `Models[...]` for the resolved
  (provider, model). Mark `Estimated` if `StepEnded.Estimated`.
- On `tool.called` (non-terminal): mark "real task" (so savings are credited only
  when the run delegated — mirrors rancher `isRealTask`/`terminalToolNames`).
- On terminal event (§2.3 `TerminalTypes`) OR `session.ended`: publish a synthetic
  `session.ended` (if not already present) carrying `{Duration,Cost,Steps,Tools}`,
  then run the cost-saver (`CompositeComplexityAnalyzer`) only if the run was a
  "real task" and `Savings.HumanHourlyRate > 0`; record `human_savings_usd_total`.
- On `compaction.ended`: bump `Compactions` and (optionally) a
  `llm_compactions_total{agent}` counter.

New Prometheus metrics owned by the facade (registered on `cfg.Registry`):
- `llm_cost_savings_usd_total{model,agent}` (if not already added in §2.2 — define
  once; prefer §2.2 in the metrics package, facade references it).
- `human_savings_usd_total` (Counter) — human time→money saved.
- `agent_tasks_total` (Counter), `agent_task_cost_usd` (Histogram,
  `ExponentialBuckets(0.001,2,16)`) — mirror rancher `metrics.go`.
- `llm_compactions_total{agent}` (Counter).
- `llm_realtime_cost_usd{session_id,agent}` (Gauge) — live running cost for a
  session (set on each step; cleared/removed on terminal). Low cardinality only if
  session ids are bounded — document the caveat (mirror `metrics/collector.go`
  cardinality note).

### 2.4 HTTP endpoint (optional, in the facade)
```go
// Mount mounts /metrics (Prometheus) and /cost/usage?session=... (JSON Snapshot)
// on the given mux. Implementations: Hertz + net/http. Provide net/http first
// (no extra dep); Hertz adapter is a follow-up if requested.
func (t *Tracker) Mount(mux *http.ServeMux)
```
- `GET /metrics` → `t.PrometheusHandler()`.
- `GET /cost/usage?session=<id>` → JSON `Snapshot` (404 when unknown).
- `GET /cost/usage` (no session) → JSON `map[sessionID]Snapshot` of live sessions.

## 3. Files to create / modify (ordered)

### A. `libs/modelsdev` — implement the documented breakdown API
1. `libs/modelsdev/pricer.go`: add `CostBreakdown`, `ContextUsage`, methods
   `Breakdown`, `(*Catalog) Usage`, refactor `Cost` to delegate to `Breakdown`.
2. `libs/modelsdev/pricer_test.go`: table-driven — component sums == `costOf`
   total; `Savings` math (anthropic opus fixture: input $5, cache_read $0.5 →
   savings = reads/1e6*4.5); `Savings==0` when `CacheRead>=Input` or
   `Cache.Read==0`; `ContextUsage` at 0/mid/over-limit, `Window<=0`⇒`Fraction()==0`;
   **BC**: `Cost(m,t) == Breakdown(m,t).Total` across known/unknown models and
   zero/non-zero cache.
3. `libs/modelsdev/README.md`: flip the "Status: designed, not yet implemented"
   section to "Implemented", add a Usage snippet + the `Savings`/cache-rate
   explanation (mirror the existing prose; no new license banners).

### B. `callbacks/activity/metrics` — fix + extend
4. `callbacks/activity/metrics/collector.go`:
   - Fix `case *activity.SessionEnded` → handle BOTH pointer and value (port
     `activity.resolveAny` to a small exported helper or inline type assertion).
   - Add `llm_cost_savings_usd_total{model,agent}` + `llm_cost_usd_by_component_total{model,agent,component}`
     counters; add `WithBreakdown(pricer)` / `BreakdownFunc` option; record them in
     `Observe` when configured (BC when nil).
5. `callbacks/activity/metrics/costsaver.go`: no metric-name changes; ensure
   `RecordAnalysis` also bumps a `cost_saver_runs_total{agent}` counter (add it).
6. `callbacks/activity/metrics/collector_test.go` + new `costsaver_test.go`:
   - Add a case proving `session.ended` (VALUE) now triggers
     `handleSessionEnded`/cost-saver (the bug fix). Use a fake `model.BaseChatModel`
     returning a JSON `ComplexityAnalysis` (copy the `mockModel` from
     `callbacks/activity/costsaver_test.go:14`).
   - Add a breakdown test: `Observe` with a `BreakdownFunc` fixture advances
     `llm_cost_savings_usd_total` and the by-component counter.
7. `callbacks/activity/metrics/README.md`: document the new metrics + the
   pointer/value fix + `WithBreakdown`.

### C. `libs/costtrack` — the new facade
8. `libs/costtrack/tracker.go`: `Config` (validate+jsonschema tags), `Recorder`
   interface, `PrometheusRecorder` (wraps `metrics.Collector` +
   `CostSaverCollector` + facade counters), `Tracker`, `NewTracker`,
   `ActivityHandler`, `Pricer`, `MetricsRegistry`, `PrometheusHandler`,
   `Snapshot`, `Watch`. Defaults: `TokenCounter`=`counter.DefaultTokenCounter`;
   `Registry`=new private registry; `Recorder`=`PrometheusRecorder` built from it;
   wire `metrics.NewCollector(reg, metrics.WithCostSaver(...))`. Validate via
   `libs/toolkit/validate.Struct`. All Recorder methods are nil-receiver safe.
9. `libs/costtrack/snapshot.go`: `Snapshot`/`Usage`/`ModelUsage` types +
   aggregation (port kilocode `model-usage.ts` grouping + `formatRate` cache-rate;
   port `cost-propagation.ts` per-key serialization for parallel subagent updates).
   Aggregation reads from the bus events directly (independent of the Recorder
   backend) so `Snapshot` works with any `Recorder`, including a future OTel one.
10. `libs/costtrack/http.go`: `Mount(mux)` + JSON handlers (`/metrics`,
    `/cost/usage`). Pure `net/http`. `/metrics` is a no-op (returns 501) when the
    Recorder is not the `PrometheusRecorder` (document).
11. `libs/costtrack/check.go` + `check_test.go`: `Check()` returning
    `checkup.Results` (per AGENTS.md "Checkup" rule) — probes that the registry
    gathers and that the catalog resolves at least one model; `limited` (not
    `error`) when the catalog has no priced models (no real "resource" to list).
    (Note: `libs/` may not strictly require checkup per CONTRIBUTING, but the
    AGENTS.md "Component Design Principles" encourages it; add it to be safe.)
12. `libs/costtrack/tracker_test.go`: table-driven unit tests (bus-only, fast).
13. `libs/costtrack/accept_test.go`: the full ADK acceptance test (§4).
14. `libs/costtrack/README.md`: what it does, `NewTracker` snippet, the
    `// "shera" investigated, non-existent` note, the OTel-out-of-scope +
    `Recorder`-seam note (§2.3.0), the rancher root-cause note ("always wire
    TokenCounter"), the metric table, and which eino abstraction it relates to
    (callbacks.Handler + metrics, not a component).
15. `libs/costtrack/fake_model_test.go`: a shared scripted fake
    `ToolCallingChatModel` for the acceptance test (see §4.2). Keep it in
    `*_test.go` so it never ships.

### D. Repo-level wiring / docs
16. `AGENTS.md` / `CONTRIBUTING.md`: no source changes required; if the
    implementer adds a new `libs/` entry, ensure the "Project structure" section's
    `libs/` bullet still applies (it does: "shared, non-component support
    libraries"). Do NOT add license banners.

## 4. Tests

### 4.1 Unit tests (bus-only, deterministic) — `libs/costtrack/tracker_test.go`
Table-driven, no real LLM. Use a fake `activity.Pricer` + scripted bus events:
- `step.started{model="claude-opus-4-5"}` + `step.ended{Tokens:{Input:1000,Output:500,Cache:{Read:200,Write:100}}, Cost:0.01}`
  → `Snapshot.Totals.Cost` increments; `Savings` > 0 (cache read discount); cache
  rate `200/(1000+200+100)` ≈ 0.153; `Models[]` grouped by `(provider,model)`.
- `step.ended` with `Estimated=true` → `Snapshot.Estimated==true` (the
  token-counter-fallback path; proves the rancher fix holds).
- `tool.called{tool:"list_pods"}` (non-terminal) then `answer.ended` → `agent_tasks_total`
  +1, `human_savings_usd_total` advances (real task). A run with ONLY a terminal
  tool and no sub-agent → savings NOT credited (mirror `isRealTask`).
- `compaction.ended` → `Snapshot.Compactions==1`, `llm_compactions_total`+1.
- Parallel `step.ended` for the same `(root,agent)` from two goroutines → no lost
  updates (the per-key lock works; assert the sum equals the serial sum).
- **Recorder seam**: add a `fakeRecorder` (in-memory counters map) in the test
  file, wire it via `Config.Recorder`, and assert `Watch` drives every
  `Recorder` method (`ObserveStep`, `ObserveBreakdown`, `RecordTask`,
  `RecordCompaction`, `RecordAnalysis`/`RecordFallback`, `SetRealtimeCost`) —
  independent of Prometheus. This proves a future OTel adapter would receive the
  same events.
- Terminal `answer.ended` → synthetic `session.ended` published (assert by
  subscribing separately) and cost-saver `RecordAnalysis` invoked (with a fake
  `model.BaseChatModel` returning valid JSON → no fallback; returning garbage →
  fallback + `cost_saver_fallback_count_total{reason="llm_error"}`+1).
- Unknown/unresolvable model → `Cost==0`, `Savings==0`, no panic; `ok=false`
  propagated.
- `/cost/usage` HTTP: 404 for unknown session; JSON for known.
- `validate.Struct(cfg)` rejects a `Config` missing `Resolve`/`CatalogHolder`
  (`validate:"required"` on the right fields — but note pointers can't use
  `required`; use a custom guard or `dive`; apply defaults first).
- Use `prometheus.NewRegistry()` per test to avoid cross-test bleed (pattern from
  `metrics/collector_test.go`).

### 4.2 Full ADK acceptance test — `libs/costtrack/accept_test.go`
Goal: prove the whole pipeline (supervisor + 2 sub-agents + fake tool-calling
model) drives real activity events → cost/tracker + Prometheus advance.

Fake model (`fake_model_test.go`, port the `fakeModel` pattern from
`components/middleware/contextopt/summarizer_test.go:13` and extend it):
```go
// scriptedToolModel implements model.ToolCallingChatModel: BindTools stores tool
// schemas; Generate/Stream return the next scripted *schema.Message from a queue.
// A script step is either a tool_call message (supervisor delegates to a sub-agent
// tool) or a final assistant text message.
type scriptedToolModel struct {
    steps []*schema.Message // queue
    bound []tool.BaseTool
}
func (m *scriptedToolModel) BindTools(_ context.Context, tools []tool.BaseTool) error { m.bound = tools; return nil }
func (m *scriptedToolModel) Generate(ctx, in, opts...) (*schema.Message, error) { return m.next(), nil }
func (m *scriptedToolModel) Stream(ctx, in, opts...) (*schema.StreamReader[*schema.Message], error) { ... pipe m.next() ... }
// compile-time: var _ model.ToolCallingChatModel = (*scriptedToolModel)(nil)
```
Each step message carries `model.TokenUsage` via `Message.ResponseMeta.Usage`
(`*schema.TokenUsage{PromptTokens,CompletionTokens,...CachedTokens}`) so the
activity handler reports REAL usage (not estimated) for the happy path; include a
second case where usage is nil to prove the `TokenCounter` fallback still yields
non-zero cost (the rancher regression guard).

Test flow:
1. `NewTracker(ctx, cfg)` with a `modelsdev.Catalog` from the embedded snapshot
   (use the `embeddedCtx`-already-cancelled trick from the rancher project to force
   the snapshot path with no network: `modelsdev.Load(alreadyCancelled, LoadOptions{})`),
   a `NameResolver` mapping the fake model name → a priced catalog entry, and a
   fake small `model.BaseChatModel` for the cost-saver returning a fixed
   `ComplexityAnalysis` JSON.
2. `callbacks.AppendGlobalHandlers(t.ActivityHandler())` (registered for the test
   process; use a private registry + `prometheus.Unregister`/`NewRegistry` so the
   global `DefaultRegisterer` is untouched — mirror `metricsRegistry` pattern).
3. Build the supervisor: `adk.NewChatModelAgent` named "supervisor" with model =
   the scripted tool model, two sub-agents built as `adk.NewAgentTool` (each itself a
   `adk.NewChatModelAgent` with its own scripted model + `agentattr`
   `ChatModelAgentMiddleware` for name attribution), `EmitInternalEvents: true`
   (so sub-agent events surface — mirror rancher `supervisor.go:55`). Use a
   terminal tool (`attempt_completion`-like) that publishes `answer.ended` on the
   bus (mirror `internal/server/agent/answer.go:98` `PublishAnswer`).
4. Script: supervisor step 1 → tool-call to sub-agent A; sub-agent A → real
   tool-call (`tool.called`) + assistant text; supervisor step 2 → tool-call to
   sub-agent B; sub-agent B → assistant text; supervisor step 3 → final answer
   (terminal tool publishes `answer.ended`).
5. Run the `adk.Runner`/iterator to completion (`adk.NewRunner(supervisor).Run(...)`
   or the `AsyncIterator` drain pattern from `rancher/internal/server/agent.go`),
   with `activity.WithSession(ctx, sessionID)`. Start `go t.Watch(ctx, sessionID)`
   before running.
6. Assertions (use the `gatherValue` helper from `metrics/collector_test.go`):
   - `llm_tokens_total{model,agent,type=input|output|reasoning|cache_read}` > 0 for
     BOTH the supervisor model and each sub-agent model (proves cross-agent
     attribution via `agentattr` + `EmitInternalEvents`).
   - `llm_cost_usd_total{model,agent}` > 0 for each; `llm_cost_savings_usd_total`
     > 0 (cache read discount present in the script).
   - `agent_tasks_total` == 1; `agent_task_cost_usd` histogram has 1 sample;
     `human_savings_usd_total` > 0 (real task: sub-agents ran).
   - `cost_saver_complexity_ratio`/`human_time_saved_seconds`/`money_saved_usd`
     gauges set (cost-saver fired via the synthetic `session.ended`).
   - `Snapshot(sessionID).Totals.Cost` equals the sum of the per-model
     `llm_cost_usd_total` samples (consistency between the facade aggregator and
     the Prometheus counters).
   - `Snapshot.Estimated` is true in the nil-usage sub-case, false otherwise.
   - `GET /metrics` (via `httptest`) contains `llm_cost_usd_total` and
     `human_savings_usd_total` lines.
7. A second, smaller scenario: a trivial "hi" → supervisor answers directly (no
   sub-agent tool call) → `agent_tasks_total`+1 but `human_savings_usd_total`
   UNCHANGED (savings not credited for non-real tasks — the `isRealTask` rule).

### 4.3 Existing-test regression guards
- Re-run `callbacks/activity` + `callbacks/activity/metrics` tests after §B; the
  pointer/value fix MUST NOT break the existing `TestCollector_Watch` (which uses
  value `StepEnded`).
- `libs/modelsdev` BC test (§3 step 2) MUST keep `Cost` numeric results identical.

## 5. Configuration example (for the README)

```go
cat := modelsdev.Load(ctxAlreadyCancelled, modelsdev.LoadOptions{}) // embedded snapshot
holder := new(atomic.Pointer[modelsdev.Catalog]); holder.Store(cat)
tracker, err := costtrack.NewTracker(ctx, &costtrack.Config{
    Bus:             activityBus,
    CatalogHolder:   holder,
    PricingProvider: "anthropic",
    Resolve: func(gw string) (provider, id string, ok bool) {
        return "anthropic", gw, true // map gateway name → catalog id
    },
    Savings: activity.ComplexityAnalyzerConfig{HumanHourlyRate: 50, BaseTaskTime: 5*time.Minute},
})
callbacks.AppendGlobalHandlers(tracker.ActivityHandler())
mux := http.NewServeMux()
tracker.Mount(mux) // /metrics + /cost/usage
go tracker.Watch(runCtx, sessionID)
```

## 6. Validation (run before declaring done)

```bash
go build ./...
go vet ./...
go test ./libs/modelsdev/... ./callbacks/activity/... ./libs/costtrack/...
```
- All new `New...` constructors validate via `validate.Struct(cfg)` after defaults.
- No license banners; `//go:embed` for any prompt (cost-saver prompt already exists
  at `callbacks/activity/prompts/complexity_analysis.md` — reuse, don't duplicate).
- `go test ./libs/costtrack/ -run Accept` must pass the full ADK acceptance test
  with NO network (snapshot-only catalog) and NO live LLM (fake models).
- Naming: `OpenSearch`/`GitHub`/`URL`/`ID`/`JSON` casing; `fmt.Sprintf` over `+`.

## 7. Risks & open items (call out in PR)

- **Cardinality**: `llm_realtime_cost_usd{session_id}` and the cost-saver gauges
  keyed by `session_id` are unbounded if session ids are minted per request. Document
  the bound-and-reuse deployment model (mirror the `lastAgent` map caveat in
  `handler.go:42-49`). Consider an LRU/eviction hook later (out of scope here).
- **adk API surface**: the exact `adk.Runner`/`AsyncIterator` + `ToolCallingChatModel`
  method set is in `github.com/cloudwego/eino v0.9.12` (used by the rancher project).
  If the eino module isn't extracted locally, run `go mod download` first; mirror the
  call sites in `rancher/internal/server/agent.go` (`drainRun`) and
  `agent/supervisor.go` (`adk.NewChatModelAgent`/`adk.NewAgentTool`). If `adk` API
  differs in v0.9.12 from the snippets above, adapt — keep the assertions, not the
  literal calls.
- **Provider-reported cost precedence** (kilocode `session.ts:445`): eino-ext's
  `CatalogPricer` currently has no hook for a gateway-reported cost. Mark as a
  documented future extension in the README (the `StepEnded.Cost` path already
  trusts `Pricer.Cost`); out of scope for this plan unless the gateway exposes it.
- **Reasoning-token pricing**: kilocode charges reasoning at the output rate;
  eino-ext's `costOf` deliberately does NOT (reasoning ⊂ output). Keep eino-ext's
  rule; note the divergence in the README so future readers aren't confused.
- **`session.ended` dual publication**: if a caller already publishes
  `session.ended`, the facade must not double-fire the cost-saver. Track a
  `sessionEnded bool` per session (under the per-key lock) and skip the synthetic
  publish when already true.
- **OpenTelemetry (out of scope)**: OTel metrics/traces are intentionally NOT part
  of this plan (§2.3.0). The `Recorder` interface is the seam: a future
  `otelRecorder` implementing `Recorder` can dual-export to OTel without touching
  the facade. When that happens, also add OTel **traces** (span per supervisor
  step / sub-agent delegation / tool call) via the activity `Handler` — but that
  is a separate cross-cutting plan. Note: a non-Prometheus `Recorder` makes
  `PrometheusHandler`/`/metrics` a no-op; the JSON `/cost/usage` endpoint still
  works (it reads `Snapshot`, not the Recorder).
