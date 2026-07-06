# sizecap — Size-cap document splitter

`sizecap` splits documents whose content exceeds a configurable chunk size
into smaller overlapping chunks. It implements `document.Transformer` and is
safe for UTF-8 content (splits on rune boundaries, never mid-character).

## Configuration

```go
type Config struct {
    ChunkSize    int // Maximum runes per chunk (default 1000)
    ChunkOverlap int // Overlap runes between chunks (default 200)
}
```

## Usage

```go
import "github.com/webcenter-fr/eino-ext/components/document/transformer/splitter/sizecap"

splitter, err := sizecap.NewSplitter(ctx, &sizecap.Config{
    ChunkSize:    1000,
    ChunkOverlap: 200,
})
chunks, err := splitter.Transform(ctx, docs)
```
