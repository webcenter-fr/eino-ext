# Memory Agent Plan

## Goal

Build an eino `adk.Agent` component (`components/memory/agent`) that provides
long-term memory and automatic learning, inspired by Hermes agent architecture.
The agent wraps any existing agent and enriches it with:
- Persistent semantic memory (facts, preferences, learnings)
- Automatic extraction of learnings from each turn
- End-of-session memory compaction/aggregation
- Swappable `MemoryStore` backend (file, vector DB, etc.)

## Design Decisions

| Decision | Choice | Rationale |
|---|---|---|
| Storage interface | Composite `MemoryStore` (Indexer + Retriever + Delete + Compact) | Simpler API; user explicitly requested RAG-interface reuse |
| Session end signal | Explicit `EndSession(ctx)` called by supervisor | Supervisor orchestrates agents; needs a lifecycle hook |
| Package location | `components/memory/agent` | New agent component, separate from conversation memory |
| Agent model | Wraps an inner `adk.Agent` as a decorator/proxy | Reuses any existing agent; memory is transparent enrichment |
| Auto-learning | Post-turn extraction via LLM call | Like hermes, use a cheap model to extract facts after each exchange |
| Maintenance | Background goroutine with ticker | Scheduled compaction/aggregation, configurable interval |

## Architecture

```
┌─────────────────────────────────────────────┐
│                 Supervisor                    │
│  1. Creates MemoryAgent(innerAgent, store)   │
│  2. Uses agent as tool or in graph           │
│  3. Calls EndSession() when work is done     │
└──────────────┬──────────────────────────────┘
               │
┌──────────────▼──────────────────────────────┐
│              MemoryAgent                      │
│  ┌──────────────────────────────────────┐    │
│  │  Pre-turn: MemoryStore.Retrieve()    │    │
│  │  Inject context into system prompt   │    │
│  ├──────────────────────────────────────┤    │
│  │  Inner agent runs (adk.Runner.Run)   │    │
│  ├──────────────────────────────────────┤    │
│  │  Post-turn: Auto-extract learnings   │    │
│  │  MemoryStore.Store(learnings)        │    │
│  ├──────────────────────────────────────┤    │
│  │  EndSession(): Compact + Aggrégate   │    │
│  └──────────────────────────────────────┘    │
│  ┌──────────────────────────────────────┐    │
│  │  Background Maintainer (ticker)      │    │
│  │  - Compact similar memories          │    │
│  │  - Aggregate into higher insights    │    │
│  │  - Cleanup stale/outdated entries    │    │
│  └──────────────────────────────────────┘    │
└──────────────┬──────────────────────────────┘
               │
┌──────────────▼──────────────────────────────┐
│             MemoryStore (interface)           │
│  Store / Retrieve / Delete / Compact         │
│  ┌────────────┐  ┌──────────────────────┐    │
│  │  FileStore  │  │  VectorStore impl    │    │
│  │  (default)  │  │  (milvus, redis...)  │    │
│  └────────────┘  └──────────────────────┘    │
└──────────────────────────────────────────────┘
```

## Implementation Plan

### 1. Define `MemoryStore` interface

File: `components/memory/agent/store.go`

```go
// MemoryStore is the composite storage interface for long-term agent memory.
// It composes eino's Indexer and Retriever for RAG compatibility, plus
// maintenance operations.
type MemoryStore interface {
    indexer.Indexer   // Store(docs) -> ids
    retriever.Retriever // Retrieve(query) -> docs
    
    // Delete removes a document by ID.
    Delete(ctx context.Context, id string) error
    
    // DeleteByFilter removes documents matching a metadata filter.
    DeleteByFilter(ctx context.Context, filter map[string]any) (deleted int, err error)
    
    // List returns all documents (or a paginated subset).
    List(ctx context.Context, offset, limit int) ([]*schema.Document, error)
}
```

**Rationale**: Composes eino's existing Indexer + Retriever so any vector DB
implementation (Milvus, Redis, Elasticsearch) works out of the box. Adds
Delete/List for maintenance operations.

### 2. Memory entry schema

File: `components/memory/agent/types.go`

```go
type MemoryEntry struct {
    ID        string
    Content   string    // The memory content
    Category  string    // "fact", "preference", "learning", "summary"
    Source    string    // "user", "assistant", "observation"
    SessionID string    // Which session produced this
    CreatedAt time.Time
    UpdatedAt time.Time
    Metadata  map[string]any
}
```

Stored as `schema.Document` with these fields in `MetaData`.

### 3. Simple File-based MemoryStore

File: `components/memory/agent/file/store.go`

Default implementation using `MEMORY.md`/`USER.md` files (like Hermes).

Uses JSONL for structured storage with in-memory index for fast retrieval.

### 4. MemoryAgent construction

File: `components/memory/agent/agent.go`

```go
type MemoryAgent struct {
    inner       adk.Agent
    store       MemoryStore
    extractor   *MemoryExtractor    // LLM-based fact extractor
    maintainer  *MemoryMaintainer   // Background compactor
    llm         model.ChatModel     // For extraction/summarization
    
    sessionID   string
    mu          sync.Mutex
}

type Config struct {
    InnerAgent     adk.Agent
    Store          MemoryStore
    Model          model.ChatModel    // Model for extraction (cheap)
    
    // AutoExtract enables post-turn automatic learning extraction.
    AutoExtract    bool   // default: true
    
    // MaintenanceInterval is the background maintenance tick interval.
    // 0 or negative disables background maintenance.
    MaintenanceInterval time.Duration // default: 1h
    
    // MaxMemoriesPerRetrieve limits retrieved memories per prefetch.
    MaxMemoriesPerRetrieve int // default: 5
    
    // SystemPromptPrefix is prepended before inner agent system prompt.
    SystemPromptPrefix string
}
```

