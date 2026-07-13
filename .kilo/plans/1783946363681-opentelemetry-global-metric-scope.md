# OpenTelemetry global metric scope (+ traces) for eino / eino-ext

> Self-contained implementation plan. An implementation agent can execute it
> without extra context. All paths are relative to the repo root
> (`/projects/eino-ext`). Follow `CONTRIBUTING.md` + `AGENTS.md` at every step.

## 0. Context & findings (read before coding)

### 0.1 eino framework has NO native OpenTelemetry
`github.com/cloudwego/eino@v0.9.12` (the only eino version this repo uses) has
**zero** OpenTelemetry imports. The framework's observability extension point is
the **`callbacks.Handler` interface** + `callbacks.AppendGlobalHandlers(...)`
(`eino/callbacks/interface.go:87-105`) plus the optional `TimingChecker`
(`interface.go:136-145`). There is nothing in eino to "extend" for OTel; the
framework is OTel-agnostic by design. Conclusion: the OTel scope is built here,
in eino-ext, on top of the `callbacks.Handler` mechanism — which is exactly the
"share this functionality with the framework" path.

### 0.2 eino-ext already has the metric-backend seam
`libs/costtrack` is a facade wiring the activity bus → `models.dev` pricer →
metric backend. It defines a pluggable `Recorder` interface
(`libs/costtrack/tracker.go:32-40`) with a default `PrometheusRecorder`
(`tracker.go:42-52`). The cost-tracking plan deliberately deferred OTel:
> "An OpenTelemetry metrics adapter can be added later by implementing
> `Recorder` — no facade changes required." (`libs/costtrack/README.md:54-59`).

This plan delivers that adapter. The `Recorder` interface is **stable and
sufficient** for the OTel metrics adapter — no changes to the facade's public
surface are required.

### 0.3 Existing activity/callbacks pieces to reuse (do NOT reimplement)
- `callbacks/activity` — `Handler` (a `callbacks.Handler`) emitting typed
  events to a `Bus`; `event.go` catalog (`StepStarted/Ended/Failed`,
  `ToolCalled/Success/Failed`, `CompactionEnded`, `SessionEnded`, …);
  context helpers `WithSession`/`SessionFromContext`/`WithAgent`/`AgentFromContext`
  (`callbacks/activity/context.go`).
- `callbacks/activity/metrics` — Prometheus `Collector` + `CostSaverCollector`
  (`callbacks/activity/metrics/collector.go`, `costsaver.go`). The OTel recorder
  records the **same logical metrics** under OTel instrument names.
- `callbacks/log` — `Handler` (`callbacks/log/handler.go`) that dispatches on
  `components.ComponentOfChatModel` / `ComponentOfTool` and pairs start/end. The
  OTel **trace** handler mirrors this dispatch exactly, replacing logrus
  entries with OTel spans.
- `libs/costtrack` — `Tracker`, `Recorder`, `Config` (validate+jsonschema),
  `NewTracker(ctx, cfg)`, `Watch`, `Snapshot`. The OTel recorder plugs in via
  `Config.Recorder`.

### 0.4 OTel dependencies are currently transitive-only
`go.sum` has only `v1.43.0/go.mod` hash entries for `go.opentelemetry.io/otel`,
`otel/metric`, `otel/sdk` (transitive). They MUST be added as **direct** deps.
Verified available versions (latest compatible with go1.26.3):
- `go.opentelemetry.io/otel` v1.43.0 (contains `otel`, `otel/trace`,
  `otel/attribute`, `otel/codes`, `otel/metric/global` is in the `otel/metric`
  module).
- `go.opentelemetry.io/otel/metric` v1.43.0 (the metric API +
  `metric/global`).
- `go.opentelemetry.io/otel/sdk` v1.43.0 (contains `sdk/trace` +
  `sdk/trace/tracetest` — **test-only**).
- `go.opentelemetry.io/otel/sdk/metric` v1.43.0 (the metric SDK +
  `metric.WithReader`/`ManualReader` — **test-only**).
- `go.opentelemetry.io/otel/exporters/prometheus` v0.66.0 — **optional**,
  README example only; do NOT make it a hard runtime dep unless added as a test
  dep (see §6 decision).

## 1. Architecture decisions (locked)

