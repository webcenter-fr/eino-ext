# Memory Agent BM25 Retrieval Fix — Implementation Plan

## Summary

Fix the memory agent's BM25 retrieval which uses `operator: "and"` and never matches because queries are long multi-sentence natural-language prompts while stored memories are single short sentences. The fix spans three parts:

1. **Part 1 (core fix)**: Make BM25 match operator configurable, default to `"or"`, add `MinScore` and `MinimumShouldMatch` knobs.
2. **Part 2 (vector dimension)**: Make the hard-coded vector dimension (384) configurable so consumers with different embedders can use kNN.
3. **Part 3 (query capping)**: Add optional `MaxQueryChars` to bound the retrieval query length for better ranking with OR semantics.

---

## Files to Modify

| File | Change |
|------|--------|
| `components/retriever/opensearch/retriever.go` | Add `Operator`, `MinimumShouldMatch`, `MinScore` to `Config`; default `Operator` to `"or"`; update `buildSearchBody` |
| `components/retriever/opensearch/retriever_test.go` | **New file** — unit tests for `buildSearchBody` |
| `components/retriever/opensearch/README.md` | Document new fields and behavior change |
| `components/agent/memory/opensearch/store.go` | Add pass-through fields to `Config`; add `VectorDimension`; forward fields in retriever construction; use `VectorDimension` in `createIndex` |
| `components/agent/memory/opensearch/store_test.go` | Add validation tests for new fields; update `applyDefaults` |
| `components/agent/memory/opensearch/README.md` | Document new fields |
| `components/agent/memory/agent.go` | Add `MaxQueryChars` to `Config` and `Agent`; update `buildQuery` |
| `components/agent/memory/agent_test.go` | Add `buildQuery` truncation tests |

---

## Part 1: Core BM25 Fix

### 1.1 `components/retriever/opensearch/retriever.go`

#### 1.1.1 Add fields to `Config` struct

Insert after the `K` field (currently line 73) and before the `Index` field (line 77):

```go
// Operator is the match operator used by the pure-BM25 query ("or" or "and").
// Defaults to "or". "and" requires every query term to be present, which is
// pathological for multi-sentence natural-language queries.
Operator string `validate:"omitempty,oneof=or and" jsonschema:"description=BM25 match operator: or (default) or and"`

// MinimumShouldMatch is passed through to the BM25 match query when non-empty
// (e.g. "2<70%"). Unset by default.
MinimumShouldMatch string `validate:"omitempty" jsonschema:"description=Optional minimum_should_match for the BM25 match query"`

// MinScore, when > 0, is sent as the search body's min_score so weakly
// matching hits are dropped instead of returned as low-relevance noise.
MinScore float64 `validate:"omitempty,gte=0" jsonschema:"description=Optional min_score threshold for search results"`
```

#### 1.1.2 Add default in `NewRetriever`

In `NewRetriever`, after the `ContentField` default (currently lines 101-103) and **before** `validate.Struct(config)` (line 104), add:

```go
if config.Operator == "" {
    config.Operator = "or"
}
```

This must be before `validate.Struct` so the `oneof=or and` tag passes.

#### 1.1.3 Update `buildSearchBody` — pure-BM25 branch

Replace the pure-BM25 `queryPart` construction (currently lines 201-209):

**Before:**
```go
if r.config.Embedding == nil {
    queryPart = map[string]any{
        "match": map[string]any{
            r.config.ContentField: map[string]any{
                "query":    query,
                "operator": "and",
            },
        },
    }
}
```

**After:**
```go
if r.config.Embedding == nil {
    matchBody := map[string]any{
        "query":    query,
        "operator": r.config.Operator,
    }
    if r.config.MinimumShouldMatch != "" {
        matchBody["minimum_should_match"] = r.config.MinimumShouldMatch
    }
    queryPart = map[string]any{
        "match": map[string]any{
            r.config.ContentField: matchBody,
        },
    }
}
```

#### 1.1.4 Update `buildSearchBody` — add `min_score`

After the `body` map is assembled (after the `search_pipeline` conditional, before `return body`), add:

