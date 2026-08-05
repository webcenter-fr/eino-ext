# costtrack — real-time LLM cost tracking facade

`costtrack` provides a single builder API that wires the activity bus,
models.dev catalog pricer, Prometheus metrics collector, cost-saver analysis,
and per-session real-time aggregation together. It also exposes HTTP endpoints
for Prometheus `/metrics` and a JSON `/cost/usage` snapshot.

> **"shera" was investigated and rejected**: the "shera" library referenced in
> early designs does not exist as a Go module (pkg.go.dev returns 0 results).
> eino-ext already exports Prometheus metrics via
> `github.com/prometheus/client_golang`, which the facade uses directly.

## Usage

```go
cat := modelsdev.Load(ctxAlreadyCancelled, modelsdev.LoadOptions{})
holder := new(atomic.Pointer[modelsdev.Catalog]); holder.Store(cat)

tracker, err := costtrack.NewTracker(ctx, &costtrack.Config{
    Bus:             activityBus,
    CatalogHolder:   holder,
    PricingProvider: "anthropic",
    Resolve: func(gw string) (provider, id string, ok bool) {
        return "anthropic", gw, true
    },
    Savings: activity.ComplexityAnalyzerConfig{
        HumanHourlyRate: 50,
        BaseTaskTime:    5 * time.Minute,
    },
})
if err != nil {
    panic(err)
}

callbacks.AppendGlobalHandlers(tracker.ActivityHandler())

mux := http.NewServeMux()
tracker.Mount(mux) // /metrics + /cost/usage

go tracker.Watch(runCtx, sessionID)
```

## Root cause: why cost can be silently zero

Gateways (e.g. GitHub Copilot) often omit token usage on streaming responses.
Without a fallback `TokenCounter`, `StepEnded.Tokens` stays all-zero → cost
stays 0 → every metric stays 0.

`NewTracker` **always wires `counter.DefaultTokenCounter`** as a fallback
(default unless overridden in Config), so tokens and cost never silently go
zero. This is the exact fix that landed in the rancher project (commit
`cb81ae3`).

## OpenTelemetry

The `Recorder` interface is the pluggable metric backend seam. OpenTelemetry
adapters are available via:

- **Metrics:** `libs/costtrack/otel` — an `OTelRecorder` implementing
  `Recorder` using the shared OTel metric scope (`libs/otelmetrics`).
- **Traces:** `callbacks/oteltrace` — a `callbacks.Handler` that records
  spans for eino component lifecycle.

Dual-export (Prometheus + OTel) is supported via `MultiRecorder` in
`libs/costtrack`. See the individual package READMEs for usage snippets.

> **Note:** OTel dependencies (`go.opentelemetry.io/otel/*` v1.43.0) are
> direct dependencies of eino-ext. The `otel/exporters/prometheus` bridge is
> an optional, caller-side dependency not included in eino-ext's `go.mod`.

## Metrics

| Metric | Labels | Description |
|---|---|---|
| `llm_tokens_total` | `model`, `agent`, `type` | Token count by type. |
| `llm_cost_usd_total` | `model`, `agent` | Cumulative cost in USD. |
| `llm_cost_savings_usd_total` | `model`, `agent` | USD saved via prompt caching. |
| `llm_cost_usd_by_component_total` | `model`, `agent`, `component` | Cost broken down by component. |
| `agent_tasks_total` | `session_id`, `agent`, `real` | Total agent tasks completed. |
| `agent_task_cost_usd` | (histogram) | Distribution of per-task costs. |
| `llm_compactions_total` | `agent` | Total context compactions. |
| `llm_realtime_cost_usd` | `session_id`, `agent` | Live running cost gauge. |
| `human_savings_usd_total` | `agent` | Estimated human savings from automation. Only incremented for sessions that used at least one tool (a "real" task); trivial no-tool sessions yield zero savings. |
| `cost_saver_runs_total` | `agent` | Cost saver analysis run count. |

## Cost saver

`human_savings_usd_total` and `cost_saver_runs_total` are only incremented for
sessions that called at least one tool — the facade's `isReal` guard. Trivial
no-tool sessions (greetings, chitchat) produce zero savings and do not run the
analyzer. This matches the standalone `metrics.WithCostSaver` path.

## Reasoning-token pricing divergence

Kilocode charges reasoning at the output rate. eino-ext's `costOf` deliberately
does NOT (reasoning is a subset of Output). This divergence is intentional.

## Provider-reported cost precedence

eino-ext's `CatalogPricer` currently has no hook for gateway-reported cost.
Marked as a future extension in the README.

## Cardinality caveat

`llm_realtime_cost_usd{session_id}` and cost-saver gauges are unbounded if
session ids are minted per request. The expected deployment model uses a
bounded, reused set of session ids.
