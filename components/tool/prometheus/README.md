# Prometheus Tools

eino tools for querying Prometheus metrics and alerts via the
`prometheus/client_golang` API client.

## Design

- **Multi-instance** — configure multiple named Prometheus servers via a
  `Configs` map, matching the Kubernetes/ArgoCD tool patterns.
- **Authentication** — supports Basic auth (username/password) and Bearer
  token auth. Bearer takes priority over Basic.
- **TLS** — optional `TLSSkipVerify` for self-signed certificates.
- **Output limiting** — all tools support Go RE2 regex filters on result JSON.
  List tools support pagination.
- **Read-only** — all tools are read-only (no `WriteToolNames`).

## Configuration

```go
import (
    "github.com/webcenter-fr/eino-ext/components/tool/prometheus"
)

configs := prometheus.Configs{
    "prod": prometheus.Config{
        Address: "https://prometheus.prod.example.com",
        BearerToken: os.Getenv("PROMETHEUS_TOKEN"),
    },
    "staging": prometheus.Config{
        Address:  "http://localhost:9090",
    },
}
```

## Available Tools

| Tool Name | Description |
|---|---|
| `prometheus_metric_query` | Execute an instant PromQL query |
| `prometheus_metric_range` | Execute a range PromQL query over a time window |
| `prometheus_alert_list` | List current alerts with lightweight output and pagination |
| `prometheus_alert_describe` | Get full details of alerts matching a label regex filter |

## Factory Functions

```go
// All tools (same as NewReadOnlyTools since all tools are read-only)
tools, err := prometheus.NewAllTools(ctx, configs)

// Read-only tools
tools, err := prometheus.NewReadOnlyTools(ctx, configs)

// All tools with pre-configured safety middleware
tools, mw, err := prometheus.NewAllToolsWithSafety(ctx, configs, safetyCfg)
```

## Tool Details

### prometheus_metric_query

Instant PromQL query at a point in time.

| Parameter | Required | Description |
|---|---|---|
| `instance` | Yes | Prometheus instance name from Configs |
| `query` | Yes | PromQL query string |
| `time` | No | Evaluation time in RFC3339 (default: now) |
| `filter` | No | Go RE2 regex on result JSON |
| `limit` | No | Max result series (1–50000) |

### prometheus_metric_range

Range PromQL query over a time window.

| Parameter | Required | Description |
|---|---|---|
| `instance` | Yes | Prometheus instance name |
| `query` | Yes | PromQL query string |
| `start` | Yes | Start time in RFC3339 |
| `end` | Yes | End time in RFC3339 |
| `step` | Yes | Query resolution step (e.g. `15s`, `1m`) |
| `filter` | No | Go RE2 regex on result JSON |
| `maxSamples` | No | Max samples per series (default 100, max 10000) |

### prometheus_alert_list

List current alerts with lightweight output.

| Parameter | Required | Description |
|---|---|---|
| `instance` | Yes | Prometheus instance name |
| `state` | No | Filter by state: `firing`, `pending`, or `inactive` |
| `filter` | No | Go RE2 regex on alert JSON |
| `paginate` | No | Pagination with `pageSize` (default 20) and `paginateToken` |

### prometheus_alert_describe

Full details of alerts matching a label regex.

| Parameter | Required | Description |
|---|---|---|
| `instance` | Yes | Prometheus instance name |
| `filter` | Yes | Go RE2 regex on alert label JSON (e.g. `HighCPU\|HighMemory`) |
| `state` | No | Filter by state: `firing`, `pending`, or `inactive` |

## Output Limiting

All tools accept an optional `filter` parameter (Go RE2 regex) applied against
each result's JSON representation. Only matching results are returned.

List tools additionally support pagination: set `paginate.pageSize` and pass the
returned `paginateToken` to fetch subsequent pages.
