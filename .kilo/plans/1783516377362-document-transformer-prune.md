# Document Transformer: `prune`

Create `components/document/transformer/prune/` — a `document.Transformer` that removes documents whose content is empty (or below a configurable minimum length).

## Motivation

The consuming chat application currently wires this via an inline `compose.Lambda`:

```go
compose.InvokableLambda(func(ctx context.Context, input []*schema.Document) ([]*schema.Document, error) {
    output = make([]*schema.Document, 0, len(input))
    for _, doc := range input {
        if strings.TrimSpace(doc.Content) != "" {
            output = append(output, doc)
        }
    }
    return output, nil
})
```

This logic is generic enough to be a reusable `document.Transformer` component, following the existing `sizecap` splitter pattern.

## Design

### Package

`components/document/transformer/prune/`

### Config

```go
type Config struct {
    // MinContentLength is the minimum number of runes in trimmed content.
    // Documents whose trimmed content is shorter than this are pruned.
    // Default: 1 (removes documents with effectively empty content).
    MinContentLength int `validate:"omitempty,gte=1" jsonschema:"description=Minimum runes in trimmed content to keep a document,default=1"`
}
```

### Constructor

```go
func NewPruner(ctx context.Context, config *Config) (document.Transformer, error)
```

- Accepts `nil` config (uses defaults).
- Applies default `MinContentLength = 1` when `<= 0`.
- Calls `validate.Struct(config)` after defaults.

### Transform behavior

- Returns documents whose `utf8.RuneCountInString(strings.TrimSpace(doc.Content)) >= MinContentLength`.
- Preserves all metadata on kept documents (no mutation).
- Returns empty slice for empty/nil input.
- Nil documents in the input slice are skipped (not an error).

No checkup (`check.go`) — pure in-process transformer with no external dependencies (same as `sizecap`).

## Files to create

| File | Purpose |
|---|---|
| `prune.go` | `Config`, `NewPruner`, `Transform`, `GetType`, compile-time check `var _ document.Transformer = (*Pruner)(nil)` |
| `prune_test.go` | Table-driven tests: empty input, nil config, single doc kept, single doc pruned, mixed, whitespace-only, UTF-8, below/at/above threshold |
| `README.md` | Description, config, usage snippet |

## Implementation notes

- Type name: `Pruner` (unexported struct), `typ = "Pruner"`.
- Reuse pattern from `sizecap`: defaults applied before validation, `emperror.dev/errors` not needed since there are no external calls that can fail (but `validate.Struct` already wraps errors).
- `Transform` does not mutate input documents — it builds a new slice of kept document pointers. The original document objects are not modified, so metadata is implicitly preserved.

## Validation

```bash
cd components/document/transformer/prune
go build ./...
go vet ./...
go test ./...
```
