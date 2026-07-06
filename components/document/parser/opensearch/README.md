# OpenSearch document parser

`opensearch` parses OpenSearch search response JSON into
`[]*schema.Document`. It supports configurable field selectors via ucfg for
content extraction and attaches standard OpenSearch metadata (`_id`,
`_index`, `_score`, `_version`) plus source hash and ID to each document.

## Configuration

```go
type Config struct {
    FieldSelectors  []string
    FieldIgnores    []string
    SourceIDField   string // default "source_id"
    SourceHashField string // default "source_hash"
}
```

## Usage

```go
import "github.com/webcenter-fr/eino-ext/components/document/parser/opensearch"

parser, _ := osparser.NewParser(ctx, &osparser.Config{
    FieldSelectors: []string{"content", "title"},
})
docs, err := parser.Parse(ctx, reader)
```
