# contextopt + persistent memory — pay for summarization once

This example shows how to run the two summarizing layers of `components/memory`
together **without paying the LLM summarization bill twice**:

| Layer | Package | When it summarizes | Persists? |
| --- | --- | --- | --- |
| Cross-request condenser | [`memory/session`](../../session) | Between requests, when the stored window exceeds `CondenseThreshold` | Yes — anchored summary written to the store |
| Intra-run optimizer | [`middleware/contextopt`](../../../middleware/contextopt) | Inside a single `adk.Runner.Run`, on real context-window overflow | No — compacts the in-flight message list only |

Both can call the **same** (expensive) LLM `Summarizer`. Wired naively, the same
history gets summarized once by each layer per turn — **double the cost**. This
sample wires them so the cost is provably paid **at most once per turn**.

## Run it

No API key required — the chat model is a local stub and the `Summarizer` is
instrumented to count its LLM calls.

```bash
go run ./components/memory/examples/contextopt_persistent
```

Expected output:

```
turn 1: condensed=false summarization LLM calls this turn = 0
turn 2: condensed=true  summarization LLM calls this turn = 1
turn 3: condensed=true  summarization LLM calls this turn = 1
turn 4: condensed=true  summarization LLM calls this turn = 1

Total summarization LLM calls over 4 turns: 3 (max 1 in any single turn).
The history was summarized AT MOST ONCE per turn — never twice.
```

The middleware is fully active (it still trims, prunes and compacts every turn),
but it never adds a *second* summarization call on top of the cross-request
`Condense`.

## The three-part invariant

A turn pays the summarization cost twice only if **both** layers decide to
summarize. To make that impossible, enforce all three of these **together**:

1. **One `TokenCounter` everywhere** — the same counter in the memory store,
   `session.Config`, and `contextopt.Config`. If the layers disagree on how big
   the window is, their overflow decisions diverge and one re-summarizes what the
   other already did.
2. **`WindowBudget` ≤ the middleware's usable window** — the usable window is
   `MaxInputTokens` (else `ContextLimit − ReservedTokens`). Because `Condense`
   bounds the persisted window to `WindowBudget`, the messages handed to the
   model on the first call of a turn already fit the middleware's budget, so
   `contextopt.IsOverflow` returns `false` and its `Summarizer` is **not called**.
3. **Shared summary markers** — automatic. `Condense` writes summaries via
   `memory.NewSummaryMessage`, and `contextopt` recognizes them through
   `memory.IsSummary` (`trimBeforeLastSummary`, `lastSummaryText`). The persisted
   summary is the truncation frontier both layers agree on.

In the example these map to:

```go
counter := memory.DefaultTokenCounter // (1) shared by store, session, middleware

session.Config{
    WindowBudget: usableWindow,   // (2) == middleware MaxInputTokens
    TokenCounter: counter,        // (1)
    Summarizer:   sharedSummarizer,
    CondenseThreshold: 3_000,     // < usableWindow, so Condense fires first
}

contextopt.Config{
    MaxInputTokens: usableWindow, // (2)
    TokenCounter:   counter,      // (1)
    Summarizer:     sharedSummarizer,
}
```

A single `contextopt.NewModelSummarizer(model)` instance feeds **both** layers
(the `session.Summarizer` interface is structurally identical to
`contextopt.Summarizer`, with no import dependency between the packages).

## Why the cost stays at one call per turn

```
request
  │
  ├─ BeginTurn ─ holds the per-session lock; user msg pending (not yet persisted)
  │
  ├─ Condense(ctx) ──────────────► the ONLY normal summarization call.
  │     window ≥ CondenseThreshold   Writes an anchored summary to the store.
  │
  ├─ Window(0)  ────────────────► [summary + recent] + pending user,
  │                                 bounded by WindowBudget ≤ usableWindow
  │
  ├─ runner.Run → adk.Runner.Run
  │     contextopt middleware runs every model call:
  │       trimBeforeLastSummary  (free; drops pre-summary history)
  │       prune / compress       (cheap; no LLM)
  │       IsOverflow? ───────────► FALSE, because the window already fits
  │                                 usableWindow with the SAME counter.
  │       → Summarizer NOT called  (no second bill)
  │
  └─ persistence goroutine: CommitAssistant → releases the lock
        the next BeginTurn for this session blocks here, serializing turns
```

If the conversation keeps growing within a single long run (e.g. large tool
outputs pushing past the window mid-turn), `contextopt` *may* summarize — but
that is genuine intra-run overflow, done **incrementally** by passing the
existing summary as `previousSummary`, not a duplicate of the cross-request work.

## Turn ownership and the persistence race (important)

After `runner.Run` succeeds, the **runner bridge owns the turn**: its persistence
goroutine calls `CommitAssistant` (or `Discard`) exactly once and releases the
session lock when done. The caller must **not** also `Discard` the turn on the
success path — a deferred `turn.Discard()` can race the async commit and drop the
pending user message, so the history silently loses every user turn.

This example therefore `Discard`s only on the early error paths (before the
bridge takes over) and lets the bridge own commit/discard afterwards. Turns stay
correctly serialized because the next `BeginTurn` blocks on the per-session lock
until persistence releases it.

## Files

- [`main.go`](./main.go) — the full runnable wiring (session + contextopt +
  runner bridge) with an instrumented `Summarizer` that proves the call count.

## See also

- [`components/memory/session`](../../session) — cross-request lifecycle and the
  "Avoiding double summarization cost" section.
- [`components/middleware/contextopt`](../../../middleware/contextopt) — the
  cheapest-first optimization pipeline.
- [`components/memory/runner`](../../runner) — the `adk.Runner` → session bridge.