1. **Metrics + traces**, both. Metrics via a new `costtrack.Recorder` adapter
   (the documented seam). Traces via a new standalone `callbacks.Handler` that
   creates OTel spans from the eino component lifecycle. Traces are NOT routed
   through the activity bus (the bus distorts timing); the trace handler reads
   `RunInfo` + `callbacks.CallbackInput/Output` directly, mirroring
   `callbacks/log`.
2. **Two-layer for metrics** (per user choice):
   - `libs/otelmetrics/` — a reusable **global metric scope** lib: wraps
     `metric/global.MeterProvider()`, exposes a `Meter`, helpers to create
     counters/histograms, and a labeled **`Gauge`** (observable gauge backed by
     a thread-safe value store) reusable by ANY eino-ext component. This is the
     "share with the library" piece.
   - `libs/costtrack/otel/` — `OTelRecorder` implementing `costtrack.Recorder`
     using `libs/otelmetrics`.
3. **Global MeterProvider / TracerProvider by default, injectable.** Defaults
   read `metric/global.MeterProvider()` and `trace/global.TracerProvider()`;
   `Config` accepts an explicit provider for tests. This is the "global scope"
   semantics — metrics/traces flow to whatever exporter the host app
   configures (OTLP, stdout, prometheus bridge).
4. **`MultiRecorder` (tee)** in `libs/costtrack` so Prometheus + OTel run
   simultaneously (per user choice). Useful for migration and dual-export.
5. **Non-breaking.** The Prometheus path is untouched. OTel is opt-in via
   `Config.Recorder` (metrics) and a separately-registered global handler
   (traces). No existing public API changes.
6. **Cardinality discipline.** Reuse the Prometheus recorder's label sets
   verbatim (`model`, `agent`, `type`, `component`, `reason`); the only
   `session_id`-keyed instruments are the realtime-cost gauge and the cost-saver
   gauges — already documented as "bounded, reused session ids" deployment
   (`libs/costtrack/README.md:93-97`, `callbacks/activity/handler.go:42-49`).
7. **Security.** The trace handler defaults to **redacting** tool input/response
   (tool args and tool response bodies are NOT added as span attributes unless
   `IncludeToolIO` is set; even then truncated to `maxSpanIO=500` chars), to
   avoid leaking secrets/P II into trace backends. This mirrors
   `callbacks/log`'s `truncate` discipline.
8. Follow all `CONTRIBUTING.md`/`AGENTS.md` rules for every new package:
   `Config` with `validate`+`jsonschema` tags, `New...` with `ctx` first param +
   `validate.Struct(cfg)` after defaults, `emperror.dev/errors` wrapping,
   `check.go`+`check_test.go` returning `checkup.Results`, `README.md`, package
   comment, `var _ Interface = (*T)(nil)` compile-time check, table-driven
   tests, no license banners, official casing (`OpenTelemetry`, `URL`, `ID`,
   `JSON`, `HTTP`), `fmt.Sprintf` over `+`, `//go:embed` for any prompt (none
   needed here).

## 2. Data model / API

### 2.1 `libs/otelmetrics` — shared global metric scope (NEW)

