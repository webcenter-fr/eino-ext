# Agent Activity Stream (eino-ext)

Expose a Kilocode-style live "activity" stream of what agents / LLMs / tools are
doing, consumable from a UI via SSE. Modeled on opencode/Kilocode's central typed
event bus (`packages/core/src/event.ts` + `session-event.ts`) fanned out over SSE.

## Decisions (locked)

- **Transport-agnostic core** with a separate Hertz SSE adapter subpackage. The
  core has zero web dependencies; `hertz` is imported only in the `sse` subpkg.
- **Full streaming deltas** (Kilocode-like): emit token-level `text.delta`,
  `reasoning.delta`, and `tool.input.delta` in addition to coarse lifecycle
  events.

## Architecture

Three layers, mirroring Kilocode:

1. **Event model + bus** (transport-agnostic) — like `event.ts`.
2. **eino producer** — an `callbacks.Handler` (+ optional `adk` middleware) that
   translates eino component lifecycle into events. This is the bridge that
   replaces Kilocode's manual `publish` calls.
3. **SSE adapter** (Hertz) — fans `bus.Subscribe` out as `text/event-stream`.

Producers publish granular events to one bus keyed by `SessionID`; transport is a
thin reader over the bus. UIs can also consume the bus in-process.

## Package layout

```
components/observability/activity/
  event.go        # Event struct, Type constants, typed Data payloads, JSON tags
  bus.go          # Bus interface + in-memory impl (per-session fan-out, replay)
  context.go      # WithSession / SessionFromContext
  handler.go      # callbacks.Handler + TimingChecker bridge
  adk.go          # (optional) adk.AgentEvent -> activity events bridge
  *_test.go
  README.md
components/observability/activity/sse/
  sse.go          # Hertz sse.Stream writer over Bus.Subscribe
  sse_test.go
  README.md
```

Follow existing eino-ext conventions: Apache 2.0 header on every file (see
`components/memory/session/session.go`), `validator/v10` on Config structs,
`emperror.dev/errors` wrapping, table-driven tests, package-doc comments.

## 1. Event model (`event.go`)

```go
type Event struct {
    ID        string    // monotonic, e.g. "evt_" + ascending counter (Last-Event-ID)
    SessionID string
    Type      Type      // string enum
    Timestamp time.Time
    Data      any       // one of the typed payloads below; JSON-tagged
}
```

`Type` constants and payloads ported from `session-event.ts` (drop the
`session.next.` prefix), each as a small typed struct:

- Lifecycle: `step.started` (agent, model), `step.ended` (finish, cost, tokens
  {input,output,reasoning,cache{read,write}}), `step.failed` (error),
  `agent.switched` (agent), `model.switched` (model), `prompted`.
- Text: `text.started`, `text.delta` (delta), `text.ended` (text).
- Reasoning: `reasoning.started/delta/ended` (reasoningID, delta/text).
- Tool: `tool.input.started` (callID, name), `tool.input.delta` (callID, delta),
  `tool.input.ended` (callID, text), `tool.called` (callID, tool, input),
  `tool.progress`, `tool.success` (callID, content), `tool.failed` (callID, error).
- Misc (optional, parity): `retried`, `compaction.started/delta/ended`.

Provide `MarshalSSEData(Event) ([]byte, error)` producing the `data:` JSON body.

## 2. Bus (`bus.go`)

```go
type Bus interface {
    Publish(ctx context.Context, e Event)
    Subscribe(ctx context.Context, sessionID string, lastEventID string) (<-chan Event, func())
    Close() error
}
```

In-memory impl:
- Per-`SessionID` subscriber registry (map guarded by `sync.RWMutex`).
- Bounded ring buffer per session (configurable size, e.g. 256) for replay so a
  late subscriber passing `lastEventID` receives missed events, then live ones.
- Non-blocking send to subscriber channels; on a full/slow channel, drop oldest
  or drop the subscriber (configurable) to avoid blocking the agent run.
- Monotonic ID generator (atomic counter) for ordering / `Last-Event-ID`.
- `Subscribe` returns an unsubscribe func; honor `ctx.Done()` for cleanup.

`Config` (validated): buffer size, slow-subscriber policy, optional clock.

## 3. Context correlation (`context.go`)

- `WithSession(ctx, id) context.Context` and `SessionFromContext(ctx) (string, bool)`.
- Set by the caller at run start; read by `Handler` so concurrent graph runs do
  not cross-talk. If absent, events use a fallback/empty session (still publish).

## 4. eino producer (`handler.go`)

`Handler` implements `callbacks.Handler` and `callbacks.TimingChecker`.

- Branch on `RunInfo.Component` (`components.ComponentOfChatModel`,
  `ComponentOfTool`, `ComponentOfPrompt`, graph types) — never assume ordering.
