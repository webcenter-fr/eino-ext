# metrics — Prometheus metrics for activity events

`metrics` provides a Prometheus collector that records token and cost counters
from the activity event stream, labeled by model and agent. It also includes an
optional LLM-based cost saver feature that estimates human time and money saved
by AI automation.

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
| `llm_tokens_total` | `model`, `agent`, `type` | Token count by type (`input`, `output`, `reasoning`, `cache_read`). |
| `llm_cost_usd_total` | `model`, `agent` | Cumulative cost in USD. Populated when a `Pricer` is configured on the activity `Handler`. |
| `llm_cost_savings_usd_total` | `model`, `agent` | Cumulative USD saved via prompt caching. Recorded when `WithBreakdown` is configured. |
| `llm_cost_usd_by_component_total` | `model`, `agent`, `component` | Cost broken down by component (`input`, `output`, `cache_read`, `cache_write`). Recorded when `WithBreakdown` is configured. |
| `cost_saver_complexity_ratio` | `session_id`, `agent` | Complexity ratio of the session (0.0-1.0) computed by LLM analyzer. |
| `cost_saver_human_time_saved_seconds` | `session_id`, `agent` | Estimated human time saved in seconds. |
| `cost_saver_money_saved_usd` | `session_id`, `agent` | Estimated money saved in USD based on human time and hourly rate. |
| `cost_saver_fallback_count_total` | `reason` | Count of fallback to simple formula when LLM analysis failed. |
| `cost_saver_runs_total` | `agent` | Total number of cost saver analysis runs completed. |

## Usage

Basic usage:

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

With cost saver enabled:

```go
import (
    "github.com/cloudwego/eino/components/model"
    "github.com/webcenter-fr/eino-ext/callbacks/activity"
    "github.com/webcenter-fr/eino-ext/callbacks/activity/metrics"
)

bus, _ := activity.NewBus(activity.Config{})
defer bus.Close()

// Create a cost saver analyzer with a small LLM
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
    metrics.WithCostSaver(costSaverConfig, bus))

// Watch a session
go coll.Watch(context.Background(), bus, "my-session")

// When session ends, emit session.ended to trigger cost saver analysis:
bus.Publish(ctx, activity.Event{
    SessionID: "my-session",
    Type:      activity.TypeSessionEnded,
    Agent:     "my-agent",
    Data:      &activity.SessionEnded{Duration: sessionDuration, Cost: totalCost, Steps: steps, Tools: tools},
})
```

## Design

- The collector follows the low-cardinality label discipline: `model` and
  `agent` values are from controlled enumerations or caller-provided strings.
- Tokens are a Counter (monotonic), matching Prometheus best practices for
  cumulative metrics.
- `Watch` subscribes to `step.ended` events from the activity bus and records
  tokens and cost for each step that has token data.
- Cost saver metrics use gauges since they represent a snapshot of analysis
  results at session end time.
- The cost saver uses an LLM to analyze session complexity and estimate
  human time/money saved, with a fallback to a simple formula when LLM analysis
  fails.
- Session end is signaled by publishing a `session.ended` event on the bus
  (typically from the MemoryAgent's `EndSession()` method). The cost saver
  subscriber in `Watch` receives this event and runs the analysis. Session
  summary is built retrospectively from the bus ring buffer via `Replay()`.