```go
// Package otelmetrics is a thin, reusable wrapper over the OpenTelemetry
// global MeterProvider. It is the single "global metric scope" for eino-ext:
// any component can record metrics here and they flow to the host app's
// configured exporter (OTLP, stdout, prometheus bridge). The default provider
// is metric/global.MeterProvider(); an explicit provider can be injected via
// Config for tests.
package otelmetrics

// Config for NewScope. Defaults: MeterName="eino-ext", MeterProvider=global.
type Config struct {
    MeterProvider metric.MeterProvider `json:"-"`
    MeterName     string               `json:"meterName" validate:"omitempty"`
}

// Scope is a configured metric scope. All instrument helpers are nil-receiver
// safe (no-op on a nil Scope) so callers degrade safely.
type Scope struct {
    meter   metric.Meter
    provider metric.MeterProvider
}

// NewScope applies defaults (MeterName="eino-ext", provider=global) then
// validates via libs/toolkit/validate.Struct. ctx threads to future client
// creation; provider resolution is synchronous.
func NewScope(ctx context.Context, cfg *Config) (*Scope, error)

// Meter exposes the underlying metric.Meter (or a noop meter on nil).
func (s *Scope) Meter() metric.Meter

// FloatCounter is a labeled float64 counter (USD-style metrics).
type FloatCounter struct{ /* unexported */ }
func (s *Scope) FloatCounter(name, desc, unit string) (*FloatCounter, error)

// IntCounter is a labeled int64 counter (token/task counts).
type IntCounter struct{ /* unexported */ }
func (s *Scope) IntCounter(name, desc, unit string) (*IntCounter, error)

// Histogram is a labeled float64 histogram (cost distributions).
type Histogram struct{ /* unexported */ }
func (s *Scope) Histogram(name, desc, unit string, buckets []float64) (*Histogram, error)

// Gauge is a labeled observable gauge backed by a thread-safe value store.
// Set(name-keyed by attribute set) updates the latest value; the observable
// gauge callback reads it on collect. Reusable for realtime cost + cost-saver
// snapshot metrics.
type Gauge struct{ /* unexported */ }
func (s *Scope) Gauge(name, desc, unit string) (*Gauge, error)

// Add/AddInt/Set/Record methods take attribute.Set (built via Attrs helper).
// nil-receiver safe.

// Attrs builds an attribute.Set from key/value pairs (string values only —
// enforces the low-cardinality discipline).
func Attrs(kv ...string) attribute.Set
```

Implementation notes:
- `Meter()` returns `metric.NoopMeter{}` on a nil `Scope`.
- `Gauge` keeps a `sync.RWMutex`-guarded `map[attribute.Set]float64` +
  `map[attribute.Set]int64`; registers ONE `Float64ObservableGauge` (and one
  `Int64ObservableGauge` if used) per `Gauge` whose `metric.WithCallback`
  iterates the store under the read lock. `Set` writes are O(1); collect is
  O(labels) which is bounded by cardinality discipline.
- Instruments are created **once** (caller caches them; `Scope` itself does not
  memoize — keep it simple). The `OTelRecorder` (§2.2) holds the instruments as
  struct fields.

### 2.2 `libs/costtrack/otel` — OTelRecorder (NEW)

Implements `costtrack.Recorder`. Instruments (OTel names, units, attrs) mirror
the Prometheus recorder 1:1:

| OTel instrument | type | unit | attrs | Recorder method |
|---|---|---|---|---|
| `llm.tokens` | IntCounter | `{token}` | model,agent,type | `ObserveStep` |
| `llm.cost.usd` | FloatCounter | `USD` | model,agent | `ObserveStep` |
| `llm.cost.savings.usd` | FloatCounter | `USD` | model,agent | `ObserveBreakdown` |
| `llm.cost.by_component.usd` | FloatCounter | `USD` | model,agent,component | `ObserveBreakdown` |
| `agent.tasks` | IntCounter | `{task}` | session_id,agent,real | `RecordTask` |
| `agent.task.cost.usd` | Histogram | `USD` | (none) | `RecordTask` |
| `llm.compactions` | IntCounter | `{compaction}` | agent | `RecordCompaction` |
| `llm.realtime.cost.usd` | Gauge | `USD` | session_id,agent | `SetRealtimeCost` |
| `human.savings.usd` | FloatCounter | `USD` | agent | (`RecordAnalysis` aggregate) |
| `cost_saver.complexity_ratio` | Gauge | `1` | session_id,agent | `RecordAnalysis` |
| `cost_saver.human_time_saved` | Gauge | `s` | session_id,agent | `RecordAnalysis` |
| `cost_saver.money_saved.usd` | Gauge | `USD` | session_id,agent | `RecordAnalysis` |
| `cost_saver.fallback.count` | IntCounter | `{fallback}` | reason | `RecordFallback` |
| `cost_saver.runs` | IntCounter | `{run}` | agent | `RecordAnalysis` |

> Note: OTel counters do NOT carry a `_total` suffix (the suffix is added by
> OTel→Prometheus bridges automatically). Document this divergence from the raw
> Prometheus names (`llm_tokens_total` etc.) in the README.

