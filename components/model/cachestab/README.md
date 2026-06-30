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

## Why this wins — design rationale

### How prompt caching works

Provider prompt caches (Anthropic, OpenAI, Bedrock, …) work on a **longest
common prefix** of the serialized request. The provider hashes the request
**token by token, from the very first byte**, and can only reuse cached compute
up to the **first byte that differs** from a previous request. Everything after
the first divergence is a cache *miss* and must be recomputed (and re-billed at
the full, uncached rate).

Crucially, **tool definitions are serialized at the front of the prompt**,
before the system prompt and the message history:

```
┌─────────────────┬───────────────┬──────────────────────┐
│ tool definitions│ system prompt │ message history …    │
└─────────────────┴───────────────┴──────────────────────┘
  ^ cache prefix starts here
```

So even a **single byte** of instability in the tool block invalidates the
cache for the *entire* request that follows it — system prompt and the whole
conversation included.

### Where the instability comes from

The exact same set of tools can serialize to **different bytes** across requests
because tool definitions are usually built from Go maps, struct reflection, or
merged from several sources. None of those guarantee a stable order:

- **Tool order** — tools collected from a `map`, registered by plugins, or
  gathered concurrently come out in a non-deterministic order.
- **JSON Schema key order** — `properties`, `required`, `$defs`, and nested
  subschemas inherit Go map iteration order, which is *randomized per run*.

Two requests that are semantically identical (same tools, same params) thus
produce byte-different prefixes, and the cache silently misses every time:

```
request A:  {"tools":[{"name":"b"...},{"name":"a"...}]}
request B:  {"tools":[{"name":"a"...},{"name":"b"...}]}   ← differs at byte ~11
            └──────── cache prefix broken here, whole request recomputed ───────┘
```

### What `cachestab` does about it

`cachestab` makes the tool block **byte-deterministic** by imposing a total
order that depends only on content, never on construction:

1. **Sort tools by name** — stable, alphabetical, independent of how/when they
   were collected.
2. **Recursively sort each schema's keys** — `properties`, `required`, `$defs`,
   and every nested subschema, so identical schemas always serialize identically.

Because the transformation is semantics-preserving, the model sees the exact
same tools — but now the serialized prefix is **stable across runs, processes,
and machines**. The cache prefix survives, and the cache hit extends through the
system prompt and message history too.

### The concrete win

| Without `cachestab`                          | With `cachestab`                              |
| -------------------------------------------- | --------------------------------------------- |
| Tool block bytes vary per request            | Tool block bytes are deterministic            |
| First divergence near the prompt's start     | No spurious divergence in the tool block      |
| Cache misses cascade into the whole prompt   | Cache prefix extends past tools into history  |
| Full uncached token billing, higher latency  | Cached-token billing, lower TTFT              |

The payoff is largest for agentic / multi-turn workloads with many tools and
long histories, where a missed prefix means re-paying for thousands of tokens
on **every** turn. By stabilizing the *cheapest-to-fix, earliest* part of the
prompt, `cachestab` protects the cache for everything downstream of it.

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

## Runnable examples

See [`example_test.go`](./example_test.go) for self-contained, runnable samples:

- `ExampleNormalizeTools` — normalize a tool slice standalone.
- `ExampleNewToolCallingChatModel` — wrap any `model.ToolCallingChatModel` so
  `WithTools` receives normalized definitions.

Run them with:

```bash
go test ./components/model/cachestab/ -run Example -v
```

## Out of scope

Provider-specific cache hints — Anthropic `cache_control`, OpenAI
`prompt_cache_key` — are **not** handled here. They are fragile to provider API
changes and belong in a proxy, per the backport plan.
