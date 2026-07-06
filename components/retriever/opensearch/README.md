# OpenSearch retriever

`opensearch` implements `retriever.Retriever` backed by OpenSearch. It
supports optional search pipelines and configurable result parsers.

## Configuration

```go
type Config struct {
    URLs           []string
    Username       string
    Password       string
    TLSSkipVerify  bool
    ResultParser   ResultParser // custom hit → document conversion
    SearchPipeline string       // optional pipeline name
}
```

## Usage

```go
import "github.com/webcenter-fr/eino-ext/components/retriever/opensearch"

retriever, _ := osretriever.NewRetriever(ctx, &osretriever.Config{
    URLs: []string{"https://localhost:9200"},
})
docs, err := retriever.Retrieve(ctx, "search terms",
    retriever.WithIndex("my-index"),
    retriever.WithTopK(10),
)
```
