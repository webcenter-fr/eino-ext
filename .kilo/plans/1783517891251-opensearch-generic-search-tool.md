# Generic OpenSearch Search Tool

## Goal

Transform the existing `OpensearchLogKubernetesTool` into a reusable, generic
OpenSearch search tool that permits searching any index with arbitrary queries,
optional date-range filtering, configurable output formatting, and PIT-based
scrolling. Provide it as both batch (`InvokableTool`) and streaming
(`StreamableTool`) modes.

## Context

The current tool at `components/tool/opensearch/opensearch_log_kubernetes.go`
is tightly coupled to Kubernetes pod log search (hardcoded fields:
`labels.cluster`, `kubernetes.namespace`, etc.). This plan replaces it with a
generic facility while keeping the original tool available as a
backward-compatible wrapper.

The new design mirrors the **factory pattern** already used by the
`opensearch` **retriever** (`components/retriever/opensearch/retriever.go`):
a `Config` struct holds connection parameters, behavioral defaults, and an
optional `ResultParser` callback for custom output formatting.

## Files to create / modify

| File | Action |
|---|---|
| `components/tool/opensearch/opensearch_search.go` | **Create** — generic search tool |
| `components/tool/opensearch/opensearch_log_kubernetes.go` | **Refactor** — thin wrapper using generic tool |
| `components/tool/opensearch/opensearch_test.go` | **Update** — tests for generic tool + wrapper |
| `components/tool/opensearch/check.go` | **Update** — add check for new tool |
| `components/tool/opensearch/check_test.go` | **Update** — adjust expected check count |
| `components/tool/opensearch/README.md` | **Update** — document new tool |
| `CONTRIBUTING.md` | **Update** — add PIT/ResultParser shared-library requirement |

## 1. `opensearch_search.go` — Generic search tool

### 1.1 Types

```go
// SearchResultParser converts a raw OpenSearch search hit (source fields plus
// metadata: _id, _index, _score, _version) into a formatted string.
// When nil, the hit is serialized as compact JSON.
type SearchResultParser func(ctx context.Context, hit map[string]any) (string, error)

// SearchConfig — factory-level configuration (mirrors retriever Config).
type SearchConfig struct {
    osclient.Config                             // URLs, Username, Password, TLSSkipVerify

    DefaultIndex string `validate:"required"`    // fallback index
    TimeField    string                           // default "@timestamp"
    DefaultSort  string                           // default "@timestamp:desc"
    MaxResults   int `min=1,max=10000`            // default 100
    ResultParser SearchResultParser               // optional, nil = compact JSON
}
```

```go
// SearchParams — per-invocation parameters (exposed to LLM).
type SearchParams struct {
    Indices     []string // override DefaultIndex
    QueryString string   // Lucene query string, "*" for all
    From        string   // date range lower bound (relative or absolute), omit for none
    To          string   // date range upper bound
    TimeField   string   // override config TimeField
    Sort        string   // "field:asc" or "field:desc"
    MaxResults  int      // override config MaxResults
}
```

### 1.2 PIT scrolling

- Open PIT via `POST /{indices}/_search/point_in_time?keep_alive=2m` (resty
  client, as in `pipeline.go`).
- Use `querydsl.NewSearchRequest().PointInTime(...)` and
  `SearchAfter(lastHit.Sort...)` for subsequent pages.
- Close PIT with `DELETE /_search/point_in_time {"pit_id":"..."}` via `defer`.
- Batch size: 500 per page.

### 1.3 Query building

```
If no from/to:
    if queryString empty or "*":   MatchAll
    else:                          QueryString(queryString)
If from or to:
    BoolQuery:
        Must: RangeQuery(TimeField).Gte(from).Lte(to)
        Must: QueryString(queryString) if present
```

### 1.4 Rules followed

- `validate.Struct` called in constructor AFTER defaults.
- `ctx context.Context` first parameter in `NewSearchTool`.
- Errors wrapped with `emperror.dev/errors`.
- Both `tool.InvokableTool` and `tool.StreamableTool` embedded (NO compile-time
  `var _` check — it causes `Info()` ambiguity with two embedded tools).
- Package-level `searchHitToMap` (copy of the retriever version — it is a
  standalone utility needed by the tool; the retriever already has its own
  copy).

### 1.5 Constructor

```go
func NewSearchTool(ctx context.Context, cfg *SearchConfig) (*SearchTool, error)
```

Threads `ctx` into `NewClient`. Uses `utils.InferTool` /
`utils.InferStreamTool` to wire the eino tool descriptors.

## 2. `opensearch_log_kubernetes.go` — backward-compatible wrapper

