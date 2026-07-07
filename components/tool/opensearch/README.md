# OpenSearch Tool

An eino tool for retrieving Kubernetes pod logs from OpenSearch.

## Design

- **OpenSearch client** — uses `github.com/disaster37/opensearch/v4`
  client via the shared `osclient.Config` connection configuration. Only
  `URLs[0]` is used. `TLSSkipVerify` controls certificate verification
  (defaults to `false`).
- **Streaming** — implements both `tool.InvokableTool` and `tool.StreamableTool`
  for batch and streaming log retrieval.
- **Lucene query** — supports Lucene query syntax for log filtering.

## Configuration

```go
import (
    "github.com/webcenter-fr/eino-ext/components/tool/opensearch"
    "github.com/webcenter-fr/eino-ext/libs/toolkit/osclient"
)

cfg := &osclient.Config{
    URLs:          []string{"https://opensearch.example.com:9200"},
    Username:      "admin",
    Password:      os.Getenv("OPENSEARCH_PASSWORD"),
    TLSSkipVerify: false,
}

tool, err := opensearch.NewOpensearchLogKubernetesTool(ctx, cfg)
```

## Parameters

```json
{
    "cluster": "prod",
    "namespace": "default",
    "pod_name": "my-pod-abc123",
    "container_name": "main",
    "from": "2025-01-01T00:00:00Z",
    "to": "2025-01-02T00:00:00Z",
    "lucene_query": "level:error",
    "max_lines": 100
}
```

| Field | Description | Default |
|---|---|---|
| `cluster` | K8s cluster field in log index | (required) |
| `namespace` | K8s namespace | (required) |
| `pod_name` | Pod name | (required) |
| `container_name` | Container name | (optional) |
| `from` | Start time (RFC 3339) | (required) |
| `to` | End time (RFC 3339) | (required) |
| `lucene_query` | Lucene query string | (optional) |
| `max_lines` | Max log lines to return | 1–500 |

## Usage

```go
// Batch mode
result, err := tool.InvokableRun(ctx, `{
    "cluster": "prod",
    "namespace": "default",
    "pod_name": "my-pod-abc123",
    "from": "2025-01-01T00:00:00Z",
    "to": "2025-01-02T00:00:00Z",
    "max_lines": 100
}`)

// Streaming mode
stream, err := tool.StreamableRun(ctx, `{
    "cluster": "prod",
    "namespace": "default",
    "pod_name": "my-pod-abc123",
    "from": "2025-01-01T00:00:00Z",
    "to": "2025-01-02T00:00:00Z"
}`)
```
