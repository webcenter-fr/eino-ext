# shellout — CLI/log/diff output compaction

`shellout` applies deterministic, declarative pattern-based transforms to
compact noisy terminal output: carriage-return progress redraws, percentage
progress bars, blank line runs, and repeated identical lines.

## Design

- **Deterministic** — every transform is a pure function. Unmatched content
  passes through byte-identically.
- **Reversible** — when a `Store` is configured, the original is preserved
  behind a content-addressed handle.
- **Fast-path** — a cheap `mayCompress` check skips expensive pattern matching
  when the input is likely incompressible.

## Default patterns

| Pattern | Effect |
|---|---|
| `strip-carriage-progress` | Keeps only the last line segment after `\r`. |
| `drop-progress-bars` | Removes lines matching download/fetch/progress patterns. |
| `collapse-blank-runs` | Collapses 2+ consecutive blank lines to 1. |
| `collapse-repeated-lines` | Collapses 3+ identical consecutive lines to a summary. |

## Usage

```go
import "github.com/webcenter-fr/eino-ext/libs/contentcomp/shellout"

// Default patterns
out, ref, err := shellout.Compress(ctx, content)

// With store for reversibility
out, ref, err = shellout.Compress(ctx, content, shellout.WithStore(store))

// Custom patterns
out, ref, err = shellout.Compress(ctx, content,
    shellout.WithPatterns(myPatterns...),
    shellout.WithMinGain(128),
)
```

## Compressor adapter

```go
c := shellout.NewCompressor()
out, changed, err := c.Compress(ctx, content)
```

Implements `contentcomp.Compressor`.

## Configuration options

- `WithStore(store)` — preserve the original behind a `Ref`.
- `WithPatterns(patterns...)` — replace the default pattern table.
- `WithMinGain(bytes)` — minimum byte reduction to return compressed output
  (default 64).

## Related

- `libs/contentcomp` — the shared `Compressor`/`Store`/`Ref` contracts.
- `libs/contentcomp/jsoncrush` — JSON array compression.
