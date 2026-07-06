# OpenSearch Memory Implementation

## Goal

Add a new OpenSearch-backed `memory.Memory` implementation under `components/memory/opensearch/` using the existing `github.com/disaster37/opensearch/v4` client. Must not modify the existing `components/memory/file/` implementation.

## Requirements

1. Use existing v4 OpenSearch client (already in `go.mod`)
2. Auto-create index if not exists with:
   - 1 shard, 1–2 replicas (configurable, default 1)
   - Proper mappings: `userId` (keyword), `conversationId` (keyword), `messages` (stored, not indexed), `updatedAt` (date)
3. Implement both `memory.Memory` and `memory.Conversation` interfaces
4. Follow existing code patterns from `components/memory/file/`

## Design Decisions

### Document model

Each conversation is a single OpenSearch document:
- Document `_id`: `{userId}:{conversationId}` (colon-separated)
- `_source`: `{userId, conversationId, messages: [], updatedAt}`

Why single-doc rather than one-doc-per-message: simpler Load/Save, consistent with the single-file-per-conversation model of `file` memory.

### Persistence strategy

- `Append` ⇒ `Save` ⇒ `Document().Index()` (upsert, replaces full document)
- This rewrites the entire messages array on every append (same trade-off as file memory rewriting the whole JSONL file). Acceptable because OpenSearch has no native append-to-array.

### Index mapping

```json
{
  "settings": {"number_of_shards": 1, "number_of_replicas": 1},
  "mappings": {
    "dynamic": false,
    "properties": {
      "userId": {"type": "keyword"},
      "conversationId": {"type": "keyword"},
      "messages": {"type": "object", "dynamic": false},
      "updatedAt": {"type": "date"}
    }
  }
}
```

- `dynamic: false` at index level prevents mapping explosion from `schema.Message.Extra` fields
- `messages.dynamic: false` stores message objects as-is without indexing sub-fields

### Constructor signature

`NewOpenSearchMemory(cfg Config) (memory.Memory, error)` — returns `error` unlike `NewFileMemory` which returns `memory.Memory` (nil on error). Justifiable because OpenSearch creation involves network calls that can fail.

### Config

```go
type Config struct {
    URLs            []string           // required, OpenSearch cluster URLs
    Username        string
    Password        string
    TLSSkipVerify   bool
    IndexName       string             // default "eino_memory"
    MaxWindowSize   int
    MaxWindowTokens int
    NumReplicas     int                // default 1, range 0-10
    TokenCounter    memory.TokenCounter
}
```

## Files

| File | Purpose |
|------|---------|
| `opensearch.go` | Config, `OpenSearchMemory`, `OpenSearchConversation`, index creation, all interface methods |
| `opensearch_test.go` | 14 unit tests for conversation behavior (GetWindow, GetMessages, summaries, token counting, doc serialization) |
| `README.md` | Usage docs |

## Known Limitations

1. **Save rewrites entire document** — Every `Append` rewrites the full messages array. For very large conversations (thousands of messages), this could become expensive. Mitigation: conversations are typically bounded by summarization + window trimming.
2. **Race condition on index creation** — If two `NewOpenSearchMemory` calls check `Exists` simultaneously, one may fail with "resource_already_exists_exception". Not handled — the caller gets the error.
3. **Only first URL used** — The v4 client config takes a single URL, matching `tools/opensearch/NewClient`. Multi-node failover isn't built in.
4. **`Append` swallows Save errors** — Required by the `Conversation` interface (`Append` returns void). Message is still appended to in-memory slice even if save fails.
5. **No default constructor** — Unlike `file.GetDefaultMemory()`, there's no `GetDefaultMemory()` because an OpenSearch connection is always required.

## Validation

- `go build ./...` — passes
- `go vet ./...` — passes
- `go test ./components/memory/...` — all 44 tests pass (including existing file/session/runner tests)
- 14 new tests cover: GetWindow (no summary, with summary, budget, multiple summaries), GetMessages, AppendSummary, LastSummaryIndex, CountTokens, document ID, JSON round-trip serialization

## Status

**Implemented** — all code written, tested, and verified. No further implementation tasks.
