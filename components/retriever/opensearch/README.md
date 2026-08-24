# OpenSearch retriever

`opensearch` implements `retriever.Retriever` backed by OpenSearch. It
supports pure BM25 (keyword), pure kNN (vector), and hybrid BM25+kNN search
with optional RRF pipeline-based score fusion.

## Configuration

```go
type Config struct {
    URLs           []string
    Username       string
    Password       string
    TLSSkipVerify  bool
    ResultParser   ResultParser // custom hit → document conversion
    SearchPipeline string       // optional pipeline name

    Embedding     embedding.Embedder // enables kNN vector search
    VectorField   string             // knn_vector field name
    ContentField  string             // text field for BM25 (defaults to "content")
    Hybrid        bool               // combine kNN with BM25
    K             int                // kNN neighbors (defaults to TopK)

    Index                  string // required, OpenSearch index to search
    EnsureSearchPipeline  bool   // auto-create SearchPipeline on startup
}
```

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

## Usage

### Pure BM25 (keyword) search

```go
import "github.com/webcenter-fr/eino-ext/components/retriever/opensearch"

retriever, _ := osretriever.NewRetriever(ctx, &osretriever.Config{
    URLs:  []string{"https://localhost:9200"},
    Index: "my-index",
})
docs, err := retriever.Retrieve(ctx, "search terms", retriever.WithTopK(10))
```

### Hybrid (kNN + BM25) with RRF pipeline

```go
import (
    "github.com/cloudwego/eino/components/embedding"
    osretriever "github.com/webcenter-fr/eino-ext/components/retriever/opensearch"
)

r, _ := osretriever.NewRetriever(ctx, &osretriever.Config{
    URLs:                 []string{"https://localhost:9200"},
    Embedding:            myEmbedder,
    VectorField:          "vector",
    ContentField:         "content",
    Hybrid:               true,
    K:                    10,
    SearchPipeline:       "rrf",
    Index:                 "my-index",
    EnsureSearchPipeline: true, // auto-create "rrf" if missing
})

docs, _ := r.Retrieve(ctx, "search terms", retriever.WithTopK(10))
```

### RRF pipeline provisioning

Use `EnsureSearchPipeline: true` in the config to have `NewRetriever`
idempotently create the pipeline during construction. Failures are not
fatal: the retriever still returns successfully and falls back to
un-fused hybrid scoring.

```go
r, _ := osretriever.NewRetriever(ctx, &osretriever.Config{
    // ...
    SearchPipeline:       "rrf",
    EnsureSearchPipeline: true,
})
```

For manual control, `EnsureRRFPipeline` is also available as a standalone
function:

```go
retriever, _ := osretriever.NewRetriever(ctx, cfg)

created, err := osretriever.EnsureRRFPipeline(ctx, retriever.Client(), "rrf")
if err != nil {
    fmt.Printf("RRF pipeline creation failed (cluster may not support it): %v\n", err)
}
```