```go
// Package otel implements costtrack.Recorder against the OpenTelemetry global
// metric scope (libs/otelmetrics). It records the same logical metrics as the
// default PrometheusRecorder, so a deployment can switch backends or dual-export.
package otel

type Config struct {
    Scope *otelmetrics.Scope `json:"-"` // required; built via otelmetrics.NewScope
}

// NewOTelRecorder validates cfg, then lazily creates all instruments on the
// Scope's Meter. ctx threads to instrument creation.
func NewOTelRecorder(ctx context.Context, cfg *Config) (*OTelRecorder, error)

// Compile-time check that OTelRecorder satisfies the costtrack Recorder seam.
var _ costtrack.Recorder = (*OTelRecorder)(nil)

type OTelRecorder struct{ /* unexported instrument fields + gauge stores */ }

// All Recorder methods implemented; nil-receiver safe (no-op on nil), matching
// PrometheusRecorder's discipline (tracker.go:365-423).
func (r *OTelRecorder) ObserveStep(model, agent string, se activity.StepEnded)
func (r *OTelRecorder) ObserveBreakdown(model, agent string, b modelsdev.CostBreakdown)
func (r *OTelRecorder) RecordTask(sessionID, agent string, cost float64, real bool)
func (r *OTelRecorder) RecordCompaction(agent string)
func (r *OTelRecorder) RecordAnalysis(sessionID, agent string, a *activity.ComplexityAnalysis)
func (r *OTelRecorder) RecordFallback(reason string)
func (r *OTelRecorder) SetRealtimeCost(sessionID, agent string, cost float64)
```

`Config` has only `Scope` (an interface-typed pointer, so it cannot use
`validate:"required"` on a pointer; guard manually AFTER `validate.Struct`
returns, mirroring `costtrack.NewTracker`'s manual `cfg.Bus == nil` guards at
`tracker.go:103-111`). `validate.Struct` still runs (no struct tags fail) —
satisfies the "every New... calls validate.Struct" rule.

### 2.3 `libs/costtrack/multi.go` — MultiRecorder (NEW, small)

```go
// MultiRecorder fans every Recorder call out to all non-nil recorders. Used to
// dual-export (e.g. PrometheusRecorder + OTelRecorder) during migration or when
// a deployment wants both /metrics and OTLP. Nil entries are skipped; a nil
// MultiRecorder is a no-op (nil-receiver safe, matching the Recorder contract).
type MultiRecorder []Recorder
func NewMultiRecorder(recorders ...Recorder) MultiRecorder
// (each Recorder method ranges over the slice, calling each non-nil recorder.)
```

### 2.4 `callbacks/oteltrace` — OTel tracing callbacks.Handler (NEW)

Mirrors `callbacks/log/handler.go` dispatch; creates spans instead of logrus
entries.

```go
// Package oteltrace provides a callbacks.Handler that records OpenTelemetry
// spans for eino component lifecycle: a span per chat-model generate (with
// token-usage attributes) and a span per tool call (with name + error
// attributes). Attach globally once with callbacks.AppendGlobalHandlers, or
// per-run with compose.WithCallbacks. The default tracer comes from
// trace/global.TracerProvider(); inject one via Config for tests.
package oteltrace

type Config struct {
    TracerProvider trace.TracerProvider `json:"-"`
    TracerName     string               `json:"tracerName"  validate:"omitempty"`
    SpanKindClient bool                 `json:"spanKindClient"` // tools=CLIENT when true (default true)
    IncludeToolIO  bool                 `json:"includeToolIO"`  // default false: redact tool args/response
    MaxSpanIO      int                  `json:"maxSpanIO"    validate:"omitempty,gte=1"`
}

// NewHandler applies defaults (TracerName="eino-ext", SpanKindClient=true,
// IncludeToolIO=false, MaxSpanIO=500) then validates via validate.Struct.
func NewHandler(ctx context.Context, cfg *Config) (*Handler, error)

var (
    _ callbacks.Handler       = (*Handler)(nil)
    _ callbacks.TimingChecker = (*Handler)(nil)
)

type Handler struct{ tracer trace.Tracer; cfg Config }

// Needed returns true for every timing EXCEPT stream timings when the parent
// context carries no span (avoids stream-copy overhead when nobody traces) —
// mirror activity.Handler.Needed's SubscriberCounter pattern but key off
// trace.SpanFromContext(ctx).IsRecording(). Simpler: always return true for
// OnStart/OnEnd/OnError; for stream timings return trace.SpanFromContext(ctx).IsRecording().
func (h *Handler) Needed(ctx context.Context, info *callbacks.RunInfo, timing callbacks.CallbackTiming) bool

// OnStart: ChatModel → tracer.Start(ctx, "chat_model.generate", kind=INTERNAL,
//   attrs: gen_ai.request.model, agent, session.id, component). Store span in
//   ctx under spanKey. Tool → span "tool.<name>" (kind=CLIENT when
//   SpanKindClient), attrs: tool.name, agent, session.id. Store span+callID.
// OnEnd: ChatModel → set attrs (gen_ai.response.finish_reason,
//   gen_ai.usage.prompt_tokens/completion_tokens/total_tokens) → span.End().
//   Tool → set attr tool.response (redacted unless IncludeToolIO) → span.End().
// OnError: RecordError(err); SetStatus(codes.Error, msg); span.End().
// OnStartWithStreamInput (tool streamed args): start the tool span here (the
//   tool call's "start" for streamed input), store span in ctx, drain stream in
//   a goroutine (MUST close the copied reader), end span on drain completion.
// OnEndWithStreamOutput (model streamed output): the span was started at OnStart;
//   drain the stream in a goroutine (MUST close the copied reader), accumulate
//   token usage, set usage attrs + finish_reason, end span on drain.
//   On mid-stream error: RecordError + SetStatus + end span (mirror
//   activity.Handler.OnEndWithStreamOutput's dangling-state handling,
//   handler.go:308-379).
```