```go
if r.config.MinScore > 0 {
    body["min_score"] = r.config.MinScore
}
```

The full `body` assembly block becomes:

```go
body := map[string]any{
    "query": queryPart,
    "size":  topK,
}
if r.config.SearchPipeline != "" {
    body["search_pipeline"] = r.config.SearchPipeline
}
if r.config.MinScore > 0 {
    body["min_score"] = r.config.MinScore
}
return body
```

#### 1.1.5 Leave hybrid branch unchanged

The kNN/hybrid branch (lines 210-237) is NOT modified. It already uses default OR semantics (no explicit operator on the BM25 match sub-query).

---

### 1.2 `components/agent/memory/opensearch/store.go`

#### 1.2.1 Add pass-through fields to `Config`

Insert after the `SearchPipeline` field (currently line 41) and before the closing `}` of `Config` (line 42):

```go
// Operator is the BM25 match operator forwarded to the retriever ("or" or "and").
// Defaults to "or". See retriever/opensearch.Config.Operator.
Operator string `validate:"omitempty,oneof=or and" jsonschema:"description=BM25 match operator: or (default) or and"`

// MinimumShouldMatch is forwarded to the retriever's BM25 match query.
// See retriever/opensearch.Config.MinimumShouldMatch.
MinimumShouldMatch string `validate:"omitempty" jsonschema:"description=Optional minimum_should_match for the BM25 match query"`

// MinScore is forwarded to the retriever as the search body's min_score.
// See retriever/opensearch.Config.MinScore.
MinScore float64 `validate:"omitempty,gte=0" jsonschema:"description=Optional min_score threshold for search results"`
```

#### 1.2.2 Forward fields in retriever config construction

In `NewStore`, at the `retrieveropensearch.NewRetriever` call (currently lines 109-121), add the three new fields:

```go
ret, err := retrieveropensearch.NewRetriever(ctx, &retrieveropensearch.Config{
    Index:              cfg.IndexName,
    URLs:               cfg.URLs,
    Username:           cfg.Username,
    Password:           cfg.Password,
    TLSSkipVerify:      cfg.TLSSkipVerify,
    SearchPipeline:     cfg.SearchPipeline,
    Embedding:          cfg.Embedding,
    VectorField:        cfg.VectorField,
    ContentField:       cfg.ContentField,
    Hybrid:             cfg.Hybrid,
    K:                  cfg.K,
    Operator:           cfg.Operator,
    MinimumShouldMatch: cfg.MinimumShouldMatch,
    MinScore:           cfg.MinScore,
})
```

No defaults are set in the store for these fields — they pass through as-is. The retriever's `NewRetriever` handles the `Operator` default (`"or"`).

---

### 1.3 `components/retriever/opensearch/README.md`

