# Plan: Use `auto_expand_replicas` instead of user-configurable `NumReplicas`

## Goal

Replace the user-facing `NumReplicas` config field with OpenSearch's built-in
`index.auto_expand_replicas: "0-2"` dynamic setting. OpenSearch automatically
expands replicas within this range based on the number of available data nodes,
so the user never needs to think about replica counts.

## Files to change

### 1. `components/memory/opensearch/opensearch.go`

- **Remove `NumReplicas` from `Config`** (line 28).
- **Remove replicas-defaulting logic** in `NewOpenSearchMemory` (lines 105–108).
- **Hardcode `auto_expand_replicas`** in `createIndex`:
  - Drop the `numReplicas int` parameter from `createIndex` signature (line 144).
  - Replace `"number_of_replicas": numReplicas` with `"index.auto_expand_replicas": "0-2"` in the settings body (line 148).
- **Update call site**: `createIndex(ctx, client, cfg.IndexName, replicas)` → `createIndex(ctx, client, cfg.IndexName)` (line 129).

The dot-notation key `"index.auto_expand_replicas"` is used because
`auto_expand_replicas` lives under the `index` settings namespace; it keeps
the surrounding flat `"number_of_shards": 1` key unchanged for consistency.

### 2. `components/memory/opensearch/README.md`

- Remove the `NumReplicas` line from the configuration snippet (line 27).
- Update the "Index creation" bullet to say `auto_expand_replicas: "0-2"` instead of mentioning a default replica count.

## Validation

```bash
go build ./components/memory/opensearch/...
go vet ./components/memory/opensearch/...
go test ./components/memory/opensearch/...
```

No test references `NumReplicas`, so no test changes are needed.