Span parenting: eino's nested runs (supervisor → sub-agent tool → sub-agent
model) are correlated through THIS handler's own context chain (each method
receives the context returned by the previous timing of the SAME handler,
`eino/callbacks/interface.go:69-71`). `tracer.Start(ctx, …)` reads the parent
span from `ctx`, so `OnStart` of a nested component naturally creates a child
span of the enclosing component's span — no manual stack needed beyond storing
the current span in `ctx` via an unexported key (mirror
`callbacks/activity/handler.go:113-116` `ctxKey`).

### 2.5 README / doc updates
- `libs/costtrack/README.md`: replace the "Recorder seam (OpenTelemetry future)"
  section (lines 54-66) with an **"OpenTelemetry"** section documenting
  `otel.NewOTelRecorder`, `costtrack.NewMultiRecorder` for dual-export, and
  `callbacks/oteltrace.NewHandler` for spans. Include a dual-export snippet
  (Prometheus `/metrics` + OTLP via the OTel global provider) and the
  OTel→Prometheus bridge option (`go.opentelemetry.io/otel/exporters/prometheus`
  v0.66.0) as an **optional, caller-side** dep (not added to eino-ext's
  go.mod). Update the "out of scope" wording — OTel is now in scope.
- Each new package gets its own `README.md` (what it does, `New…` snippet, which
  eino abstraction/role it fills).

## 3. Files to create / modify (ordered)

### A. Dependencies
1. `go.mod` / `go.sum`: add direct deps
   - `go.opentelemetry.io/otel` v1.43.0
   - `go.opentelemetry.io/otel/metric` v1.43.0
   Add **test-only** deps:
   - `go.opentelemetry.io/otel/sdk` v1.43.0 (for `sdk/trace` + `tracetest`)
   - `go.opentelemetry.io/otel/sdk/metric` v1.43.0 (for `ManualReader`)
   Run `go mod tidy` after the new packages compile.

### B. `libs/otelmetrics` — shared global metric scope (NEW)
2. `libs/otelmetrics/scope.go`: `Config`, `Scope`, `NewScope`, `Meter`. Defaults:
   `MeterName="eino-ext"`, `MeterProvider=global.MeterProvider()`. `validate.Struct`.
3. `libs/otelmetrics/instruments.go`: `FloatCounter`, `IntCounter`,
   `Histogram`, `Gauge` types + their `Add/AddInt/Set/Record` methods. `Gauge`
   backed by a mutex-guarded label→value store + a single observable-gauge
   callback per `Gauge`. All nil-receiver safe.
4. `libs/otelmetrics/attributes.go`: `Attrs(...string) attribute.Set`.
5. `libs/otelmetrics/noop.go`: re-export `metric.NoopMeterProvider`/noop
   instruments for tests + safe defaults.
