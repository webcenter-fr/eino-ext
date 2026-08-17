# Prometheus Tools

eino tools for querying Prometheus metrics via the
`prometheus/client_golang` API client.

## Design

- **Multi-instance** — configure multiple named Prometheus servers via a
  `Configs` map, matching the Kubernetes/ArgoCD tool patterns.
- **Authentication** — supports Basic auth (username/password) and Bearer
  token auth. Bearer takes priority over Basic.
- **TLS** — optional `TLSSkipVerify` for self-signed certificates.
- **Output limiting** — all tools support Go RE2 regex filters on result JSON.
  List tools support pagination.
- **Read-only** — Prometheus metrics/targets tools are read-only; this
  component exposes no write tools. Alert management lives in the dedicated
  `components/tool/alertmanager` package.

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

`Config` supports `Address` (required, http/https), `Username`/`Password`
(basic auth), `BearerToken`, and `TLSSkipVerify`.

## Available Tools

| Tool Name | Type | Description |
|---|---|---|
| `prometheus_instance_list` | Read | List all configured Prometheus instances |
| `prometheus_metric` | Read | Execute a PromQL query (instant or range, `mode` discriminator) |
| `prometheus_target_list` | Read | List active scrape targets and their health status |

## Factory Functions

```go
// All tools (read + write)
tools, err := prometheus.NewAllTools(ctx, configs)

// Read-only tools (same set as above: Prometheus has no write tools)
tools, err := prometheus.NewReadOnlyTools(ctx, configs)

// All tools with pre-configured safety middleware
tools, mw, err := prometheus.NewAllToolsWithSafety(ctx, configs, safetyCfg)
```

`prometheus.WriteToolNames()` returns `[]string{}` (no write tools).

## Tool Details

### prometheus_metric

Executes a PromQL query. The required `mode` param selects instant
(single-point) or range (time-window) evaluation.

| Parameter | Required | Description |
|---|---|---|
| `instance` | Yes | Prometheus instance name from Configs |
| `mode` | Yes | `instant` or `range` |
| `query` | Yes | PromQL query string |
| `filter` | No | Go RE2 regex on result JSON |
| `time` | No | (instant) Evaluation time in RFC3339 (default: now) |
| `limit` | No | (instant) Max result series (1–50000) |
| `start` | No* | (range) Start time in RFC3339 (*required in range mode) |
| `end` | No* | (range) End time in RFC3339 (*required in range mode) |
| `step` | No* | (range) Resolution step (e.g. `15s`, `1m`); must be >= 15s (*required in range mode) |
| `maxSamples` | No | (range) Max samples per series (default 100, max 10000) |

PromQL subqueries with a range greater than 7 days are rejected in **both**
modes to prevent excessive load on the Prometheus server.

### prometheus_target_list

List active scrape targets and their health status. Useful for verifying that
metrics are being scraped correctly (e.g. no network policy issues, unreachable
exporters, or misconfigured scrape jobs).

| Parameter | Required | Description |
|---|---|---|
| `instance` | Yes | Prometheus instance name |
| `health` | No | Filter by health: `up`, `down`, or `unknown` |
| `scrapePool` | No | Filter by exact scrape pool name (e.g. `node/10.0.0.1:9100`) |
| `filter` | No | Go RE2 regex on target JSON |

Each result contains: `labels`, `scrapePool`, `scrapeUrl`, `health`,
`lastError`, `lastScrape` (RFC3339), and `lastScrapeDuration` (Go duration
string). Only active targets are returned; dropped targets are excluded.

## Output Limiting

All tools accept an optional `filter` parameter (Go RE2 regex) applied against
each result's JSON representation. Only matching results are returned.

## Breaking changes

- **Moved**: `prometheus_alert` and `prometheus_alert_write` moved to the
  dedicated `components/tool/alertmanager` package and were renamed
  `alertmanager_alert` and `alertmanager_alert_write`. The
  `prometheus.Config.Alertmanager` field and `prometheus.AlertmanagerConfig`
  type were removed; configure Alertmanager via `alertmanager.Configs`
  instead. The underlying client changed from a hand-rolled `net/http`
  client to the official Alertmanager v2 client (no wire-format change).
- Removed tools: `prometheus_metric_query`, `prometheus_metric_range`,
  `prometheus_alert_list`, `prometheus_alert_describe`.
- Renamed tools: `prometheus_alertmanager_alert_list` → `prometheus_alert`,
  `prometheus_alertmanager_alert_write` → `prometheus_alert_write`.
- Removed constructors/types: `NewMetricQueryTool`/`MetricQueryTool`/
  `MetricQueryParams`/`MetricQueryOutput`, `NewMetricRangeTool`/
  `MetricRangeTool`/`MetricRangeParams`, `NewAlertListTool`/`AlertListTool`/
  `AlertListParams`/`AlertListOutput`, `NewAlertDescribeTool`/
  `AlertDescribeTool`/`AlertDescribeParams`. `MetricInstantOutput` replaces
  `MetricQueryOutput`; `MetricRangeOutput` is kept.
- Renamed types: `AlertmanagerAlertListTool` → `AlertTool`,
  `AlertmanagerAlertListParams` → `AlertParams`, `AlertmanagerAlertListOutput`
  → `AlertOutput`, `NewAlertmanagerAlertListTool` → `NewAlertTool`,
  `AlertmanagerAlertWriteTool` → `AlertWriteTool`,
  `AlertmanagerAlertWriteParams` → `AlertWriteParams`,
  `AlertmanagerAlertWriteOutput` → `AlertWriteOutput`,
  `NewAlertmanagerAlertWriteTool` → `NewAlertWriteTool`,
  `AlertListPaginate` → `AlertPaginate`.
