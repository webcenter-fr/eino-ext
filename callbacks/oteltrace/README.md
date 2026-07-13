# oteltrace — OpenTelemetry tracing callbacks.Handler for eino

Provides a `callbacks.Handler` that records OpenTelemetry spans for eino
component lifecycle: a span per chat-model generate (with token-usage
attributes) and a span per tool call (with name + error attributes).

## Usage

```go
import "github.com/webcenter-fr/eino-ext/callbacks/oteltrace"

th, err := oteltrace.NewHandler(ctx, nil) // redacts tool IO by default
callbacks.AppendGlobalHandlers(th)
```

## Span structure

- ChatModel invocations create spans named `chat_model.generate` (INTERNAL kind)
  with `gen_ai.request.model`, `gen_ai.usage.*`, `gen_ai.response.finish_reason`,
  `agent`, `session.id`, and `component` attributes.
- Tool invocations create spans named `tool.<name>` (CLIENT kind by default)
  with `tool.name`, `agent`, `session.id`, and `component` attributes.

## Span parenting

Nested runs (supervisor → sub-agent tool → sub-agent model) are correlated
through the handler's own context chain: eino passes the context returned by
a handler's previous timing to the same handler's next timing. This means
`tracer.Start(ctx, …)` naturally creates a child span of the enclosing
component's span — no manual stack needed.

## Security: redaction

By default, tool input and response are NOT added as span attributes
(`IncludeToolIO=false`). Set `IncludeToolIO=true` to include them (truncated
to `MaxSpanIO=500` by default) for debugging.

## Span timing for streamed output

The model span ends when the stream goroutine drains the last chunk —
span duration ≈ time-to-last-chunk, not full TTFT.

## Configuration

| Field | Default | Description |
|---|---|---|
| `TracerProvider` | noop | OTel tracer provider |
| `TracerName` | `"eino-ext"` | Tracer name |
| `SpanKindClient` | `true` | Tool spans are CLIENT kind |
| `IncludeToolIO` | `false` | Include tool args/response in spans |
| `MaxSpanIO` | `500` | Max chars for tool IO attributes |
