# OpenSearch indexer

`opensearch` implements `indexer.Indexer` backed by OpenSearch (via
`github.com/disaster37/opensearch/v4`), plus utilities for OpenSearch index
lifecycle management used by indexing graphs: field mapping merges,
source-hash lookups, bulk reconciliation (delete stale documents), and batch
deletes by source ID.

## Indexer

`Indexer` bulk-indexes `schema.Document` values into an OpenSearch index. It
optionally vectorizes document content with an `embedding.Embedder` and
stores the resulting vector, so it can back either a plain BM25 index or a
kNN/hybrid one (pair it with `components/retriever/opensearch`).

### Configuration

```go
type Config struct {
    URLs          []string // OpenSearch cluster URLs
    Username      string
    Password      string
    TLSSkipVerify bool

    Index     string // default index, overridable per call with indexer.WithIndex
    BatchSize int    // max documents per bulk request, default 100

    DocumentToFields DocumentToFields   // optional custom doc -> fields mapper
    Embedding        embedding.Embedder // optional, enables vectorization
    VectorField      string             // knn_vector field, default "vector"
    ContentField     string             // text field for content, default "content"
}
```

By default (no `DocumentToFields`), the document's `Content` is stored under
`ContentField` and every `MetaData` entry is copied as-is (internal eino keys
such as `_sub_indexes` are skipped). When `Embedding` is set, content is
embedded and the vector stored under `VectorField` — unless the document
already carries a vector via `schema.Document.WithDenseVector`, in which case
that vector is reused and no embedding call is made for it.

### Usage

```go
import (
    "github.com/cloudwego/eino/components/indexer"
    osindexer "github.com/webcenter-fr/eino-ext/components/indexer/opensearch"
)

idx, err := osindexer.NewIndexer(ctx, &osindexer.Config{
    URLs:      []string{"https://localhost:9200"},
    Index:     "my-index",
    Embedding: myEmbedder,
})

ids, err := idx.Store(ctx, docs, indexer.WithIndex("my-index"))
```

## Index lifecycle utilities

| Function | Description |
|---|---|
| `EnsureMappings` | Idempotently merges field properties into an existing index |
| `LookupSourceHash` | Returns the stored hash for a given source ID |
| `BulkLookupSourceHashes` | Scrolls all source ID → hash pairs |
| `DeleteBySourceID` / `DeleteBySourceIDs` | Deletes documents by source ID |
| `Reconcile` | Scrolls all source IDs and deletes those not in `seen` |

Field names for source ID and hash are customizable via `WithSourceIDField`
and `WithSourceHashField` (defaults: `"source_id"`, `"source_hash"`).

```go
import "github.com/webcenter-fr/eino-ext/components/indexer/opensearch"

hash, found, err := opensearch.LookupSourceHash(ctx, client, "my-index", sourceID)
deleted, err := opensearch.Reconcile(ctx, client, "my-index", seen, nil)
```
