# contextopt — Context-window optimization middleware

`contextopt` ports kilocode's session-compaction strategies into the eino
ecosystem. It keeps long conversations under a model's context window by:

- **trimming** everything before the last anchored summary;
- **pruning** stale tool outputs beyond a protected recent window;
- **compacting** the summarizable head into an anchored Markdown summary (via an
  injectable LLM `Summarizer`) while preserving the most recent turns (the *tail*).

The core `Optimizer` is **pure** (no LLM dependency). LLM access only enters
through the optional `Summarizer`.

## Why this wins — design rationale

### The problem: context grows faster than it pays off

In an agentic loop, every turn appends new messages — user input, assistant
reasoning, and (often large) tool outputs — to a history that is **re-sent in
full on every request**. That history grows monotonically, which causes three
compounding costs:

- **Hard failures** — once the history exceeds the model's context window, the
  request is rejected outright. The agent simply stops working mid-task.
- **Linear token billing** — you pay for the *entire* history on *every* turn.
  A 100-turn session re-bills the early turns ~100 times.
- **Latency & quality decay** — larger prompts mean higher time-to-first-token,
  and relevant recent context gets diluted by stale, low-value tool dumps.

Naively truncating the oldest messages loses information the agent still needs;
naively summarizing on every turn is expensive and destroys the cacheable
prefix. The art is removing the *least valuable* tokens while preserving both
**meaning** and **cache stability**.

### The strategy: a layered, cheapest-first pipeline

`contextopt` attacks the history in increasing order of cost and information
loss, so the expensive, lossy operations only run when the cheap ones are not
enough (`optimizer.go:509`):

```
1. trim before last summary   (free)        ← drop history already captured by an anchor
2. content compression        (lossless)    ← jsoncrush/shellout crush tool outputs, no info lost
3. prune stale tool outputs   (low loss)    ← truncate/offload OLD tool dumps, protect recent window
4. summarize on overflow      (lossy, $)    ← only if still over budget; collapse head, keep tail verbatim
```

Each layer is gated:

- **Trim** is free — it discards messages already represented by an anchored
  summary, so no information is actually lost.
- **Compress** is lossless or reversible — a JSON tool output is crushed and a
  shell dump compacted *before* anything is destroyed.
- **Prune** is bounded — it only touches tool outputs **outside** a protected
  recent window (`PruneProtectTokens`), only when eligible tokens exceed
  `PruneMinimum`, and with a `Backend` it is **reversible** (the original is
  offloaded, not deleted; recover via `RestorePruned`).
- **Summarize** is the last resort — it runs **only** on real overflow and
  **only** if a `Summarizer` is configured, and even then it preserves the most
  recent turns (the *tail*) verbatim so live working context is never blurred.

### Why each design choice concretely pays off

| Choice | Why it wins |
| --- | --- |
| Pure core `Optimizer`, LLM only via `Summarizer` | Deterministic, unit-testable, zero LLM cost on the common (non-overflow) path. |
| Idempotence markers (`__eino_ext_contextopt_pruned`, `…_compressed`) | `Optimize` runs every turn but only does O(new tool outputs) work — no re-truncating, no re-compressing. |
| Protected recent window + tail preservation | The agent's *active* reasoning is never pruned or summarized away, so task quality holds. |
| Cheapest-first ordering | Most turns are fixed by free/lossless layers; the costly summarizer call is avoided until genuinely necessary. |
| Reversible prune (`Backend`) | Frees tokens now while keeping originals recoverable, so pruning is safe even for data you might need later. |
| Append-only `VerbositySteer` / non-mutating `VolatileCheck` | Steering and diagnostics keep the cacheable prefix byte-stable, so they don't sabotage prompt-cache hits (complements `cachestab`). |

### The concrete win

| Without `contextopt`                          | With `contextopt`                                 |
| --------------------------------------------- | ------------------------------------------------- |
| History grows until the request is rejected   | History is kept under the usable window           |
| Full history re-billed every turn             | Stale/low-value tokens removed before re-billing  |
| Long sessions degrade and eventually break    | Long, multi-turn sessions stay viable             |
| Summarization (if any) is all-or-nothing      | Layered loss: free → lossless → bounded → lossy   |

The payoff scales with session length and tool-output volume: the longer the
agent runs, the more turns benefit from removing tokens that were already
summarized, already compressible, or too old to matter — while the recent,
decision-relevant context is protected by design.

## Two surfaces

Both wrap the same `*Optimizer`:

- `Middleware` — an `adk.ChatModelAgentMiddleware` that rewrites
  `state.Messages` in `BeforeModelRewriteState`.
- `ChatModel` — a `model.BaseChatModel` decorator that optimizes the input
  before each `Generate`/`Stream` call.

## Usage