- Correlate via `SessionFromContext(ctx)`; allocate a `callID`/`stepID` and stash
  it in the returned context (`context.WithValue`) so `OnEnd`/`OnError` of the
  SAME handler can pair with `OnStart` (per callbacks contract).
- ChatModel:
  - `OnStart` -> `step.started` / `text.started`.
  - `OnEndWithStreamOutput` -> read the COPIED `*schema.StreamReader`, emit
    `text.delta` / `reasoning.delta` per chunk, then `text.ended`; extract
    token usage/cost -> `step.ended`. **Always `defer out.Close()`** (leak risk).
  - Non-streaming `OnEnd` -> `text.ended` + `step.ended` directly.
- Tool:
  - `OnStart` -> `tool.input.started` + `tool.called` (name from `RunInfo`,
    input from callback input).
  - `OnStartWithStreamInput` -> `tool.input.delta`...`tool.input.ended`
    (`defer in.Close()`).
  - `OnEnd` -> `tool.success` (content from output); `OnError` -> `tool.failed`.
- `TimingChecker.Needed(...)` returns false for stream timings when there are no
  subscribers, to skip stream copies/goroutines on hot paths.
- **Do NOT mutate** callback Input/Output (shared pointers -> data races).

Attachment: `callbacks.AppendGlobalHandlers(h)` at startup (one process), or
per-run `compose.WithCallbacks(h)` / `adk` option; document both.

## 5. adk bridge (`adk.go`, optional)

Bridge `adk.AgentEvent` (AgentName, Output, Action, Err) to `agent.switched` /
action / error events for multi-agent flows. Implement as a thin consumer of the
adk event stream or an `adk` middleware; keep it optional and dependency-light.

## 6. SSE adapter (`sse/sse.go`, Hertz)

- Handler factory `NewHandler(bus Bus) app.HandlerFunc` (Hertz) using
  `hertz` `sse.NewStream` / `sse.Stream`.
- Read `session` from query/path and `Last-Event-ID` header; call
  `bus.Subscribe(ctx, session, lastID)`.
- For each event: write SSE frame `id: <Event.ID>`, `event: <Type>`,
  `data: <json>`; flush.
- Heartbeat ticker (comment line `:` or `event: ping`) to keep connections alive.
- Cleanup on `ctx.Done()` / client disconnect via the unsubscribe func.
- Add `hertz` to `go.mod` only because of this subpackage.

## UI consumption (doc only)

- Open `EventSource('/events?session=ID')`; reconnect uses `Last-Event-ID`.
- Render banner from `tool.*` ("Running <tool>…", success/fail), live text from
  `text.delta`, reasoning from `reasoning.delta`, and counters from `step.ended`
  (tokens/cost) — same UX as Kilocode.

## Invariants / risks to encode

- MUST `Close()` every copied stream reader in stream timings (else pipeline
  goroutine/memory leak).
- MUST NOT mutate callback Input/Output.
- Bus sends MUST be non-blocking; never block an agent run on a slow UI client.
- Session isolation: no cross-session delivery; empty session is a valid bucket.
- Implement `TimingChecker` to avoid stream-copy overhead when unused.
- IDs monotonic per process for correct `Last-Event-ID` replay ordering.

## Implementation task list (ordered)

1. Scaffold `components/observability/activity/` package + Apache headers + doc.
2. `event.go`: Type constants + typed payloads + SSE marshal helper.
3. `context.go`: session context helpers.
4. `bus.go`: in-memory Bus (fan-out, ring-buffer replay, ID gen) + Config +
   tests (subscribe/replay/slow-subscriber/unsubscribe/concurrent publish).
5. `handler.go`: `callbacks.Handler` + `TimingChecker` mapping all components;
   tests using a fake graph/run + an in-memory bus asserting emitted events.
6. (Optional) `adk.go`: adk.AgentEvent bridge + test.
7. `sse/sse.go`: Hertz SSE handler + test (httptest-style); add `hertz` dep.
8. READMEs for both packages; wire example in `bin/` or docs.
9. `make` build/test (see `Makefile`); ensure `go vet` / lint pass.

## Validation

- Unit tests per file (table-driven), `go test ./components/observability/...`.
- Race detector: `go test -race` for bus + handler (concurrency-critical).
- Manual: run a sample agent with the global handler + SSE endpoint, `curl -N`
  the stream, confirm deltas + step.ended counters, then reconnect with
  `Last-Event-ID` and confirm replay.

## Out of scope

- Persistence/storage of events (in-memory only; pluggable Bus interface allows a
  later durable impl).
- Auth on the SSE endpoint (left to the host app).
- Non-Hertz transports (net/http adapter can be added later behind the same Bus).

## Open questions (non-blocking)

- Final package name: `observability/activity` vs `activity` vs `agentstream`.
- Whether to ship the optional `adk.go` bridge in v1 or defer to a follow-up.
