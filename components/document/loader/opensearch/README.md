# OpenSearch document loader

`opensearch` implements `document.Loader` backed by an OpenSearch index.
Source URIs encode the target index and an optional query string.

## Configuration

```go
type Config struct {
    URLs          []string
    Username      string
    Password      string
    TLSSkipVerify bool
}
```

## Usage

```go
import "github.com/webcenter-fr/eino-ext/components/document/loader/opensearch"

loader, _ := osparser.NewOpensearchLoader(ctx, &osloader.Config{
    URLs: []string{"https://localhost:9200"},
})
docs, err := loader.Load(ctx, document.Source{
    URI: "opensearch://my-index?q=status:published",
})
```
