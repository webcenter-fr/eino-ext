# otelmetrics — OpenTelemetry global metric scope for eino-ext

`otelmetrics` is a thin, reusable wrapper over the OpenTelemetry global
`MeterProvider`. It is the single shared "global metric scope" for eino-ext:
any component can record metrics here and they flow to the host app's
configured exporter (OTLP, stdout, Prometheus bridge).

## Usage

```go
import "github.com/webcenter-fr/eino-ext/libs/otelmetrics"

scope, err := otelmetrics.NewScope(ctx, nil) // uses global MeterProvider
```

Inject a custom provider for tests:

```go
scope, err := otelmetrics.NewScope(ctx, &otelmetrics.Config{
    MeterProvider: sdkmetric.NewMeterProvider(sdkmetric.WithReader(rdr)),
})
```

## Instruments

- **FloatCounter** — labeled float64 counter (USD-style metrics)
- **IntCounter** — labeled int64 counter (token/task counts)
- **Histogram** — labeled float64 histogram with configurable buckets
- **Gauge** — labeled observable gauge backed by a thread-safe value store

All instruments are nil-receiver safe: calling any method on a nil pointer is
a no-op.
