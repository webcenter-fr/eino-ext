# metrics — Prometheus metrics for activity events

`metrics` provides a Prometheus collector that records token and cost counters
from the activity event stream, labeled by model and agent.

## Collector

```go
c := metrics.NewCollector(prometheus.DefaultRegisterer)

// Watch a session's activity bus
go metrics.Watch(ctx, bus, sessionID)
// or feed events individually:
// c.Observe(model, agent, stepEndedEvent)
```

## Metrics

| Metric | Labels | Description |
|---|---|---|
| `llm_tokens_total` | `model`, `agent`, `type` | Token count by type (`input`, `output`, `reasoning`, `cache_read`, `cache_write`). |
| `llm_cost_usd_total` | `model`, `agent` | Cumulative cost in USD. Populated when a `Pricer` is configured on the activity `Handler`. |

## Usage

```go
import (
    "github.com/webcenter-fr/eino-ext/callbacks/activity"
    "github.com/webcenter-fr/eino-ext/callbacks/activity/metrics"
    "github.com/webcenter-fr/eino-ext/libs/modelsdev"
)

bus, _ := activity.NewBus(activity.Config{})
defer bus.Close()

cat := modelsdev.Load(ctx, modelsdev.LoadOptions{})
h := activity.NewHandlerWithConfig(bus, activity.WithPricer(modelsdev.CatalogPricer{
    Catalog: cat,
    Resolve: func(gm string) (string, string, bool) { return "anthropic", gm, true },
}))

coll := metrics.NewCollector(prometheus.DefaultRegisterer)
go metrics.Watch(context.Background(), bus, "my-session")
```

## Design

- The collector follows the low-cardinality label discipline: `model` and
  `agent` values are from controlled enumerations or caller-provided strings.
- Tokens are a Counter (monotonic), matching Prometheus best practices for
  cumulative metrics.
- `Watch` subscribes to `step.ended` events from the activity bus and records
  tokens and cost for each step that has token data.