6. `libs/otelmetrics/scope_test.go`: table-driven — `NewScope` default vs injected
   provider; counter `Add` then read via `sdk/metric.ManualReader` → assert
   sum; `Gauge.Set` then collect → assert value; nil `Scope`/nil receiver = no-op
   (no panic); `validate.Struct` rejects malformed `MeterName` (none — but test
   that the helper still runs).
7. `libs/otelmetrics/check.go` + `check_test.go`: `Check(ctx, *Scope) checkup.Results`
   — probe that the meter is non-noop and a test counter records. `limited` when
   provider is the noop provider (no exporter configured) — not `error`.
8. `libs/otelmetrics/README.md`: what it does + `NewScope` snippet; note it is
   the "global metric scope" reusable by any eino-ext component.

### C. `libs/costtrack/otel` — OTelRecorder (NEW)
9. `libs/costtrack/otel/recorder.go`: `Config`, `OTelRecorder`,
   `NewOTelRecorder` (ctx first, `validate.Struct` + manual `Scope` guard),
   all `Recorder` methods, `var _ costtrack.Recorder = (*OTelRecorder)(nil)`,
   instrument table from §2.2. nil-receiver safe on every method.
10. `libs/costtrack/otel/recorder_test.go`: table-driven using an
    `sdk/metric.ManualReader`-backed `MeterProvider`:
    - `ObserveStep` → `llm.tokens` (input/output/reasoning/cache_read) +
      `llm.cost.usd` advance; assert via reader `Collect`.
    - `ObserveBreakdown` → `llm.cost.savings.usd` + by-component advance.
    - `RecordTask` (real/!real) → `agent.tasks` + `agent.task.cost.usd` histogram
      sample.
    - `RecordCompaction`/`RecordFallback`/`RecordAnalysis` → respective counters
      + gauges set.
    - `SetRealtimeCost` → gauge observable yields the value on collect.
    - nil receiver = no-op.
    - `Config` missing `Scope` → error (emperror-wrapped).
11. `libs/costtrack/otel/check.go` + `check_test.go`: `Check` probes the scope
    gathers (manual reader) + instruments exist. `limited` (not `error`) when the
    global provider is noop.
12. `libs/costtrack/otel/README.md`: what it does, `NewOTelRecorder` snippet, the
    metric table (OTel names + units + the `_total` bridge note), dual-export
    example with `costtrack.NewMultiRecorder`.

### D. `libs/costtrack/multi.go` — MultiRecorder (NEW)
13. `libs/costtrack/multi.go`: `MultiRecorder`, `NewMultiRecorder`, all
    `Recorder` methods fanning out (skip nil). `var _ Recorder = MultiRecorder{}`.
14. `libs/costtrack/multi_test.go`: table-driven with two `fakeRecorder`
    (copy `fakeRecorder` from `tracker_test.go:319` or import it via a small
    exported test-helper — prefer a local copy to keep test deps internal);
    assert both recorders receive every call; nil entries skipped; nil
    `MultiRecorder` is a no-op.

### E. `callbacks/oteltrace` — tracing handler (NEW)
15. `callbacks/oteltrace/handler.go`: `Config`, `Handler`, `NewHandler` (ctx
    first, `validate.Struct` + defaults), `Needed`, `OnStart`, `OnEnd`,
    `OnError`, `OnStartWithStreamInput`, `OnEndWithStreamOutput`. Span naming:
    `chat_model.generate` (INTERNAL), `tool.<name>` (CLIENT when
    `SpanKindClient`). Attributes per GenAI semantic conventions
    (`gen_ai.request.model`, `gen_ai.usage.*`, `gen_ai.response.finish_reason`)
    + `agent`, `session.id`, `component`, `tool.name`. Redaction default
    (`IncludeToolIO=false`, `MaxSpanIO=500`). Stream goroutines MUST close the
    copied reader (mirror `callbacks/log/handler.go:137,175`).
    `var _ callbacks.Handler`, `var _ callbacks.TimingChecker`.
