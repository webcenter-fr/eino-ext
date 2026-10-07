# memory-agent

An [eino adk](https://github.com/cloudwego/eino) `Agent` wrapper that adds
long-term memory capabilities: automatic extraction, retrieval, and maintenance
of facts, preferences, and learnings from conversations.

## How it works

`MemoryAgent` wraps an inner `adk.Agent` and intercepts its run loop:

1. **Retrieval** — Before each turn, relevant memories are fetched from the
   `MemoryStore` using the last user message as a query. Results are injected
   into the system prompt as context.
2. **Extraction** — After each assistant response, an LLM extracts structured
   memories (facts, preferences, learnings) from the user+assistant exchange and
   persists them to the store.
3. **Maintenance** — An optional background maintainer runs periodic compaction
   (deduplication via Jaccard similarity, optional LLM merging) and age-based
   cleanup.
4. **Session end** — On `EndSession`, session-scoped memories are compacted into
   summaries.

Memories are scoped by `user_id` (for multi-tenant isolation) and `session_id`
(for session-level lifecycle). Identity is resolved per-invocation from adk
session values, with static fallbacks.

### Memory categories and sources

| Category | Description |
|---|---|
| `fact` | Declarative statements about the user or world |
| `preference` | User likes, dislikes, preferences |
| `learning` | Inferred or discovered knowledge |
| `procedure` | Reusable operational know-how (resource, label selector, wrapper, command form) |
| `summary` | Compaction-generated session summaries |

| Source | Description |
|---|---|
| `user` | Derived from user messages |
| `assistant` | Derived from assistant responses |
| `observation` | External observations |
| `session` | Session compaction output |

## Configuration

```go
import (
    "github.com/cloudwego/eino/adk"

    memoryagent "github.com/webcenter-fr/eino-ext/components/memory/agent"
)

agent, err := memoryagent.NewMemoryAgent(ctx, memoryagent.Config{
    InnerAgent:          myAgent,          // required: inner adk.Agent
    Store:               myStore,          // required for retrieval + extraction
    Model:               myModel,          // required for extraction + LLM dedup
    UserID:              "user-123",       // static default, overridable per-invocation
    SessionID:           "session-abc",    // static default, overridable per-invocation
    AutoExtract:         true,             // defaults to true when Store+Model set
    MaxMemoriesPerRetrieve: 5,            // max memories injected per turn
    MaintenanceInterval: 1 * time.Hour,    // 0 disables background maintenance
    SystemPromptPrefix:  "",               // optional prefix between memory context and system prompt
})
```

### Run-trace extraction (opt-in)

By default the extractor only sees the concatenated assistant prose. Enabling
`Trace.Enabled` makes the agent collect the **whole run trace** — assistant
text, tool calls, tool results, sub-agent output and the terminal answer — and
extract reusable `procedure` memories from it (for example "kafka.sh runs on the
pod selected by label `cli=true`").

```go
agent, err := memoryagent.NewAgent(ctx, memoryagent.Config{
    InnerAgent: myAgent,
    Store:      myStore,
    Model:      myModel,
    Trace: memoryagent.TraceConfig{
        Enabled: true,
        // Capture the terminal answer from a tool argument.
        TerminalTools: map[string]string{
            "attempt_completion":   "result",
            "ask_followup_question": "question",
        },
        // Optional: drop noisy tools before rendering.
        StepFilter: func(s memoryagent.TraceStep) bool { return s.Name != "noisy_tool" },
        // Optional: custom secret masking (defaults to a best-effort redactor).
        Redact: nil,
    },
    ExtractTimeout: 30 * time.Second, // bounds the extraction LLM call (default 30s)
    AsyncExtract:   true,             // extract in a goroutine; EndSession waits for it
    RetrieveTopK:   5,                // passed to store.Retrieve when > 0
})
```

Rendering is bounded: each step is capped at `Trace.MaxStepChars` (default
1500) with head+tail truncation, and the whole trace at `Trace.MaxChars`
(default 12000). When trimming, terminal answers are kept first, then tool
calls, tool results and assistant text (newest first); terminal answers are
never dropped.

**Redaction caveat:** the default redactor is best-effort. It masks
`password`/`secret`/`token`/`api_key` key-values, `Authorization: Bearer`
headers, PEM blocks and `sasl.jaas.config` values. Review and extend the
patterns (via `Trace.Redact`) for your deployment before storing tool output
that may contain credentials.

### Retrieval and extraction control

| Field | Description |
|---|---|
| `ExtractTimeout` | Timeout for the post-run extraction LLM call (default 30s). Extraction runs on a context detached from the run, so approval-halt/cancel/timeout runs are still learned from. |
| `AsyncExtract` | Run extraction in a goroutine after the run closes (default false). `EndSession` waits for in-flight extraction. |
| `RetrieveTopK` | Passed to `store.Retrieve` as `retriever.WithTopK` when > 0. |
| `QueryMessageFilter` | Excludes messages (e.g. synthetic control messages) from the retrieval query. nil keeps all user messages. |
| `ShouldRetrieve` | Gates retrieval. nil means true. |
| `ShouldExtract` | Gates extraction. nil means true. |

Stored memories use a deterministic `sha256(category + content)` ID, so
re-learning an identical memory upserts instead of duplicating. `created_at`
and `updated_at` are refreshed on each (re)store.

### Per-invocation identity

Use `adk.AddSessionValue` to set user/session identity dynamically:

```go
adk.AddSessionValue(ctx, "memory_user_id", "user-123")
adk.AddSessionValue(ctx, "memory_session_id", "session-abc")
```

These take precedence over the static `Config` values.

### Programmatic identity updates

```go
agent.SetUserID("user-456")
agent.SetSessionID("session-xyz")
```

## MemoryStore interface

```go
type MemoryStore interface {
    Indexer
    Retriever
    Delete(ctx context.Context, id string) error
    DeleteByFilter(ctx context.Context, filter map[string]any) (deleted int, err error)
    List(ctx context.Context, offset, limit int) ([]*schema.Document, error)
    Count(ctx context.Context) (int, error)
}
```

A JSONL file-backed implementation is provided at `components/memory/agent/file/`.

## Maintenance

The `MemoryMaintainer` performs two operations on a configurable interval:

- **Compaction** — Groups memories by category, clusters by Jaccard text
  similarity, and merges similar entries. When a model is available, an LLM
  produces a consolidated entry instead of raw concatenation.
- **Cleanup** — Removes entries older than `MaxAge`.

`TriggerFullPass` can be called manually for on-demand maintenance.

```go
maintainer := memoryagent.NewMemoryMaintainer(memoryagent.MaintainerConfig{
    Store:                  store,
    Interval:               1 * time.Hour,
    MaxCompactionSimilarity: 0.8,
    MaxAge:                 30 * 24 * time.Hour,
    Model:                  model,
})
maintainer.Start(ctx)
defer maintainer.Stop()
```

## End-of-session

Call `EndSession` to compact session memories:

```go
if err := agent.EndSession(ctx); err != nil {
    return err
}
```

This lists all memories for the current session, groups similar entries, and
merges them — using the maintainer's LLM dedup if available, or a simple
keep-first-delete-rest fallback.
