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
}
```

## Usage

### Pure BM25 (keyword) search

```go
import "github.com/webcenter-fr/eino-ext/components/retriever/opensearch"

retriever, _ := osretriever.NewRetriever(ctx, &osretriever.Config{
    URLs: []string{"https://localhost:9200"},
})
docs, err := retriever.Retrieve(ctx, "search terms",
    retriever.WithIndex("my-index"),
    retriever.WithTopK(10),
)
```

### Hybrid (kNN + BM25) with RRF pipeline

```go
import (
    "github.com/cloudwego/eino/components/embedding"
    osretriever "github.com/webcenter-fr/eino-ext/components/retriever/opensearch"
)

r, _ := osretriever.NewRetriever(ctx, &osretriever.Config{
    URLs:          []string{"https://localhost:9200"},
    Embedding:     myEmbedder,
    VectorField:   "vector",
    ContentField:  "content",
    Hybrid:        true,
    K:             10,
    SearchPipeline: "rrf",
})

docs, _ := r.Retrieve(ctx, "search terms",
    retriever.WithIndex("my-index"),
    retriever.WithTopK(10),
)
```

### RRF pipeline provisioning

`EnsureRRFPipeline` idempotently creates an RRF search pipeline on the cluster:

```go
retriever, _ := osretriever.NewRetriever(ctx, cfg)

created, err := osretriever.EnsureRRFPipeline(ctx, retriever.Client(), "rrf")
if err != nil {
    log.Warnf("RRF pipeline creation failed (cluster may not support it): %v", err)
}
```
