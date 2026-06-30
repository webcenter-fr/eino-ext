# runner — adk.Runner → session bridge

`runner` makes streaming **and** history persistence work out of the box for any
eino project, removing the per-project glue that consumes an `adk.Runner`
iterator, proxies tokens to the client, and persists the full answer through a
[`session.Turn`](../session).

It is the only place in `components/memory` that imports `adk`, keeping the
[`session`](../session) package policy-free and adk-free.

## What it does

`Run` consumes the `adk.AsyncIterator` returned by `adk.Runner.Run` and splits it
into two concurrent halves over a single duplicated stream:

- a **proxy** goroutine forwards the selected assistant tokens/messages to the
  returned `StreamReader` (token by token for streaming events) — this is what
  you stream back to the client;
- a **persistence** goroutine drains the second copy, concatenates the full
  assistant answer and commits it through the `Turn`.

### Guarantees

- **no-dangling-user** — if no assistant content is produced (or concatenation
  fails), the `Turn` is `Discard()`ed, so the pending user message is never
  persisted alone and a retry cannot duplicate it;
- **incomplete** — on an iterator error or a truncated stream, the committed
  assistant message is tagged with `memory.MarkIncomplete`;
- **ephemeral** — `OnError` notices (`memory.NewEphemeralMessage`) are streamed
  to the client but excluded from persistence, as are messages carrying tool
  calls.

The run is driven under `context.Background()` inside the bridge so a client
disconnection neither aborts generation nor persistence. Condensation stays the
caller's responsibility under the request context, **before** calling `Run`.

## Usage

```go
turn, err := sm.BeginTurn(userID, convID, schema.UserMessage(userInput))
if err != nil {
    return err
}
defer turn.Discard() // no-op once the bridge commits/discards

if _, err := turn.Condense(ctx); err != nil { // under the request context
    return err
}

iter := agentRunner.Run(context.Background(), turn.Window(0))

stream, err := runner.Run(runner.Config{
    Turn:      turn,
    Iterator:  iter,
    Predicate: runner.AgentRole("supervisor", schema.Assistant), // nil => assistant-only
    OnError: func(err error) *schema.Message {
        return memory.NewEphemeralMessage(schema.Assistant, "an error occurred")
    },
})
if err != nil {
    return err
}

// Stream `stream` back to the client (e.g. over SSE). Persistence happens
// asynchronously and releases the session lock when done.
```

## Predicates

`Predicate` selects which events are streamed and persisted, by emitting agent
name and message role. When `nil`, every assistant-role message is streamed and
persisted. Composable helpers are provided:

| Helper | Purpose |
| --- | --- |
| `AgentRole(name, role)` | Match a specific agent + role (e.g. supervisor + assistant). |
| `Role(role)` | Match a role regardless of the agent. |
| `And`, `Or`, `Not` | Combine predicates. |

## API

| Type / function | Purpose |
| --- | --- |
| `Run(Config)` | Start the bridge; returns the client `StreamReader`. |
| `Config.Turn` | Required locked session turn (committed/discarded by the bridge). |
| `Config.Iterator` | Required `adk.AsyncIterator` from `adk.Runner.Run`. |
| `Config.Predicate` | Stream/persist selector; `nil` => assistant-only. |
| `Config.OnError` | Optional ephemeral error notice builder. |
| `Config.OnSkip` | Optional observer of filtered-out events. |
| `Config.BufferSize` | Pipe buffer size; `<= 0` => `DefaultBufferSize` (1000). |
