# osclient — Shared OpenSearch client constructor

`osclient` provides a shared constructor for the eino-ext OpenSearch v4 client
(`github.com/disaster37/opensearch/v4`). It centralizes the connection
configuration and error-wrapping convention that was duplicated across the
OpenSearch indexer, retriever, loader, memory and tool components.

This is the eino-component OpenSearch client. It is distinct from the eino
OpenSearch client scaffolding used by the upstream eino-ext
`retriever/indexer` abstractions — do not unify them.

## API

```go
type Config struct {
    URLs          []string
    Username      string
    Password      string
    TLSSkipVerify bool
}

func New(cfg Config, timeout time.Duration) (opensearchv4.Client, error)
```

- `Config` — the connection fields shared by every OpenSearch component, with
  the shared `validate`+`jsonschema` tags.
- `New` — builds an OpenSearch v4 client. A zero `timeout` leaves the client
  default in place.

## Usage

```go
import "github.com/webcenter-fr/eino-ext/libs/toolkit/osclient"

client, err := osclient.New(osclient.Config{
    URLs:          config.URLs,
    Username:      config.Username,
    Password:      config.Password,
    TLSSkipVerify: config.TLSSkipVerify,
}, 30*time.Second)
if err != nil {
    return nil, err
}
```