Add a new section after the "Configuration" struct block (after the closing ` ``` ` of the config block, before "## Usage"):

```markdown
### BM25 match tuning

The pure-BM25 (no `Embedding`) search path supports three tuning knobs:

| Field                | Type     | Default | Description                                                      |
|---------------------|----------|---------|------------------------------------------------------------------|
| `Operator`           | `string` | `"or"`  | BM25 match operator: `"or"` or `"and"`                           |
| `MinimumShouldMatch` | `string` | (unset) | Optional `minimum_should_match` for the BM25 match query (e.g. `"2<70%"`) |
| `MinScore`           | `float64`| `0`     | When > 0, drops search hits with `_score` below this threshold   |

> **Behavior change**: Prior to this version, the pure-BM25 path hard-coded
> `operator: "and"`. The default is now `"or"`, which is the standard RAG
> behavior and matches OpenSearch's own default. Consumers that require strict
> AND semantics must set `Operator: "and"` explicitly.
```

---

### 1.4 `components/agent/memory/opensearch/README.md`

Add three rows to the configuration table (after the `SearchPipeline` row):

```markdown
| `Operator`           | `string`  | No       | (forwarded)           | BM25 match operator: `"or"` or `"and"`       |
| `MinimumShouldMatch` | `string`  | No       | (forwarded)           | Optional `minimum_should_match` for BM25     |
| `MinScore`           | `float64` | No       | `0`                   | min_score threshold for search results       |
```

Add a note after the table:

```markdown
> **Memory retrieval is BM25-only** unless an `Embedding` is supplied. The
> `Operator` defaults to `"or"` (forwarded to the retriever). To reduce noise
> from the broader OR recall, set `MinScore` to a small positive value (e.g.
> `0.5`). For strict AND matching, set `Operator: "and"`.
```

---

## Part 2: Configurable Vector Dimension

### 2.1 `components/agent/memory/opensearch/store.go`

#### 2.1.1 Add `VectorDimension` field to `Config`

Insert after the `K` field (currently line 39) and before `BatchSize` (line 40):

```go
// VectorDimension is the dimension of the knn_vector field created when the
// index is auto-created. Defaults to 384 for backward compatibility. Changing
// this on an existing index requires deleting and recreating the index.
VectorDimension int `validate:"omitempty,gte=1" jsonschema:"description=knn_vector dimension,default=384"`
```

#### 2.1.2 Add default in `NewStore`

In `NewStore`, after the `BatchSize` default (currently lines 73-75) and before `validate.Struct(cfg)` (line 76), add:

```go
if cfg.VectorDimension == 0 {
    cfg.VectorDimension = 384
}
```

#### 2.1.3 Update `createIndex`

Replace the hard-coded `384` on line 178 with `cfg.VectorDimension`:

**Before (lines 175-185):**
```go
if cfg.Embedding != nil {
    properties[cfg.VectorField] = map[string]any{
        "type":      "knn_vector",
        "dimension": 384,
        "method": map[string]any{
            "name":       "hnsw",
            "engine":     "nmslib",
            "space_type": "innerproduct",
        },
    }
}
```

**After:**
```go
if cfg.Embedding != nil {
    properties[cfg.VectorField] = map[string]any{
        "type":      "knn_vector",
        "dimension": cfg.VectorDimension,
        "method": map[string]any{
            "name":       "hnsw",
            "engine":     "nmslib",
            "space_type": "innerproduct",
        },
    }
}
```

### 2.2 `components/agent/memory/opensearch/README.md`

Add a row to the configuration table (after the `K` row):

```markdown
| `VectorDimension`   | `int`     | No       | `384`                 | knn_vector dimension (only used when Embedding is set and index is auto-created) |
```

Add a note:

```markdown
> **Changing `VectorDimension` on an existing index requires deleting and
> recreating the index**, because `ensureIndex` only creates the mapping when
> the index does not exist. An existing index with a different vector dimension
> will not be updated automatically.
```

---

## Part 3: Query Capping

### 3.1 `components/agent/memory/agent.go`

#### 3.1.1 Add `MaxQueryChars` field to `Config`

Insert after `MaxMemoriesPerRetrieve` (currently line 39) and before `SystemPromptPrefix` (line 40):

```go
// MaxQueryChars, when > 0, truncates the retrieval query to at most this many
// characters. With OR semantics, very long queries can dilute BM25 ranking;
// capping keeps the query focused on the most recent user intent.
MaxQueryChars int `validate:"omitempty,gte=0" jsonschema:"description=Max characters for the retrieval query, 0 disables"`
```

#### 3.1.2 Add `maxQueryChars` field to `Agent` struct

Add after `maxMemoriesPerRetrieve` (currently line 57):

```go
maxQueryChars int
```

#### 3.1.3 Set in `NewAgent` return literal

In the `return &Agent{...}` literal (lines 82-92), add after `maxMemoriesPerRetrieve: cfg.MaxMemoriesPerRetrieve,`:

```go
maxQueryChars: cfg.MaxQueryChars,
```

#### 3.1.4 Update `buildQuery` to respect `MaxQueryChars`

Modify `buildQuery` (lines 190-203). After the `strings.Join` on line 202, add truncation before the return:

```go
func (a *Agent) buildQuery(messages []*schema.Message) string {
    const maxUserMessages = 2
    var userContents []string
    for i := len(messages) - 1; i >= 0 && len(userContents) < maxUserMessages; i-- {
        if messages[i].Role == schema.User && messages[i].Content != "" {
            userContents = append(userContents, messages[i].Content)
        }
    }
    // Reverse to chronological order.
    for i, j := 0, len(userContents)-1; i < j; i, j = i+1, j-1 {
        userContents[i], userContents[j] = userContents[j], userContents[i]
    }
    result := strings.Join(userContents, "\n")
    if a.maxQueryChars > 0 && len(result) > a.maxQueryChars {
        result = result[:a.maxQueryChars]
    }
    return result
}
```

**Edge case note**: Truncating mid-byte in a multi-byte UTF-8 character could produce invalid UTF-8. This is acceptable because queries are English natural language (ASCII). If stricter handling is desired, use `string([]rune(result)[:a.maxQueryChars])` but this is not required for the current use case.

---

## Test Specifications

### T1: `components/retriever/opensearch/retriever_test.go` (NEW FILE)

All tests are unit tests — `buildSearchBody` returns a `map[string]any`, no live OpenSearch cluster needed. Construct `Retriever` structs directly (bypass `NewRetriever`).

Package: `package opensearch`

Imports: `"testing"`, `"github.com/stretchr/testify/assert"`, `"github.com/stretchr/testify/require"`

#### T1.1 `TestBuildSearchBody_DefaultOperatorIsOr`

- **Purpose**: Regression test for the bug — confirms the default operator is `"or"`.
- **Setup**: `Retriever{config: Config{ContentField: "content", Operator: "or"}}` (simulating what `NewRetriever` would set).
- **Action**: `r.buildSearchBody("long multi sentence query", 10, nil)`
- **Assertions**:
  - `body["query"].(map[string]any)["match"].(map[string]any)["content"].(map[string]any)["operator"]` equals `"or"`
  - `body["query"].(map[string]any)["match"].(map[string]any)["content"].(map[string]any)["query"]` equals `"long multi sentence query"`
  - `body["size"]` equals `10`

#### T1.2 `TestBuildSearchBody_ExplicitAndOperator`

- **Purpose**: Confirm `Operator: "and"` is honored.
- **Setup**: `Retriever{config: Config{ContentField: "content", Operator: "and"}}`
- **Action**: `r.buildSearchBody("test query", 5, nil)`
- **Assertions**: `operator` equals `"and"`

#### T1.3 `TestBuildSearchBody_MinimumShouldMatch`

- **Purpose**: `minimum_should_match` is present only when configured.
- **Sub-test A**: `Config{..., MinimumShouldMatch: "2<70%"}` → assert `matchBody["minimum_should_match"]` equals `"2<70%"`
- **Sub-test B**: `Config{..., MinimumShouldMatch: ""}` → assert `minimum_should_match` key is absent from the match body

#### T1.4 `TestBuildSearchBody_MinScore`

- **Purpose**: `min_score` is set only when `> 0`.
- **Sub-test A**: `Config{..., MinScore: 0.5}` → assert `body["min_score"]` equals `0.5`
- **Sub-test B**: `Config{..., MinScore: 0}` → assert `body["min_score"]` key is absent

#### T1.5 `TestBuildSearchBody_HybridUnchanged`

- **Purpose**: Guard the blast radius — hybrid branch is unchanged.
- **Setup**: `Retriever{config: Config{Embedding: mockEmbedder, VectorField: "vector", ContentField: "content", Hybrid: true, K: 5}}`
- **Action**: `r.buildSearchBody("test", 10, [][]float64{{0.1, 0.2}})`
- **Assertions**:
  - `body["query"]["bool"]["should"]` is a slice of length 2
  - First element has `"knn"` key
  - Second element: `body["query"]["bool"]["should"][1]["match"]["content"]` equals `"test"` (no operator key — default OR)
  - `body["size"]` equals `10`

**Mock embedder**: Use a simple stub that satisfies `embedding.Embedder`:

```go
type stubEmbedder struct{}

func (s stubEmbedder) EmbedStrings(ctx context.Context, texts []string) ([][]float64, error) {
    return nil, nil
}
```

#### T1.6 `TestBuildSearchBody_SearchPipeline`

- **Purpose**: Regression — `search_pipeline` still works.
- **Setup**: `Config{ContentField: "content", Operator: "or", SearchPipeline: "rrf"}`
- **Assertions**: `body["search_pipeline"]` equals `"rrf"`

#### T1.7 `TestBuildSearchBody_AllFieldsCombined`

- **Purpose**: All new fields coexist correctly.
- **Setup**: `Config{ContentField: "content", Operator: "or", SearchPipeline: "rrf", MinScore: 0.5, MinimumShouldMatch: "1"}`
- **Assertions**: All four are present in the body with correct values.

---

### T2: `components/agent/memory/opensearch/store_test.go` (ADDITIONS)

#### T2.1 Update `applyDefaults` helper

Add `VectorDimension` defaulting:

```go
func applyDefaults(cfg *Config) {
    if cfg.IndexName == "" {
        cfg.IndexName = "eino_agent_memory"
    }
    if cfg.VectorField == "" {
        cfg.VectorField = "vector"
    }
    if cfg.ContentField == "" {
        cfg.ContentField = "content"
    }
    if cfg.BatchSize == 0 {
        cfg.BatchSize = 100
    }
    if cfg.VectorDimension == 0 {
        cfg.VectorDimension = 384
    }
}
```

Note: `Operator` is NOT defaulted here — it passes through to the retriever which handles the default.

#### T2.2 `TestConfig_OperatorFields`

- **Purpose**: New fields pass validation.
- **Setup**: `Config{URLs: []string{"http://localhost:9200"}, Operator: "or", MinimumShouldMatch: "2<70%", MinScore: 0.5}`
- **Action**: `applyDefaults(cfg)` then `validate.Struct(cfg)`
- **Assertions**: No error.

#### T2.3 `TestConfig_OperatorInvalid`

- **Purpose**: Invalid operator rejected.
- **Setup**: `Config{URLs: []string{"http://localhost:9200"}, Operator: "invalid"}`
- **Action**: `applyDefaults(cfg)` then `validate.Struct(cfg)`
- **Assertions**: Error contains `"operator"` and `"one of"`.

#### T2.4 `TestConfig_MinScoreNegative`

- **Purpose**: Negative MinScore rejected.
- **Setup**: `Config{URLs: []string{"http://localhost:9200"}, MinScore: -1}`
- **Action**: `applyDefaults(cfg)` then `validate.Struct(cfg)`
- **Assertions**: Error.

#### T2.5 `TestConfig_VectorDimensionDefault`

- **Purpose**: VectorDimension defaults to 384.
- **Setup**: `Config{URLs: []string{"http://localhost:9200"}}` (VectorDimension left 0)
- **Action**: `applyDefaults(cfg)`
- **Assertions**: `cfg.VectorDimension` equals `384`.

#### T2.6 `TestConfig_VectorDimensionNegative`

- **Purpose**: Negative VectorDimension rejected.
- **Setup**: `Config{URLs: []string{"http://localhost:9200"}, VectorDimension: -1}`
- **Action**: `applyDefaults(cfg)` then `validate.Struct(cfg)`
- **Assertions**: Error.

---

### T3: `components/agent/memory/agent_test.go` (ADDITIONS)

#### T3.1 `TestBuildQuery_MaxQueryChars_Truncates`

- **Purpose**: Query is truncated when `MaxQueryChars` is set.
- **Setup**: `Agent{maxQueryChars: 10}`
- **Action**: `agent.buildQuery([]*schema.Message{schema.UserMessage("this is a very long query that exceeds the limit")})`
- **Assertions**: Result equals `"this is a "` (first 10 chars).

#### T3.2 `TestBuildQuery_MaxQueryChars_Zero_NoTruncation`

- **Purpose**: `MaxQueryChars: 0` disables truncation.
- **Setup**: `Agent{maxQueryChars: 0}`
- **Action**: `agent.buildQuery([]*schema.Message{schema.UserMessage("hello world")})`
- **Assertions**: Result equals `"hello world"`.

#### T3.3 `TestBuildQuery_MaxQueryChars_ShorterThanLimit`

- **Purpose**: Query shorter than limit is not truncated.
- **Setup**: `Agent{maxQueryChars: 100}`
- **Action**: `agent.buildQuery([]*schema.Message{schema.UserMessage("short")})`
- **Assertions**: Result equals `"short"`.

#### T3.4 `TestBuildQuery_MaxQueryChars_TwoMessages`

- **Purpose**: Two joined messages are truncated together.
- **Setup**: `Agent{maxQueryChars: 15}`
- **Action**: `agent.buildQuery([]*schema.Message{schema.UserMessage("hello"), schema.UserMessage("world")})`
- **Assertions**: Result equals `"hello\nworld"` (13 chars, under limit — no truncation). Or with a smaller limit: `maxQueryChars: 5` → `"hello"` (first 5 chars of the joined string).

#### T3.5 `TestNewAgent_MaxQueryChars`

- **Purpose**: `MaxQueryChars` is stored correctly.
- **Setup**: `Config{InnerAgent: &mockAgent{name: "test"}, MaxQueryChars: 512}`
- **Action**: `NewAgent(ctx, cfg)`
- **Assertions**: `agent.maxQueryChars` equals `512`.

---

## Edge Cases and Error Handling

| Scenario | Behavior |
|----------|----------|
| `Operator` is empty string | Defaulted to `"or"` in `NewRetriever` before validation |
| `Operator` is `"invalid"` | Validation rejects with `oneof` error |
| `MinScore` is `0` | `min_score` key is absent from search body (OpenSearch default: no filtering) |
| `MinScore` is negative | Validation rejects with `gte` error |
| `MinimumShouldMatch` is empty | Key is absent from match body |
| `MinimumShouldMatch` is invalid syntax | Passed through as-is; OpenSearch returns error at query time |
| `VectorDimension` is `0` | Defaulted to `384` in `NewStore` before validation |
| `VectorDimension` is negative | Validation rejects with `gte` error |
| `MaxQueryChars` is `0` | No truncation applied |
| `MaxQueryChars` is negative | Validation rejects with `gte` error |
| `MaxQueryChars` > query length | No truncation, full query returned |
| Hybrid branch (Embedding != nil) | Unchanged — no operator/min_score added to hybrid queries |
| `buildQuery` with no user messages | Returns empty string (existing behavior, unchanged) |
| `buildQuery` with empty user message content | Skipped (existing behavior, unchanged) |

---

## Validation

After implementation, run:

```bash
go build ./...
go vet ./...
go test ./components/retriever/opensearch/...
go test ./components/agent/memory/opensearch/...
go test ./components/agent/memory/...
gofmt -l components/retriever/opensearch/ components/agent/memory/opensearch/ components/agent/memory/agent.go
```

---

## Risks

1. **Default `Operator` change (`and` → `or`)** affects any pure-BM25 consumer of `retriever/opensearch`. In the known consumer (rancher-doc-chat-api-k8s), only the memory store uses the pure-BM25 path. External consumers relying on AND must set `Operator: "and"` explicitly — call this out in release notes.

2. **Recall/precision trade-off**: OR always matches something. `MinScore` is the mitigation; it is off by default (`0`), so a deployment that sees irrelevant memory injections should enable it rather than reverting to AND.

3. **Part 2 requires a reindex**: `ensureIndex` only creates the mapping when the index does not exist, so an existing index with a different vector dimension will not be updated automatically.

---

## Rollout

1. Land all changes in eino-ext; tag/commit.
2. Consumer: `go get github.com/webcenter-fr/eino-ext@latest`, rebuild, redeploy. No consumer config change is required — the fixed default (`Operator: "or"`) takes effect automatically.
3. Optionally set `memoryAgent.opensearch.minScore` once the knob is plumbed through the consumer config.
