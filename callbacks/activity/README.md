# activity

A Kilocode-style live **activity stream** of what eino agents, chat models and
tools are doing during a run. It is transport-agnostic: this package has **zero
web dependencies**. A Hertz SSE adapter lives in the [`sse`](./sse) sub-package.

It mirrors opencode/Kilocode's central typed event bus, in three layers:

1. **Event model** (`event.go`) — a flat `Event` envelope plus a catalog of typed
   `Data` payloads, ported 1:1 from Kilocode's session-event catalog (with the
   `session.next.` type prefix dropped).
2. **Bus** (`bus.go`) — an in-memory, per-session fan-out with bounded
   ring-buffer replay so a reconnecting subscriber can resume from a
   `Last-Event-ID`.
3. **Producer** (`handler.go`) — a `callbacks.Handler` that translates eino
   component lifecycle into events. An optional adk bridge can be added later.

## Quick start

```go
bus, err := activity.NewBus(activity.Config{})
if err != nil {
    log.Fatal(err)
}
defer bus.Close()

h := activity.NewHandler(bus)

// Attach globally once at startup (NOT thread-safe)...
callbacks.AppendGlobalHandlers(h)
// ...or per run:
//   runnable.Invoke(ctx, in, compose.WithCallbacks(h))

// Correlate a run to a fan-out bucket:
ctx := activity.WithSession(context.Background(), "session-123")
_, _ = runnable.Invoke(ctx, input)
```

Subscribe in-process (or let the `sse` adapter do it for a UI):

```go
events, unsubscribe := bus.Subscribe(ctx, "session-123", "" /* lastEventID */)
defer unsubscribe()
for e := range events {
    fmt.Printf("%s %s %+v\n", e.ID, e.Type, e.Data)
}
```

## Event catalog

| Group     | Types |
|-----------|-------|
| Lifecycle | `step.started`, `step.ended`, `step.failed`, `agent.switched`, `model.switched`, `prompted` |
| Text      | `text.started`, `text.delta`, `text.ended` |
| Reasoning | `reasoning.started`, `reasoning.delta`, `reasoning.ended` |
| Tool      | `tool.input.started`, `tool.input.delta`, `tool.input.ended`, `tool.called`, `tool.progress`, `tool.success`, `tool.failed` |
| Misc      | `retried`, `compaction.started`, `compaction.delta`, `compaction.ended` |

`step.ended` carries the finish reason, cost and a token breakdown
(`{input, output, reasoning, cache{read, write}}`) — the same shape Kilocode's UI
renders for token/cost counters.

## Bus semantics & invariants

- **Non-blocking publish.** A slow or stalled UI client can never block an agent
  run. On a full subscriber channel the configured `SlowPolicy` either drops the
  event (`DropEvent`, default) or unsubscribes the laggard (`DropSubscriber`).
- **Replay.** Each session keeps a bounded ring buffer (`BufferSize`, default
  256). Subscribing with a `lastEventID` replays buffered events newer than it,
  then live events follow in order. The buffer is retained across brief
  subscriber gaps so an auto-reconnecting `EventSource` resumes cleanly.
- **Bounded sessions.** The number of retained sessions is capped by
  `MaxSessions` (default 4096); when exceeded, the least-recently-active session
  with **no active subscribers** is evicted, so a long-running process that runs
  many distinct sessions cannot grow memory without bound. Sessions with a live
  subscriber are never evicted.
- **Session isolation.** No cross-session delivery; the empty session id is a
  valid bucket.
- **Monotonic IDs.** Event IDs are `evt_<n>` from a process atomic counter, which
  is what `Last-Event-ID` replay ordering relies on.

## Producer invariants (eino callbacks contract)

The `Handler` follows the eino callbacks rules strictly:

- Copied stream readers in stream timings are **always `Close()`d** — otherwise
  the whole pipeline leaks goroutines/memory.
- Callback `Input`/`Output` are **never mutated** (shared pointers → data races).
- Start/end pairing uses only the **same handler's** context chain
  (`context.WithValue`); no ordering is assumed between different handlers.
- `TimingChecker.Needed` returns `false` for the expensive stream timings when
  the bus reports **no subscribers** for the session, skipping stream copies on
  hot paths.

## Configuration

```go
activity.Config{
    BufferSize:          256,                 // per-session replay ring buffer
    SubscriberQueueSize: 64,                  // extra per-subscriber buffering
    MaxSessions:         4096,                // cap on retained sessions (LRU evicts idle)
    SlowPolicy:          activity.DropEvent,  // or activity.DropSubscriber
    Clock:               time.Now,            // override in tests
}
```
