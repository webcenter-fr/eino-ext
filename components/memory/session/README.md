# session — Cross-request conversation lifecycle

`session` provides the generic, **cross-request** lifecycle for persisting a
conversation history on top of a [`memory.Memory`](../) store. It is the
counterpart of the **intra-run** optimization middleware
([`contextopt`](../../middleware/contextopt)): the middleware compacts the
message list *within a single `runner.Run`* and persists nothing, while this
package owns the durable, listable/deletable history *between requests*.

It encapsulates:

- **per-session locking** — one logical lock per `"<userId>:<id>"`, with
  **ref-counted cleanup** so the lock map never leaks entries (fixes the classic
  "one mutex per session id forever" leak);
- **the turn lifecycle** — `BeginTurn → [Condense] → Window → CommitAssistant`;
- **optional non-destructive condensation** — an anchored summary driven by a
  token threshold, reusing an injected `Summarizer`.

The package is intentionally policy-free: filtering of agent events, SSE wiring
and the choice of `Summarizer` implementation are injected by the caller.

## Usage

```go
mem := file.NewFileMemory(file.FileMemoryConfig{
    Dir:             "/var/lib/app/history",
    TokenCounter:    counter,   // share the SAME counter everywhere (see below)
    MaxWindowTokens: 24_000,
})

// Reuse contextopt's LLM summarizer: a single instance feeds both the
// intra-run middleware and this cross-request condenser.
summarizer := contextopt.NewModelSummarizer(chatModel)

sm, err := session.NewSessionManager(session.Config{
    Memory:            mem,
    Summarizer:        summarizer, // nil disables condensation
    CondenseThreshold: 16_000,     // tokens; <= 0 disables
    WindowBudget:      24_000,     // tokens fed to the model
    TokenCounter:      counter,
})
if err != nil {
    return err
}
```

Per request:

```go
turn, err := sm.BeginTurn(userID, convID, schema.UserMessage(userInput))
if err != nil {
    return err
}
// The user message is held pending (visible to Window/Condense) and persisted
// only by CommitAssistant. Discard persists nothing, so an aborted run never
// leaves a dangling user message and a retry cannot duplicate it.
defer turn.Discard() // releases the lock if CommitAssistant is not reached

if _, err := turn.Condense(ctx); err != nil { // appends an anchored summary at threshold
    return err
}

msgs := turn.Window(0) // [last summary + recent], bounded by WindowBudget
// ... run the agent with msgs, stream to the client, build the assistant reply ...

if err := turn.CommitAssistant(assistantMsg); err != nil { // append + unlock
    return err
}
```

Management:

```go
ids, _ := sm.ListConversations(userID)
_ = sm.DeleteConversation(userID, convID) // also drops the lock entry
```

## Interop with `contextopt`

Persisted summaries use the shared marker `memory.SummaryMarkerKey` (via
`memory.NewSummaryMessage`), so they are recognized natively by `contextopt`
(`trimBeforeLastSummary`, `lastSummaryText`) and vice-versa. The `Summarizer`
interface here is structurally identical to `contextopt.Summarizer`, so one
`contextopt.NewModelSummarizer(...)` instance satisfies both — **no import
dependency** on `contextopt` is introduced.

### Avoiding double summarization cost

When both layers are active, a turn must never pay the LLM summarization cost
twice. Guarantee it by enforcing, together:

1. the **same `TokenCounter`** in `session.Config`, the memory store, and the
   `contextopt` middleware;
2. **`WindowBudget` ≤ the middleware's usable window**
   (`MaxInputTokens`, else `ContextLimit − ReservedTokens`);
3. shared summary markers (already the case).

Then the post-`Condense` window `[summary + tail]` cannot overflow on the first
model call, so the middleware does not re-summarize the already-condensed
history. Any later middleware summarization is genuine intra-run overflow
(incremental via `previousSummary`), not a duplicate.

## API

| Type / method | Purpose |
| --- | --- |
| `NewSessionManager(Config)` | Validate config and build the manager. |
| `SessionManager.BeginTurn(userId, id, userMsg)` | Lock + load/create; user message held pending → `*Turn`. |
| `SessionManager.ListConversations(userId)` | Forward to the store. |
| `SessionManager.DeleteConversation(userId, id)` | Delete + drop the lock entry. |
| `Turn.Window(budget)` | `[last summary + recent]`, bounded by budget (0 = `WindowBudget`). |
| `Turn.Condense(ctx)` | Append an anchored summary when the window reaches the threshold. |
| `Turn.CommitAssistant(msg)` | Persist pending user message + assistant message, release lock (double-commit guarded). |
| `Turn.Discard()` | Release lock without persisting (drops pending user message; idempotent; use with `defer`). |
| `Turn.Conversation()` | Access the underlying `memory.Conversation`. |
