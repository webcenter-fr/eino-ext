# contextopt — Context-window optimization middleware

`contextopt` ports kilocode's session-compaction strategies into the eino
ecosystem. It keeps long conversations under a model's context window by:

- **trimming** everything before the last anchored summary;
- **pruning** stale tool outputs beyond a protected recent window;
- **compacting** the summarizable head into an anchored Markdown summary (via an
  injectable LLM `Summarizer`) while preserving the most recent turns (the *tail*).

The core `Optimizer` is **pure** (no LLM dependency). LLM access only enters
through the optional `Summarizer`.

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