16. `callbacks/oteltrace/handler_test.go`: table-driven with a
    `sdk/trace` provider + `tracetest.NewInMemoryExporter`:
    - non-stream ChatModel generate → 1 span, attrs incl. usage; OnError path →
      span status Error + recorded exception.
    - non-stream Tool call → 1 span kind CLIENT; redacted (no `tool.response` by
      default); with `IncludeToolIO=true` → truncated response attr.
    - streamed model output → span ends only after stream drain; usage attrs set;
      mid-stream error → status Error.
    - nested (model span starts, tool span starts, both end) → parent/child
      linking via the handler's own ctx chain (assert `ParentSpanID` of tool
      span == model span ID when tool runs inside the model run's ctx).
    - `Needed` returns false for stream timings when no span is recording.
    - `Config` validation: `MaxSpanIO` rejected when `<1` (omitempty,gte=1).
17. `callbacks/oteltrace/check.go` + `check_test.go`: `Check` probes the
    tracer provider is non-noop (limited when noop).
18. `callbacks/oteltrace/README.md`: what it does, `NewHandler` snippet +
    `callbacks.AppendGlobalHandlers(h)`, the redaction default, and how spans
    nest through the handler's context chain.

### F. Docs / repo
19. `libs/costtrack/README.md`: rewrite §"Recorder seam (OpenTelemetry future)"
    → "OpenTelemetry" (§2.5). Add the dual-export snippet.
20. (No `AGENTS.md`/`CONTRIBUTING.md` source changes needed — `libs/` and
    `callbacks/` are already sanctioned locations.)

## 4. Tests

### 4.1 Unit tests (no network, no live LLM)
All new packages: table-driven, in-memory OTel SDK readers/exporters only.
- `libs/otelmetrics`: counter/histogram/gauge round-trips via
  `sdk/metric.ManualReader` + `rm.Collect(ctx)`.
- `libs/costtrack/otel`: every `Recorder` method drives the expected OTel
  instrument; assert via `ManualReader` output `metricdata.Metrics` names +
  data points. Reuse the activity event fixtures from
  `callbacks/activity/metrics/collector_test.go` for realistic `StepEnded`.
- `libs/costtrack/multi`: two recorders both invoked.
- `callbacks/oteltrace`: `tracetest` spans + attributes + status via
  `sdk/trace.NewTracerProvider(sdktrace.WithSpanProcessor(
  sdktrace.NewBatchSpanProcessor(exp)))`; drain `exp` then assert.

### 4.2 Integration (metrics): wire OTelRecorder through costtrack
Reuse the `fakeRecorder`-based harness from
`libs/costtrack/tracker_test.go:TestTracker_FakeRecorder` but swap in a real
`OTelRecorder` on an `sdk/metric` provider with a `ManualReader`. Drive a scripted
sequence of activity events (`step.started`/`step.ended`/`tool.called`/
`compaction.ended`/terminal) and assert the OTel reader exposes the same numbers
the `fakeRecorder` harness already asserts (tokens, cost, tasks, compactions,
realtime gauge). This proves the OTel recorder receives the same events as the
Prometheus one — i.e. the seam is backend-neutral.

### 4.3 Integration (traces): activity handler + oteltrace handler together
Register BOTH `callbacks/activity.NewHandler(bus)` and
`callbacks/oteltrace.NewHandler(ctx, nil)` globally (`AppendGlobalHandlers`),
run a scripted fake `ToolCallingChatModel` (port the `scriptedToolModel`
described in `1783939261746-cost-tracking-helper-plan.md` §4.2 — keep it in a
`*_test.go`), and assert: nested spans (model → tool → model) with correct
parenting, usage attributes, and error status on the failure-scripted case.
No network, no live LLM.

### 4.4 Regression guards
- `callbacks/activity` + `callbacks/activity/metrics` tests unchanged and green
  (the OTel work adds packages; it must not alter the Prometheus path).
- `libs/costtrack` existing tests unchanged and green (the new `MultiRecorder`
  + `otel` sub-package do not touch `tracker.go` semantics).

## 5. Configuration example (for the READMEs)

Dual-export (Prometheus `/metrics` + OTel metrics + traces):

