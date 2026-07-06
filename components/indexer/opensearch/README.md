# OpenSearch indexer helpers

`opensearch` provides utilities for OpenSearch index lifecycle management
in indexing graphs: field mapping merges, source-hash lookups, bulk
reconciliation (delete stale documents), and batch deletes by source ID.

## Functions

| Function | Description |
|---|---|
| `EnsureMappings` | Idempotently merges field properties into an existing index |
| `LookupSourceHash` | Returns the stored hash for a given source ID |
| `BulkLookupSourceHashes` | Scrolls all source ID → hash pairs |
| `DeleteBySourceID` / `DeleteBySourceIDs` | Deletes documents by source ID |
| `Reconcile` | Scrolls all source IDs and deletes those not in `seen` |

## Options

Field names for source ID and hash are customizable via `WithSourceIDField`
and `WithSourceHashField` (defaults: `"source_id"`, `"source_hash"`).

## Usage

```go
import "github.com/webcenter-fr/eino-ext/components/indexer/opensearch"

hash, found, err := opensearch.LookupSourceHash(ctx, client, "my-index", sourceID)
deleted, err := opensearch.Reconcile(ctx, client, "my-index", seen, nil)
```
