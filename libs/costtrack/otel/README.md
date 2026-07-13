# costtrack/otel — OpenTelemetry Recorder for costtrack

Implements `costtrack.Recorder` using the OpenTelemetry global metric scope
(`libs/otelmetrics`). Records the same logical metrics as the default
`PrometheusRecorder`, enabling deployments to export LLM cost data to any OTel
backend (OTLP, stdout, Prometheus bridge).

## OTel instrument names vs Prometheus

OTel counters do NOT carry a `_total` suffix; the suffix is added by
OTel→Prometheus bridges automatically.

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
| `cost_saver.complexity_ratio` | Gauge | `1` | session_id,agent | `RecordAnalysis` |
| `cost_saver.human_time_saved` | Gauge | `s` | session_id,agent | `RecordAnalysis` |
| `cost_saver.money_saved.usd` | Gauge | `USD` | session_id,agent | `RecordAnalysis` |
| `cost_saver.fallback.count` | IntCounter | `{fallback}` | reason | `RecordFallback` |
| `cost_saver.runs` | IntCounter | `{run}` | agent | `RecordAnalysis` |

## Usage

```go
import (
    otelcost "github.com/webcenter-fr/eino-ext/libs/costtrack/otel"
    "github.com/webcenter-fr/eino-ext/libs/otelmetrics"
)

scope, _ := otelmetrics.NewScope(ctx, nil)
otelRec, _ := otelcost.NewOTelRecorder(ctx, &otelcost.Config{Scope: scope})

tracker, err := costtrack.NewTracker(ctx, &costtrack.Config{
    // ...
    Recorder: otelRec,
})
```

For dual-export (Prometheus + OTel), use `costtrack.NewMultiRecorder`.
