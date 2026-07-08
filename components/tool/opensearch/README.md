# OpenSearch Search Tool

A generic, reusable OpenSearch search tool for eino that supports arbitrary
indices, Lucene queries, optional date-range filtering, configurable output
formatting, and PIT-based scrolling. Provides both batch (`InvokableTool`) and
streaming (`StreamableTool`) modes.

## Design

- **OpenSearch client** — uses `github.com/disaster37/opensearch/v4` via the
  shared `osclient.Config` connection configuration. Only `URLs[0]` is used.
  `TLSSkipVerify` controls certificate verification (defaults to `false`).
- **PIT scrolling** — uses Point-in-Time (`POST /_search/point_in_time`) with
  `search_after` for consistent deep pagination (500 hits per batch).
- **Result formatting** — each hit is converted to a map enriched with `_id`,
  `_index`, `_score`, and `_version` metadata, then passed through an optional
  `SearchResultParser` callback. When no parser is configured, hits are
  serialized as compact JSON.
- **Streaming** — implements both `tool.InvokableTool` and `tool.StreamableTool`
  for batch and streaming result retrieval.

## Configuration

```go
import (
    "context"

    "github.com/webcenter-fr/eino-ext/components/tool/opensearch"
    "github.com/webcenter-fr/eino-ext/libs/toolkit/osclient"
)

cfg := &opensearch.SearchConfig{
    Config: osclient.Config{
        URLs:          []string{"https://opensearch.example.com:9200"},
        Username:      "admin",
        Password:      os.Getenv("OPENSEARCH_PASSWORD"),
        TLSSkipVerify: false,
    },
    DefaultIndex: "logs-*",
    TimeField:    "@timestamp",
    DefaultSort:  "@timestamp:desc",
    MaxResults:   100,
}

tool, err := opensearch.NewSearchTool(ctx, cfg)
```

### SearchConfig Fields

| Field | Type | Default | Description |
|---|---|---|---|
| `Config` | `osclient.Config` | (required) | OpenSearch connection configuration |
| `DefaultIndex` | `string` | (required) | Default index pattern to search |
| `TimeField` | `string` | `@timestamp` | Default timestamp field for date range queries |
| `DefaultSort` | `string` | `@timestamp:desc` | Default sort (`field:asc` or `field:desc`) |
| `MaxResults` | `int` | `100` | Default maximum results (capped at 10,000) |
| `ResultParser` | `SearchResultParser` | `nil` | Optional custom hit→string formatter |

## Parameters

The LLM provides per-invocation parameters via JSON:

```json
{
    "indices": ["app-logs-*"],
    "queryString": "level:error AND service:api",
    "from": "now-1h",
    "to": "now",
    "timeField": "created_at",
    "sort": "@timestamp:desc",
    "maxResults": 200
}
```

### SearchParams Fields

| Field | Type | Required | Description |
|---|---|---|---|
| `indices` | `[]string` | no | Indices to search (falls back to `DefaultIndex`) |
| `queryString` | `string` | no | Lucene query string; `"*"` for all documents |
| `from` | `string` | no | Start time (relative like `now-1h` or absolute RFC3339) |
| `to` | `string` | no | End time (relative like `now` or absolute RFC3339) |
| `timeField` | `string` | no | Override the default timestamp field |
| `sort` | `string` | no | Override the default sort |
| `maxResults` | `int` | no | Override the default max results (capped at 10,000) |

## SearchResultParser

Customize hit formatting by providing a `SearchResultParser` in the config:

```go
type SearchResultParser func(ctx context.Context, hit map[string]any) (string, error)
```

The hit map contains all source fields plus metadata (`_id`, `_index`, `_score`,
`_version`). When `ResultParser` is nil, hits are serialized as compact JSON.

Example parser that extracts a specific field:

```go
cfg.ResultParser = func(ctx context.Context, hit map[string]any) (string, error) {
    msg, _ := hit["message"].(string)
    return fmt.Sprintf("[%s] %s", hit["_index"], msg), nil
}
```

## Usage

```go
// Batch mode
result, err := tool.InvokableRun(ctx, `{
    "indices": ["app-logs-*"],
    "queryString": "level:error",
    "from": "now-1h",
    "maxResults": 50
}`)

// Streaming mode
stream, err := tool.StreamableRun(ctx, `{
    "indices": ["app-logs-*"],
    "queryString": "status:500",
    "maxResults": 100
}`)
```
