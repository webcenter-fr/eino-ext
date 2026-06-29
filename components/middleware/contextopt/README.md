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
