# prune — Document content pruner

`prune` removes documents whose trimmed content is below a configurable minimum
length (in runes). It implements `document.Transformer`.

## Configuration

```go
type Config struct {
    MinContentLength int // Minimum runes in trimmed content to keep a document (default 1)
}
```

## Usage

```go
import "github.com/webcenter-fr/eino-ext/components/document/transformer/prune"

pruner, err := prune.NewPruner(ctx, &prune.Config{
    MinContentLength: 10,
})
kept, err := pruner.Transform(ctx, docs)
```
