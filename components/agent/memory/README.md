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
