# activity/sse

A [Hertz](https://github.com/cloudwego/hertz) **Server-Sent Events** adapter that
fans an [`activity.Bus`](../) out to a UI as `text/event-stream`.

This is the **only** package in the activity tree that depends on a web
framework; the core (event/bus/handler) stays transport-agnostic.

## Usage

```go
bus, _ := activity.NewBus(activity.Config{})

h := server.Default()
h.GET("/events", sse.NewHandler(sse.Config{
    Bus:               bus,
    SessionQuery:      "session",       // default
    HeartbeatInterval: 15 * time.Second, // default; <= 0 disables
}))
h.Spin()
```

Each connection:

- reads the session id from the `session` query parameter;
- reads the `Last-Event-ID` request header and asks the bus to replay missed
  events before streaming live ones;
- writes one SSE frame per event (`id:`, `event: <Type>`, `data: <json>`), where
  the JSON body is `activity.MarshalSSEData(event)` (the typed `Data` payload);
- emits periodic `event: ping` heartbeats to hold the connection open through
  proxies;
- unsubscribes from the bus on client disconnect (`ctx.Done()`).

## UI consumption

```js
const es = new EventSource('/events?session=ID')
// EventSource automatically reconnects and sends Last-Event-ID.

es.addEventListener('text.delta', (e) => append(JSON.parse(e.data).delta))
es.addEventListener('reasoning.delta', (e) => appendReasoning(JSON.parse(e.data).delta))
es.addEventListener('tool.called', (e) => banner(`Running ${JSON.parse(e.data).tool}…`))
es.addEventListener('tool.success', () => banner('done'))
es.addEventListener('step.ended', (e) => {
  const { cost, tokens } = JSON.parse(e.data)
  showCounters(cost, tokens)
})
```

## Out of scope

- **Auth** on the endpoint — left to the host app (wrap with your middleware).
  Activity events carry sensitive data (prompts, tool args/results), so the
  endpoint must enforce that callers only subscribe to sessions they own.
- **Connection limits / rate limiting** — each connection holds one bus
  subscriber; cap concurrent connections at the host/proxy layer to avoid
  resource exhaustion from many open streams.
- **Persistence** — the in-memory bus is ephemeral; swap in a durable
  `activity.Bus` implementation behind the same interface for replay across
  restarts.
