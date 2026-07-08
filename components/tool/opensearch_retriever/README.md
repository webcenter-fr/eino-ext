# OpenSearch Retriever Tool

An eino `tool.InvokableTool` that wraps the existing OpenSearch retriever,
enabling semantic document search via natural-language queries as an invokable
tool. Eliminates duplication for users who create multiple
semantically-identical retriever-as-tool implementations pointed at different
indices with different output formatting.

This tool complements the existing [OpenSearch tool](../opensearch/) — the log
tool is for operational debugging (structured pod log retrieval with streaming),
while this retriever tool is for knowledge grounding (semantic document search
via embeddings).

## Design

- **Underlying retriever** — uses
  `github.com/webcenter-fr/eino-ext/components/retriever/opensearch`
  for BM25 + kNN hybrid search.
- **Non-streaming** — Top-K documents are bounded and the LLM needs all
  results to reason across them, so streaming is intentionally omitted.
- **Output formatting** — configurable via `HitFormatter` interface (custom)
  or `HeaderFields` (default metadata headers + content).
- **Multi-index** — `NewAllTools` factory creates one tool per index sharing
  a single cluster configuration.

## Configuration

### Single tool

```go
import (
    "github.com/cloudwego/eino/components/embedding"
    "github.com/webcenter-fr/eino-ext/components/tool/opensearch_retriever"
)

cfg := &opensearch_retriever.Config{
    URLs:          []string{"https://opensearch.example.com:9200"},
    Username:      "admin",
    Password:      os.Getenv("OPENSEARCH_PASSWORD"),
    Index:         "knowledge_base",
    Embedder:      myEmbedder,
    VectorField:   "content_vector",
    Hybrid:        true,
    ToolName:      "search_knowledge_base",
    Description:   "Search the knowledge base for relevant documents",
    HeaderFields: []opensearch_retriever.HeaderField{
        {MetaKey: "_id", Label: "Document ID"},
        {MetaKey: "_score", Label: "Relevance"},
    },
}

tool, err := opensearch_retriever.NewTool(ctx, cfg)
```

### Multiple tools sharing a cluster

```go
cluster := opensearch_retriever.ClusterConfig{
    URLs:        []string{"https://opensearch.example.com:9200"},
    Embedder:    myEmbedder,
    VectorField: "content_vector",
    Hybrid:      true,
}

indices := []opensearch_retriever.IndexConfig{
    {
        Index:        "docs",
        ToolName:     "search_docs",
        Description:  "Search documentation",
        HeaderFields: []opensearch_retriever.HeaderField{{MetaKey: "title"}},
    },
    {
        Index:        "wiki",
        ToolName:     "search_wiki",
        Description:  "Search the internal wiki",
    },
}

tools, err := opensearch_retriever.NewAllTools(ctx, cluster, indices)
```

## Parameters

```json
{
    "query": "how to configure OpenSearch authentication",
    "limit": 5
}
```

| Field | Description | Default |
|---|---|---|
| `query` | Natural-language search query | (required) |
| `limit` | Maximum results to return | Configured `DefaultTopK` (defaults to 5) |

## Output Formatting

### Default formatter with HeaderFields

Each document is rendered as:

```
title: Authentication Guide
url: /docs/auth

Content of the document...
```

Fields with empty values are omitted. The `Label` defaults to `MetaKey` when
not set.

### Custom HitFormatter

```go
type myFormatter struct{}

func (f *myFormatter) FormatHit(doc *schema.Document) string {
    return fmt.Sprintf("Source: %s\n\n%s",
        doc.MetaData["url"], doc.Content)
}

cfg.Formatter = &myFormatter{}
```

## Usage

```go
result, err := tool.InvokableRun(ctx, `{
    "query": "OpenSearch authentication setup",
    "limit": 3
}`)
```

## Checkup

```go
results := opensearch_retriever.Check(ctx, []opensearch_retriever.Config{
    {URLs: []string{"https://os.example.com"}, Index: "docs",
        ToolName: "docs_search", Description: "doc search"},
})
fmt.Println(results.JSON("  "))
```
