# OpenSearch Retriever Tool

Add a generic `tool.InvokableTool` that wraps the existing
`components/retriever/opensearch` retriever, eliminating duplication for users
who create multiple semantically-identical retriever-as-tool implementations
(pointed at different indices with different output formatting).

## Relationship to existing `components/tool/opensearch/`

This tool does **NOT** replace the existing log tool. They serve different query
paradigms and remain independent:

|  | Existing log tool | This retriever tool |
|---|---|---|
| **Query** | Structured: cluster, namespace, pod, container, time range, Lucene | Unstructured: natural-language → embedding → kNN |
| **Search** | Raw `querydsl` (term, range, query_string) | `retriever.Retriever` (BM25 + kNN hybrid via embedding) |
| **Output** | Raw `event.original` log lines | Formatted documents with metadata headers + content |
| **Streaming** | `tool.StreamableTool` (logs unbounded) | None — Top-K documents fit single response |
| **Indices** | `*` (cross-index) | Single configured index |
| **Use case** | Operational debugging: pod log retrieval | Knowledge grounding: semantic document search |

**Streaming decision**: intentionally omitted. The retriever returns 5–15
bounded, ranked documents. Streaming would be counterproductive — the LLM needs
all results to reason across them, and there is no progressive-consumption model
for partial ranked results.

## Package

`components/tool/opensearch_retriever/`

Uses the existing retriever at `github.com/webcenter-fr/eino-ext/components/retriever/opensearch`
(package name `opensearch`).

## Files

| File | Purpose |
|------|---------|
| `tool.go` | `Tool` struct, `Query` params, `Invoke`, `NewTool`, compile-time interface check |
| `output.go` | `HitFormatter` interface + `HeaderField`-based default implementation |
| `all_tools.go` | `NewAllTools` factory — creates one tool per index sharing a single cluster config |
| `check.go` | `Check()` probing connectivity per index via dummy search |
| `check_test.go` | Tests for checkup |
| `tool_test.go` | Table-driven tests with mocked OpenSearch backend |
| `README.md` | Usage docs |

## Types

### Config

```go
type Config struct {
    // ── Connection (passed through to retriever) ──
    URLs          []string `validate:"required,min=1" jsonschema:"..."`
    Username      string   `validate:"omitempty" jsonschema:"..."`
    Password      string   `validate:"omitempty" jsonschema:"..."`
    TLSSkipVerify bool     `validate:"omitempty" jsonschema:"..."`

    // ── Search (passed through to retriever) ──
    Index                string              `validate:"required" jsonschema:"..."`
    Embedder             embedding.Embedder  `validate:"-" jsonschema:"-"`
    VectorField          string              `validate:"omitempty" jsonschema:"..."`
    ContentField         string              `validate:"omitempty" jsonschema:"..."`    // default "content"
    Hybrid               bool                `validate:"omitempty" jsonschema:"..."`
    K                    int                 `validate:"omitempty" jsonschema:"..."`
    SearchPipeline       string              `validate:"omitempty" jsonschema:"..."`
    EnsureSearchPipeline bool                `validate:"omitempty" jsonschema:"..."`

    // ── Tool identity ──
    ToolName    string `validate:"required" jsonschema:"(required) name for the tool"`
    Description string `validate:"required" jsonschema:"(required) tool description for the LLM"`
    DefaultTopK int    `validate:"omitempty" jsonschema:"max results default, defaults to 5"`

    // ── Output formatting ──
    Formatter    HitFormatter  `validate:"-" jsonschema:"-"`
    HeaderFields []HeaderField `validate:"omitempty" jsonschema:"metadata fields rendered as header lines before content"`
}

type HeaderField struct {
    MetaKey string `validate:"required" jsonschema:"(required) key in doc.MetaData"` 
    Label   string `validate:"omitempty" jsonschema:"display label (e.g. 'Document source'), defaults to MetaKey"`
}
```

### HitFormatter

```go
// HitFormatter converts a single search result document into a formatted string.
// Provided for full output control; when nil, HeaderFields-based default is used.
type HitFormatter interface {
    FormatHit(doc *schema.Document) string
}
```