### 5. Pre-turn: Memory retrieval

Before each agent invocation, retrieve relevant memories:

1. Build query from the last user message
2. Call `MemoryStore.Retrieve(query, topK=N)`
3. Format memories as context block
4. Inject into system prompt (or prepend to messages)

Context format:
```
[Memory context - NOT new user input. Treat as authoritative reference.]
- fact: user prefers Go over Python for backend services
- fact: the project architecture uses DDD with hexagonal ports
```

### 6. Post-turn: Auto-learning extraction

After the assistant responds, extract learnings:

1. Prompt a cheap model with:
   ```
   Extract facts, preferences, and insights from this exchange.
   User: <user message>
   Assistant: <assistant response>
   
   Return JSON array of {content, category, confidence}.
   ```
2. Parse the JSON response
3. Call `MemoryStore.Store(docs)` for each high-confidence extraction
4. Deduplicate against existing memories (check metadata for similar content)

### 7. End-of-session processing

Triggered by `EndSession(ctx)`:

1. **Summarization**: Run an LLM call to build a session summary:
   - Take all turn exchanges as context
   - Prompt: "Summarize the key outcomes, decisions, and learnings from this session"
   - Store as `MemoryEntry{Category: "summary", Source: "session"}`

2. **Compaction**: Merge similar memories created during the session:
   - Group by category
   - Detect near-duplicates (embedding similarity or simple text overlap)
   - Replace duplicates with a merged/consolidated entry

3. **Background trigger**: Signal the maintainer to run a full pass

### 8. Background maintenance

File: `components/memory/agent/maintainer.go`

Runs on a ticker (default: 1 hour):

1. **Compaction phase**:
   - Retrieve all entries from store
   - Group by semantic similarity (embedding or text)
   - Merge near-duplicates into consolidated entries
   - Delete originals, store merged versions

2. **Aggregation phase**:
   - Group facts by category
   - For facts with >3 entries in same category, run LLM to build higher-level insight
   - Store insight, mark source facts as "aggregated"

3. **Cleanup phase**:
   - Remove entries older than configurable TTL with low confidence
   - Remove entries superseded by aggregations

### 9. Integration with existing eino-ext components

The `MemoryAgent` implements `adk.Agent`:

```go
func (a *MemoryAgent) Run(ctx context.Context, input *adk.AgentInput, opts ...adk.AgentRunOption) *adk.AsyncIterator[*adk.AgentEvent]
```

It wraps the inner agent's `Run`:
1. Pre-turn memory retrieval
2. Delegate to inner agent
3. Post-turn extraction (via the `runner.Run` bridge, hooking into the `Turn.CommitAssistant` path)

Or, alternatively, the agent can be used as a **tool** in a supervisor graph via `adk.NewAgentTool()`.

### 10. Session lifecycle with supervisor

The supervisor creates the MemoryAgent and manages its lifecycle:

```go
// Create
memAgent := memory.NewMemoryAgent(ctx, memory.Config{
    InnerAgent: chatAgent,
    Store:      memory.NewFileStore("/var/memory"),
    Model:      cheapLLM,
})

// Use as tool in graph
tool := adk.NewAgentTool(ctx, memAgent)

// ... after all work is done ...
memAgent.EndSession(ctx)  // Triggers compaction + aggregation
```

### 11. File structure

```
components/memory/agent/
├── store.go           # MemoryStore interface
├── types.go           # MemoryEntry, categories, errors
├── agent.go           # MemoryAgent implementation
├── agent_test.go      # Tests
├── extractor.go       # MemoryExtractor (LLM-based fact extraction)
├── extractor_test.go
├── maintainer.go      # MemoryMaintainer (background compaction)
├── maintainer_test.go
├── file/
│   ├── store.go       # File-based MemoryStore
│   └── store_test.go
└── README.md          # Usage documentation
```

### 12. Test plan

1. **FileStore tests**: Store/Retrieve/Delete/List integration tests
2. **MemoryExtractor tests**: Mock LLM responses, verify extraction logic
3. **MemoryAgent tests**: Full integration with mock inner agent + mock store
4. **Maintainer tests**: Compaction + aggregation + cleanup with controlled dataset
5. **EndSession tests**: Verify summarization and maintenance trigger

## Open questions (post-plan)

1. **Deduplication strategy**: Text overlap vs embedding similarity? Start with simple
   text overlap (Jaccard), upgrade to embedding if needed.
2. **Extraction prompt engineering**: Will need iteration; start with a simple
   JSON schema prompt inspired by hermes' learn_prompt.py.
3. **MemoryStore implementations**: File-based first. Vector DB implementations
   (Milvus, Redis, ES) reuse existing eino-ext indexers/retrievers directly.
4. **Concurrency**: The maintainer runs in background; needs proper locking
   around Store operations to avoid races with post-turn writes.

## Validation

1. `go build ./components/memory/agent/...` compiles
2. `go test ./components/memory/agent/...` passes
3. `go vet ./components/memory/agent/...` clean
4. Manual integration: supervisor creates MemoryAgent, runs turns, verifies
   memories persist, calls EndSession, verifies compaction.
