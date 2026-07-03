# pricer — Token cost calculation interface

`pricer` defines the interface and data types for computing the USD cost of
token usage for a model invocation.

## Types

```go
type CacheTokens struct {
    Read  int
    Write int
}

type Tokens struct {
    Input      int
    Output     int
    Reasoning  int
    Cache      CacheTokens
}
```

## Interface

```go
type Pricer interface {
    Cost(model string, tokens Tokens) float64
}
```

`Cost` returns the total USD cost for the given token counts on the given model.
Implementations should handle unknown models by returning 0.

## Related

- `libs/modelsdev` — a `Pricer` implementation backed by the models.dev catalog
  with per-model input/output/cache pricing rates.
- `callbacks/activity/metrics` — Prometheus collector that records cost via a
  `Pricer`.
