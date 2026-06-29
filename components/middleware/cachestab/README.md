# cachestab — Tool-definition normalization for prompt-cache stability

`cachestab` deterministically normalizes **tool definitions** (not the message
history) to maximize prompt-cache hit rates. It is a port, in spirit, of
headroom's `tool_def_normalize`.

On every `WithTools` call it:

- sorts the tool list by name (alphabetical), and
- recursively sorts each tool's JSON Schema keys (`properties`, `required`,
  `$defs`, nested subschemas …).

The transformation is **semantics-preserving** (same tool names, same
parameters) and **modifies no message**, so it composes safely with any other
middleware.

## Usage

```go
base, _ := openai.NewChatModel(ctx, cfg) // any model.ToolCallingChatModel
m, err := cachestab.NewToolCallingChatModel(base)
if err != nil {
    return err
}

withTools, err := m.WithTools(tools) // tools arrive normalized & sorted
```

`NormalizeTools` is also exposed standalone:

```go
normalized, err := cachestab.NormalizeTools(tools)
```

## Out of scope

Provider-specific cache hints — Anthropic `cache_control`, OpenAI
`prompt_cache_key` — are **not** handled here. They are fragile to provider API
changes and belong in a proxy, per the backport plan.
