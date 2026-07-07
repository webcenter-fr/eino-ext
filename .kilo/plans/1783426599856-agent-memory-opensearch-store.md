# Agent Memory OpenSearch Store

Add `components/agent/memory/opensearch/` implementing `memoryagent.MemoryStore` backed by OpenSearch.

## Background

- `components/agent/memory/store.go` defines `MemoryStore` interface: `indexer.Indexer` + `retriever.Retriever` + `Delete`, `DeleteByFilter`, `List`, `Count`
- `components/agent/memory/file/` implements it with JSONL (dev only)
- `components/memory/opensearch/` implements `memory.Memory` (conversation history) — different interface, different data model, but shares OS client patterns
- `components/indexer/opensearch/` and `components/retriever/opensearch/` are eino components that can be composed internally

## Design Decisions

1. **Compose eino components internally** — Store uses `indexer/opensearch.Indexer` for `Store()` and `retriever/opensearch.Retriever` for `Retrieve()`. Direct OS calls (using `disaster37/opensearch/v4`) for `Delete`, `DeleteByFilter`, `List`, `Count`.
2. **BM25 + kNN hybrid** — Full embedding/vector search support with `Embedding`, `VectorField`, `Hybrid`, `K` config options.
3. **Auto-expand replicas** — Index created with `"index.auto_expand_replicas": "0-2"`, matching `components/memory/opensearch`.

## Files to Create

### `components/agent/memory/opensearch/store.go`

**Package**: `package opensearch`

**Config struct** (union of needed fields from both sub-components):
```go
type Config struct {
    URLs          []string   `validate:"required,min=1"`
    Username      string     `validate:"omitempty"`
    Password      string     `validate:"omitempty"`
    TLSSkipVerify bool       `validate:"omitempty"`
    IndexName     string     `validate:"omitempty"`  // default: "eino_agent_memory"
    Embedding     embedding.Embedder `validate:"omitempty"`
    VectorField   string     `validate:"omitempty"`  // default: "vector"
    ContentField  string     `validate:"omitempty"`  // default: "content"
    Hybrid        bool       `validate:"omitempty"`
    K             int        `validate:"omitempty"`
    BatchSize     int        `validate:"omitempty,gte=1"`  // default: 100
    SearchPipeline string    `validate:"omitempty"`
}
```

**Store struct**:
```go
type Store struct {
    client      opensearchv4.Client  // v4 client for direct OS calls
    indexer     indexer.Indexer      // eino indexer/opensearch.Indexer
    retriever   retriever.Retriever  // eino retriever/opensearch.Retriever
    indexName   string
    clientConfig osclient.Config     // for passing indexName to indexer/retriever per-call opts
}
```

**NewStore constructor**:
1. Apply defaults (IndexName="eino_agent_memory", VectorField="vector", ContentField="content", BatchSize=100)
2. `validate.Struct(cfg)`
3. Create v4 client via `osclient.New(cfg, 30s)`
4. Check if index exists, create if not (with mapping + auto_expand_replicas)
5. Create internal `opensearchindexer.NewIndexer()` with `cfg.IndexName` as Index
6. Create internal `opensearchretriever.NewRetriever()`
7. Return `*Store`

**Index mapping** (created in `createIndex`):
| Field | Type | Note |
|-------|------|------|
| `content` | text | For BM25 |
| `category` | keyword | fact/preference/learning/summary |
| `source` | keyword | user/assistant/observation/session |
| `session_id` | keyword | Session scoping |
| `user_id` | keyword | User scoping |
| `created_at` | date | |
| `updated_at` | date | |
| `vector` | knn_vector (384 dim, innerproduct) | Only when Embedding configured |
| Dynamic | `false` | Prevent mapping explosion from metadata fields |

Settings:
```json
{
    "number_of_shards": 1,
    "index.auto_expand_replicas": "0-2"
}
```

The mapping creation must be conditional: if `Embedding` is not set, skip the `vector` field in the mapping to avoid OpenSearch rejecting unknown knn_vector dimensions.

**Methods**:
- `Store(ctx, docs, ...opts) ([]string, error)` → delegate to `s.indexer.Store(ctx, docs, withIndexOpt(s.indexName, opts)...)`
- `Retrieve(ctx, query, ...opts) ([]*schema.Document, error)` → delegate to `s.retriever.Retrieve(ctx, query, withIndexOpt(s.indexName, opts)...)`
- `Delete(ctx, id) error` → `s.client.Document().Delete(ctx, ...)`
- `DeleteByFilter(ctx, filter) (int, error)` → `s.client.Document().DeleteByQuery(ctx, ...)` with term filter
- `List(ctx, offset, limit) ([]*schema.Document, error)` → search with from/size
- `Count(ctx) (int, error)` → `s.client.Indices().Count(ctx, ...)`
- `GetType() string` → `"OpenSearch"` (required by indexer.Indexer/retriever.Retriever)
- `IsCallbacksEnabled() bool` → `true`

**Helpers**:
- `createIndex(ctx, client, indexName, cfg) error` — creates index with mapping
- `withIndexOpt(indexName, opts) []indexer.Option` / `[]retriever.Option` — ensures index is set

### `components/agent/memory/opensearch/store_test.go`

Table-driven tests covering:
1. Config validation (missing URLs, defaults)
2. `Delete` — documents with matching/non-matching IDs
3. `DeleteByFilter` — filter by category, source, session_id
4. `List`/`Count` — pagination and count accuracy
5. Result parsing from search hits

Tests should use mocks. The eino indexer and retriever require a real OS for actual Store/Retrieve tests; those can use a docker-compose test container.

### `components/agent/memory/opensearch/README.md`

Conventional README with constructor snippet and config table.

## What They Share vs Don't Share

**Share** (patterns, not code reuse):
- `osclient.New()` for client creation
- Index creation with `auto_expand_replicas`
- `validate.Struct(cfg)` validation pattern
- Error wrapping with `emperror.dev/errors`

**Don't share** (different Go interfaces):
- `memory.Memory` vs `MemoryStore` — incompatible operations
- Different index mappings and document structures
- Different search requirements (conversation lookup by ID vs semantic search)

## Tasks (ordered)

1. Create `components/agent/memory/opensearch/store.go` with Config, Store, NewStore, createIndex, Delete, DeleteByFilter, List, Count, GetType, IsCallbacksEnabled, and the internal indexer/retriever composition
2. Create `components/agent/memory/opensearch/store_test.go` with table-driven tests
3. Create `components/agent/memory/opensearch/README.md`
4. Run `go build ./components/...` and `go vet ./components/agent/memory/opensearch/...`
