# memory-agent/file

A JSONL file-backed implementation of the
[`memoryagent.MemoryStore`](../) interface for local development and testing.

## How it works

Memories are stored as JSONL (one JSON object per line) in a single
`memories.jsonl` file. The store is loaded into memory on startup and rewritten
on delete/update operations.

- **Retrieval** scores entries by lexical term overlap with the query and
  returns them best-first (see [Retrieval semantics](#retrieval-semantics)).
  The `TopK` retriever option is respected.
- **Delete / DeleteByFilter** rewrites the file from a temp copy for atomicity.
- **Filter** supports `category`, `source`, `session_id`, and arbitrary metadata
  key-value matches.

## Retrieval semantics

`Retrieve` performs a **lexical** (not semantic) ranked search:

1. **Tokenization** — query and entry content are lowercased and split on every
   rune that is neither a letter nor a digit. Tokens shorter than 2 runes and
   stopwords (a small English/French list) are dropped. Digits are kept, so
   identifiers such as `ran37hpd2` and `eu-west-3` remain searchable
   (`eu`, `west`; the trailing `3` is dropped as a single rune).
2. **Scoring** — `score = matched + matched/distinctContentTerms`, where
   `matched` is the number of distinct query terms present in the entry. The
   coverage bonus is in `(0, 1]`, so an entry matching more query terms always
   outranks one matching fewer, while a short focused memory beats a long one
   with the same number of matches.
3. **Filtering** — entries scoring `0` are never returned. When `MinScore` is
   set, entries scoring below it are dropped as well.
4. **Ordering** — score descending, then `updated_at` descending, then
   `created_at` descending, then insertion order. Fully deterministic, so
   `TopK` (and the agent's `MaxMemoriesPerRetrieve`) keeps the *best* matches,
   not the oldest. Each returned document exposes its score under the `_score`
   metadata key, like the OpenSearch backend.
5. **Blank query** — an empty or whitespace-only query returns every entry in
   insertion order, bounded by `TopK` (list-like behavior).
6. **Degenerate query** — a query made only of stopwords, punctuation, or
   single-rune tokens returns **no** documents rather than everything.

> Matching is lexical: a paraphrase sharing no term with a memory is not found.
> That is expected for the local backend — use the OpenSearch backend with an
> embedder for semantic recall.

## Configuration

| Field      | Type       | Required | Default                   | Description                                                     |
|------------|------------|----------|---------------------------|-----------------------------------------------------------------|
| `Dir`      | `string`   | Yes      | `/tmp/eino/memory-agent`  | Directory holding `memories.jsonl`                              |
| `MinScore` | `*float64` | No       | `nil` (no threshold)      | Drops retrieved memories scoring below this value; `0` == `nil`  |

> `MinScore` mirrors `agent/memory/opensearch.Config.MinScore`. Set it to a
> small positive value (e.g. `2`) when memories matching a single common term
> are being injected. Negative values are rejected by `NewStore`.

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

Retrieval is lexical and in-process: paraphrases without shared terms are not
found, and scripts written without word separators (e.g. Chinese, Japanese)
tokenize poorly. The stopword list is a small English/French heuristic.