```go
import (
    "github.com/cloudwego/eino/callbacks"
    "github.com/webcenter-fr/eino-ext/callbacks/oteltrace"
    otelcost "github.com/webcenter-fr/eino-ext/libs/costtrack/otel"
    "github.com/webcenter-fr/eino-ext/libs/costtrack"
    "github.com/webcenter-fr/eino-ext/libs/otelmetrics"
)

// Host app configures its global OTel provider once (OTLP, stdout, …) — not
// shown. eino-ext reads metric/global.MeterProvider() and trace/global.TracerProvider()
// by default.

scope, _ := otelmetrics.NewScope(ctx, nil)
otelRec, _ := otelcost.NewOTelRecorder(ctx, &otelcost.Config{Scope: scope})

tracker, err := costtrack.NewTracker(ctx, &costtrack.Config{
    Bus:             activityBus,
    CatalogHolder:   holder,
    PricingProvider: "anthropic",
    Resolve:         resolver,
    Registry:        prometheus.NewRegistry(), // dedicated Prometheus registry
    Recorder:        costtrack.NewMultiRecorder(nil /*prom filled below*/, otelRec),
})
// (PrometheusRecorder is the facade's default when Recorder is nil; to dual-export,
//  build a PrometheusRecorder explicitly OR leave Recorder=nil for prom-only and
//  add OTel via a separate Watch-less recorder fed by the same bus — see README
//  for the canonical dual-export wiring.)
callbacks.AppendGlobalHandlers(tracker.ActivityHandler())

th, _ := oteltrace.NewHandler(ctx, nil) // redacts tool IO by default
callbacks.AppendGlobalHandlers(th)
```

> The exact dual-export wiring (one bus subscription feeding both a
> PrometheusRecorder and an OTelRecorder) is finalized in the README; the
> `MultiRecorder` is the supported path when the facade owns the Recorder slot.

## 6. Validation (run before declaring done)

```bash
go mod tidy
go build ./...
go vet ./...
go test ./libs/otelmetrics/... ./libs/costtrack/... ./callbacks/oteltrace/...
go test ./callbacks/activity/...   # regression
```
- Every new `New...` validates via `validate.Struct(cfg)` after defaults; `ctx`
  first param threaded through.
- Compile-time checks present: `var _ costtrack.Recorder`,
  `var _ callbacks.Handler`, `var _ callbacks.TimingChecker`,
  `var _ costtrack.Recorder = MultiRecorder`.
- No license banners; `fmt.Sprintf` over `+`; official casing (`OpenTelemetry`,
  `OpenSearch`, `URL`, `ID`, `JSON`).
- No live LLM / no network in any test.

## 7. Risks & open items (call out in PR)

- **Cardinality**: `session_id`-keyed gauges (`llm.realtime.cost.usd`,
  cost-saver gauges) are unbounded if session ids are minted per request.
  Inherited caveat — document the bounded-reuse deployment model, mirroring
  `callbacks/activity/handler.go:42-49` and `libs/costtrack/README.md:93-97`.
- **Observable gauge cost**: each `Gauge` holds a label→value map; with bounded
  model/agent sets this is negligible, but a runaway cardinality source would
  grow the map. The README recalls the discipline.
- **Span timing for streamed output**: the model span ends when the stream
  goroutine drains (not at `OnEndWithStreamOutput` entry) — this matches the
  activity handler's stream semantics but means span duration ≈ time-to-last-chunk,
  not full TTFT. Documented in the trace README.
- **Handler context chain is per-handler** (`eino/callbacks/interface.go:69-71`):
  span parenting works within the `oteltrace` handler's own chain only. There is
  NO guaranteed ordering between DIFFERENT global handlers — so an
  `activity.Handler` span (if one existed) and an `oteltrace` span are NOT
  parented to each other. This is acceptable: `oteltrace` owns the span tree.
- **OTel→Prometheus bridge** (`exporters/prometheus`): documented as an
  optional caller-side dep; intentionally NOT added to eino-ext's `go.mod` to
  keep the OTel surface minimal. Revisit if users want `/metrics` sourced from
  OTel instruments directly.
- **GenAI semantic-convention maturity**: attribute names (`gen_ai.*`) are still
  evolving in OTel semconv; pin to the v1.43 conventions documented here and note
  that a future semconv bump may rename them.
- **eino upgrade**: if a future eino version adds native OTel, the
  `oteltrace.Handler` can coexist (handlers compose); revisit for de-duplication
  then. Out of scope for this plan.