Refactor to use the generic `SearchTool` underneath. The tool retains its
existing Kubernetes-specific parameters (`Cluster`, `Namespace`, `PodName`,
`ContainerName`, `LuceneQuery`, `From`, `To`, `MaxLines`) and its old
description. Internally it instantiates a `SearchTool` (or receives one via
config dependency injection) and delegates to it.

**Option A** (simpler): The Kubernetes tool wraps `SearchTool`, translating
its params into the generic `SearchParams` and using a custom
`SearchResultParser` that extracts `event.original` as before.

**Option B**: Keep the original implementation as-is and document that users
should prefer `NewSearchTool` for new use cases. Keep `OpensearchLogKubernetesTool`
for backward compatibility only.

**Recommendation**: Option B — minimal risk. Add a deprecation comment on the
kubernetes tool pointing to the generic one.

## 3. Tests (`opensearch_test.go`)

- `TestSearchBuildQuery` — table-driven: various combinations of queryString,
  from/to, timeField, verifying the generated query structure.
- `TestParseSort` — edge cases: "field:asc", "field:desc", "field",
  "field:ASCENDING".
- `TestSearchHitToMap` — normal hit, nil score, nil version.
- `TestDefaultSearchResultParser` — valid hit produces valid JSON.
- `TestSearchParamsDefaults` — that optional fields fall back to config.
- **All tests are pure unit tests** (no live OpenSearch dependency).

## 4. Checkup (`check.go` / `check_test.go`)

- Add a `"opensearch_search"` component result.
- When no URLs: status `"error"`.
- When client created: status `"limited"` (requires search parameters for
  actual invocation).
- Update `check_test.go` to expect 3 results instead of 2.

## 5. README.md

Replace the current Kubernetes-specific documentation with:
- Generic `NewSearchTool` example.
- `SearchConfig` / `SearchParams` tables.
- `SearchResultParser` example (how to define a custom formatter).
- Note that `NewOpensearchLogKubernetesTool` is still available for legacy use.

## 6. CONTRIBUTING.md

Add a new section under **Components** or **Shared library patterns**:

```markdown
### OpenSearch shared library patterns

When implementing OpenSearch-backed tools:

- **Use PIT scrolling**: Always use Point-in-Time (`POST
  /_search/point_in_time`) with `search_after` for result pagination instead
  of the legacy Scroll API.  PIT provides a consistent snapshot view and is
  the recommended approach for deep pagination.

- **Provide a ResultParser callback**: Every OpenSearch tool that returns
  search results MUST accept an optional `ResultParser` function (type
  `func(ctx context.Context, hit map[string]any) (string, error)`) in its
  constructor config. The parser receives the full hit map (source fields +
  `_id`, `_index`, `_score`, `_version`) and returns a formatted string.
  When `ResultParser` is nil, the default formatter serializes each hit as
  compact JSON.

- **Propose both stream and regular modes**: Every OpenSearch tool MUST
  implement both `tool.InvokableTool` and `tool.StreamableTool`.

- **Keep tools generic**: Tool parameters should accept arbitrary query
  strings and indices. Project-specific wrappers (e.g. Kubernetes log search)
  should be built on top of the generic tool.
```

## 7. Risks and edge cases

- **PIT not supported**: Older OpenSearch versions lack PIT. The tool can
  fall back to a regular (non-PIT) search with `from`/`size` pagination,
  logged as a warning. This fallback is NOT implemented in v1 — PIT is
  required.
- **Large result sets**: Default `MaxResults=100`, capped at 10,000.
- **Interleaved writes**: PIT guarantees a consistent snapshot, so
  concurrent writes during scrolling are not visible.
- **Indices at call time**: If the LLM omits `indices`, the tool uses
  `DefaultIndex`. If `DefaultIndex` is empty, validation fails at
  construction time.
- **Date range on non-`@timestamp` fields**: Users can override `timeField`
  to scope the range to any date field. If `from`/`to` are both empty, no
  range filter is applied.

## 8. Task list for implementer

- [ ] Create `opensearch_search.go` with `SearchConfig`, `SearchParams`,
      `SearchTool`, `NewSearchTool`, PIT helpers, `Invoke`, `InvokeAsStream`,
      and all private helpers.
- [ ] Mark `opensearch_log_kubernetes.go` with a deprecation comment
      pointing to `NewSearchTool`.
- [ ] Add `searchHitToMap` to `opensearch_search.go` (copy from retriever).
- [ ] Write table-driven unit tests for query building, parsing, formatting.
- [ ] Update `check.go`: add `"opensearch_search"` entry.
- [ ] Update `check_test.go`: expect 3 results.
- [ ] Rewrite `README.md`: generic tool + legacy note.
- [ ] Update `CONTRIBUTING.md`: add PIT / ResultParser / dual-mode section.
- [ ] Run `go build ./... && go vet ./... && go test ./...`.
