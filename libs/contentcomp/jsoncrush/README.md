# jsoncrush — Deterministic JSON array compression

`jsoncrush` deterministically compresses JSON arrays of objects by hoisting
common key/value pairs into a `_defaults` block, leaving only per-row
deviations. Includes an optional lossy stage for high-entropy columns via a
content-addressed `Store`.

## Design

- **Deterministic** — all output has sorted keys and stable byte representation.
  Unchanged input always produces identical output.
- **Lossless by default** — `Expand` reverses the crush to canonical JSON.
- **Idempotent** — already-crushed content passes through unchanged.
- **Cache-safe** — crushed form is only returned when it is actually smaller
  than the original input.

## Usage

```go
import "github.com/webcenter-fr/eino-ext/libs/contentcomp/jsoncrush"

// Lossless crush
crushed, refs, err := jsoncrush.Crush(ctx, content)
expanded, err := jsoncrush.Expand(crushed)

// With opt-in lossy stage
crushed, refs, err = jsoncrush.Crush(ctx, content, jsoncrush.WithStore(store))
expanded, err = jsoncrush.ExpandWithStore(ctx, crushed, store)
```

When a `Store` is configured, columns with near-unique values across all rows
are moved behind content-addressed handles (`Ref`), preserving the original data
while keeping the crushed output compact.

## Compressor adapter

```go
c := jsoncrush.NewCompressor()
out, changed, err := c.Compress(ctx, content)
// or with store:
c := jsoncrush.NewCompressor(jsoncrush.WithStore(store))
```

Implements `contentcomp.Compressor`.

## Related

- `libs/contentcomp` — the shared `Compressor`/`Store`/`Ref` contracts.
- `libs/contentcomp/shellout` — CLI/log output compaction.