Default: iterates `HeaderFields`, checks `doc.MetaData[key]`, renders non-empty
values as `"Label: value\n"`, then appends `doc.Content`, separated by `"---\n"`.

### Query (inferred by utils.InferTool)

```go
type Query struct {
    Query string `json:"query" jsonschema:"(required) natural-language search query"`
    Limit int    `json:"limit,omitempty" jsonschema:"max results, defaults to configured DefaultTopK"`
}
```

### Tool

```go
type Tool struct {
    retriever retriever.Retriever
    formatter HitFormatter
    tool.InvokableTool
}

var _ tool.InvokableTool = (*Tool)(nil)
```

### Factory types

```go
// ClusterConfig is shared across all tools created by NewAllTools.
type ClusterConfig struct {
    URLs          []string
    Username      string
    Password      string
    TLSSkipVerify bool

    Embedder             embedding.Embedder
    VectorField          string
    ContentField         string
    Hybrid               bool
    K                    int
    SearchPipeline       string
    EnsureSearchPipeline bool
    Formatter            HitFormatter
}

// IndexConfig defines one tool per index.
type IndexConfig struct {
    Index     string
    ToolName  string
    Description string
    DefaultTopK int
    HeaderFields []HeaderField
}
```

## Constructors

1. **`NewTool(ctx context.Context, cfg *Config) (*Tool, error)`**
   - Defaults `ContentField` → `"content"`, `DefaultTopK` → `5`
   - `validate.Struct(cfg)`
   - Creates `retrieveropensearch.NewRetriever(ctx, ...)` — full pass-through
   - Builds formatter: `cfg.Formatter` if set, else default from `cfg.HeaderFields`
   - `utils.InferTool(cfg.ToolName, cfg.Description, tool.Invoke)`

2. **`NewAllTools(ctx context.Context, cluster ClusterConfig, indices []IndexConfig) ([]tool.InvokableTool, error)`**
   - Validates indices has at least 1 entry
   - Iterates, calls `NewTool` for each index with merged config
   - Returns aggregate slice
   - Errors on first failure

## Checkup

`Check(ctx context.Context, configs []Config) checkup.Results`

- One result per config, component = `"opensearch_retriever"`, instance = `cfg.ToolName`
- Issues a dummy search with a known-absent query string against the index
- Status `"ok"` if search returns (even with 0 hits), `"error"` if the client fails
- Timeout: 10s per config

## Tests

- `tool_test.go`: mock the OpenSearch search endpoint via `httptest.Server`
  - Test tool creation with validation errors
  - Test tool invocation (query → hits → formatted output)
  - Test default TopK handling
  - Test HeaderFields formatting
  - Test custom HitFormatter
  - Test OpenSearch client errors
- `check_test.go`: test checkup against mock server, test empty configs

## Checklist (pre-implementation review)

- [ ] `go build ./...`, `go vet ./...`, `go test ./...` pass
- [ ] `Config` has `validate`+`jsonschema` tags AND `NewTool` calls `validate.Struct`
- [ ] Compile-time check: `var _ tool.InvokableTool = (*Tool)(nil)`
- [ ] `NewTool` and `NewAllTools` accept `ctx` as first parameter
- [ ] Table-driven tests, no live OpenSearch dependency
- [ ] `check.go` + `check_test.go` using `checkup.Results`
- [ ] Errors wrapped with `emperror.dev/errors`
- [ ] No license banner
- [ ] Package comment `// Package opensearch_retriever ...`
- [ ] README with usage examples for both `NewTool` and `NewAllTools`

## Dependencies

- Imports `github.com/webcenter-fr/eino-ext/components/retriever/opensearch` (existing retriever)
- `github.com/cloudwego/eino/components/tool/utils` (InferTool)
- `github.com/cloudwego/eino/schema` (schema.Document)
- `github.com/cloudwego/eino/components/embedding` (embedding.Embedder)
- `github.com/cloudwego/eino/components/retriever` (retriever.Retriever)
- `github.com/webcenter-fr/eino-ext/libs/toolkit/validate`
- `github.com/webcenter-fr/eino-ext/libs/toolkit/checkup`
- No new external dependencies
