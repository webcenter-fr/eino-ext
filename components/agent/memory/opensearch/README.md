# agent-memory/opensearch

An OpenSearch-backed implementation of the `MemoryStore` interface for
persistent, production-grade agent memory storage with support for BM25 text
search and optional kNN vector search.

## How it works

Internally composes `indexer/opensearch` and `retriever/opensearch` for the
`Store` and `Retrieve` operations, and uses the OpenSearch v4 Go client for
direct maintenance operations (`Delete`, `DeleteByFilter`, `List`, `Count`).

Documents are stored in an OpenSearch index with the following mapping:

| Field        | Type                    | Note                              |
|-------------|------------------------|-----------------------------------|
| `content`    | text                   | BM25 text search                  |
| `category`   | keyword                | fact / preference / learning / summary |
| `source`     | keyword                | user / assistant / observation / session |
| `session_id` | keyword                | Session scoping                   |
| `user_id`    | keyword                | User scoping                      |
| `created_at` | date                   |                                   |
| `updated_at` | date                   |                                   |
| `vector`     | knn_vector (384, innerproduct) | Only when Embedding is configured |

The index is created with `index.auto_expand_replicas: "0-2"` for automatic
replica management.

## Configuration

| Field           | Type                 | Required | Default                | Description                                 |
|----------------|----------------------|----------|------------------------|---------------------------------------------|
| `URLs`          | `[]string`           | Yes      |                        | OpenSearch cluster URLs                     |
| `Username`      | `string`             | No       |                        | Basic auth username                         |
| `Password`      | `string`             | No       |                        | Basic auth password                         |
| `TLSSkipVerify` | `bool`               | No       | `false`                | Skip TLS certificate verification           |
| `IndexName`     | `string`             | No       | `eino_agent_memory`    | OpenSearch index name                       |
| `Embedding`     | `embedding.Embedder` | No       |                        | Embedder for kNN vector search              |
| `VectorField`   | `string`             | No       | `vector`               | knn_vector field name                       |
| `ContentField`  | `string`             | No       | `content`              | Text field for BM25 match                   |
| `Hybrid`        | `bool`               | No       | `false`                | Combine kNN with BM25                       |
| `K`             | `int`                | No       |                        | Number of nearest neighbors for kNN         |
| `BatchSize`     | `int`                | No       | `100`                  | Max documents per bulk request              |
| `SearchPipeline`| `string`             | No       |                        | Optional search pipeline name               |

## Example

```go
import (
    "context"

    memoryagent "github.com/webcenter-fr/eino-ext/components/agent/memory"
    osstore "github.com/webcenter-fr/eino-ext/components/agent/memory/opensearch"
)

store, err := osstore.NewStore(context.Background(), &osstore.Config{
    URLs: []string{"https://opensearch.example.com:9200"},
})
if err != nil {
    return err
}

agent, err := memoryagent.NewMemoryAgent(ctx, memoryagent.Config{
    InnerAgent: myAgent,
    Store:      store,
    Model:      myModel,
})
```

With embedding and hybrid search:

```go
store, err := osstore.NewStore(ctx, &osstore.Config{
    URLs:      []string{"https://opensearch.example.com:9200"},
    Embedding: myEmbedder,
    Hybrid:    true,
})
```