### ADK middleware

```go
mw, err := contextopt.NewMiddleware(&contextopt.Config{
    ContextLimit:     128_000,
    PruneToolOutputs: true,
    Summarizer:       contextopt.NewModelSummarizer(myModel),
})
// register mw as an adk.ChatModelAgentMiddleware on your ChatModelAgent
```

### Model decorator (portable, outside ADK)

```go
cm, err := contextopt.NewChatModel(baseModel, &contextopt.Config{
    ContextLimit: 128_000,
    Summarizer:   contextopt.NewModelSummarizer(baseModel),
})
out, err := cm.Generate(ctx, messages) // input is optimized before delegation
```

When `baseModel` implements `model.ToolCallingChatModel`, `NewChatModel` returns
a `*ToolCallingChatModel` whose `WithTools` re-wraps the bound model and
preserves optimization. Use `NewToolCallingChatModel` for a statically-typed
tool-calling decorator. Plain base models never advertise tool support.

## Configuration

| Field | Default | Description |
| --- | --- | --- |
| `ContextLimit` | — | Total model context window (tokens). |
| `MaxInputTokens` | — | If > 0, takes precedence over `ContextLimit-ReservedTokens`. |
| `ReservedTokens` | `20_000` | Buffer reserved for model output. |
| `TailTurns` | `2` | Recent turns preserved verbatim (`<= 0` disables tail truncation). |
| `PreserveRecentTokens` | `clamp(usable*0.25, 2_000, 8_000)` | Token budget for the tail. |
| `PruneToolOutputs` | `false` | Enable pruning of stale tool outputs. |
| `PruneProtectTokens` | `40_000` | Protected recent window (no pruning inside it). |
| `PruneMinimum` | `20_000` | Minimum eligible tokens before pruning applies. |
| `ToolOutputMaxChars` | `2_000` | Max characters kept when truncating a tool output. |
| `ProtectedTools` | — | Tool names whose outputs are never pruned (e.g. `["skill"]`). |
| `TokenCounter` | `memory.DefaultTokenCounter` | Token estimator. |
| `Summarizer` | `nil` | LLM-backed compaction; `nil` => trim/prune only, no error on overflow. |
| `Backend` | `nil` | `contentcomp.Store` enabling **reversible** prune (offload original instead of destructive truncation). |
| `ContentCompressors` | — | Deterministic per-message compressors (e.g. `jsoncrush`, `shellout`) applied to tool outputs **before** any truncation. |
| `VolatileCheck` / `VolatileObserver` | `false` / `nil` | Warn-only detection of volatile tokens (timestamps, UUIDs, `*_id`) in the cached prefix. Non-mutating. |
| `VerbositySteer` | `""` | Append-only concision instruction at the **end** of the system prompt (cache-safe). Disabled by default. |

## Reversible prune & content compression

Without a `Backend`, pruning truncates stale tool outputs destructively. Set a
`contentcomp.Store` as `Backend` to make pruning **reversible**: the original is
offloaded to the store and the message keeps a content-addressed handle
(`Extra["__eino_ext_contextopt_pruned_ref"]`). Recover it via
`Optimizer.RestorePruned`.

`ContentCompressors` run on tool outputs **before** any truncation, so a JSON
tool output is crushed (lossless) and a shell output compacted before the
optimizer ever considers a hard truncation:

```go
store := contentcomp.NewMemoryStore()
cfg := &contextopt.Config{
    PruneToolOutputs:   true,
    Backend:            store, // reversible prune
    ContentCompressors: []contentcomp.Compressor{
        jsoncrush.NewCompressor(),
        shellout.NewCompressor(),
    },
}
```

## Diagnostics & steering (warn-only)

- `VolatileCheck` + `VolatileObserver` report ISO-8601 timestamps, UUIDs and
  `*_id` fields found in the cacheable prefix (system + tools + first messages).
  **No bytes are modified.**
- `VerbositySteer` appends a concision instruction to the **end** of the first
  system message (append-only; the prefix before it is unchanged, so the cache
  is preserved). Idempotent via `Extra["__eino_ext_contextopt_verbosity_steer"]`.

## Notes

- **Token counting** uses `memory.DefaultTokenCounter` (~4 chars/token) by
  default. Inject a precise counter (tiktoken/provider) via `Config.TokenCounter`
  for accurate overflow detection.
- **Idempotence**: pruning marks truncated tool messages with
  `Extra["__eino_ext_contextopt_pruned"]`, so `Optimize` can run on every turn
  without re-truncating.
- **Markers are shared** with the `memory` module (`memory.IsSummary`,
  `memory.NewSummaryMessage`, `memory.SummaryMarkerKey`) for consistency.
- The model decorator only optimizes **input**; streamed output is untouched.
