# memory-agent/file

A JSONL file-backed implementation of the
[`memoryagent.MemoryStore`](../) interface for local development and testing.

## How it works

Memories are stored as JSONL (one JSON object per line) in a single
`memories.jsonl` file. The store is loaded into memory on startup and rewritten
on delete/update operations.

- **Retrieval** uses simple substring matching against entry content (sorted by
  insertion order). The `TopK` retriever option is respected.
- **Delete / DeleteByFilter** rewrites the file from a temp copy for atomicity.
- **Filter** supports `category`, `source`, `session_id`, and arbitrary metadata
  key-value matches.

## Configuration

```go
import (
    memoryagent "github.com/webcenter-fr/eino-ext/components/memory/agent"
    storefile "github.com/webcenter-fr/eino-ext/components/memory/agent/file"
)

store, err := storefile.NewStore(storefile.Config{
    Dir: "/var/data/memory-agent",  // defaults to /tmp/eino/memory-agent
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

## Limitations

This store is intended for local development. It holds all entries in memory
and performs linear scans for retrieval. For production use, implement the
`MemoryStore` interface with a vector database backed by an eino retriever
and indexer.
