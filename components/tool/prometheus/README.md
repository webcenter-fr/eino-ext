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
- **Read + write split** — Prometheus metrics/targets tools are read-only.
  Alertmanager alert management adds one read tool (`prometheus_alert`) and one
  write tool (`prometheus_alert_write`), the latter gated by `dryRun`/`confirmed`.
- **Alertmanager** — an optional `Alertmanager` sub-config per instance enables
  the two alert tools. Instances without it only expose the read-only
  Prometheus tools.

## Configuration

```go
import (
    "github.com/webcenter-fr/eino-ext/components/tool/prometheus"
)

configs := prometheus.Configs{
    "prod": prometheus.Config{
        Address: "https://prometheus.prod.example.com",
        BearerToken: os.Getenv("PROMETHEUS_TOKEN"),
        Alertmanager: &prometheus.AlertmanagerConfig{
            Address:     "https://alertmanager.prod.example.com",
            BearerToken: os.Getenv("ALERTMANAGER_TOKEN"),
        },
    },
    "staging": prometheus.Config{
        Address:  "http://localhost:9090",
    },
}
```

`Alertmanager` is optional. When set, `prometheus_alert` and
`prometheus_alert_write` become available for that instance; when nil, those
tools return a not-found error for the instance. `AlertmanagerConfig` supports
`Address` (required, http/https), `Username`/`Password` (basic auth),
`BearerToken`, `TLSSkipVerify`, and `Timeout` (Go duration string, default
`30s`).

## Available Tools

| Tool Name | Type | Description |
|---|---|---|
| `prometheus_instance_list` | Read | List all configured Prometheus instances |
| `prometheus_metric` | Read | Execute a PromQL query (instant or range, `mode` discriminator) |
| `prometheus_alert` | Read | Read alerts from the associated Alertmanager (list or get-single via `fingerprint`) |
| `prometheus_target_list` | Read | List active scrape targets and their health status |
| `prometheus_alert_write` | Write | Create, update, or delete (resolve) an Alertmanager alert |

## Factory Functions

```go
// All tools (read + write)
tools, err := prometheus.NewAllTools(ctx, configs)

// Read-only tools (excludes prometheus_alert_write)
tools, err := prometheus.NewReadOnlyTools(ctx, configs)

// All tools with pre-configured safety middleware
tools, mw, err := prometheus.NewAllToolsWithSafety(ctx, configs, safetyCfg)
```

`prometheus.WriteToolNames()` returns `["prometheus_alert_write"]`. The write
tool is gated by `dryRun`/`confirmed` (use `dryRun=true` to preview, then
`confirmed=true` to execute).

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

### prometheus_alert

Reads alerts from the Alertmanager associated with a Prometheus instance
(Alertmanager `/api/v2/alerts` only). Read-only (no confirmation needed). Can
fetch all alerts, a single alert by `fingerprint`, or filter by state/matchers.

| Parameter | Required | Description |
|---|---|---|
| `instance` | Yes | Prometheus instance name (must have `Alertmanager` configured) |
| `fingerprint` | No | Return only the alert with this fingerprint. Takes precedence over `alertFilter`/`state` |
| `state` | No | Filter by Alertmanager state: `active`, `unprocessed`, or `suppressed` |
| `alertFilter` | No | Alertmanager matcher string (e.g. `alertname="HighCPU"`); comma-separate multiple matchers |
| `filter` | No | Go RE2 regex on alert JSON |
| `paginate` | No | Pagination with `pageSize` (default 20) and `paginateToken` |

Each result contains: `labels`, `annotations`, `state`, `startsAt`, `endsAt`
(both RFC3339), `fingerprint`, `silencedBy`, and `receivers`.

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

### prometheus_alert_write

Create, update, or delete (resolve) an alert on the associated Alertmanager.
This is a **write tool**: always use `dryRun=true` first to preview, then set
`confirmed=true` to execute. The required `operation` param selects the action:
`create`, `update`, or `delete`.

| Parameter | Required | Description |
|---|---|---|
| `instance` | Yes | Prometheus instance name (must have `Alertmanager` configured) |
| `operation` | Yes | `create`, `update`, or `delete` |
| `labels` | Yes | Alert labels; must include `alertname`. For `create`/`delete` these are the alert's labels; for `update` they identify the existing alert |
| `annotations` | No | Alert annotations. Used by `create`/`update` (update: omit to keep, set to replace) |
| `startsAt` | No | RFC3339. `create`: defaults to now. `update`: omit to keep existing. `delete`: ignored |
| `endsAt` | No | RFC3339. `create`: defaults to now+5m, must be future. `update`: omit to keep existing. `delete`: ignored |
| `generatorURL` | No | Source URL (http/https). `create`: sets it. `update`: omit to keep existing. `delete`: ignored |
| `dryRun` | No | Preview the resolved payload without posting |
| `confirmed` | No | Must be true to actually post |

Operation semantics:

- `create` — POST a new firing alert. `endsAt` must be in the future and after
  `startsAt`.
- `update` — fetch the existing alert matching `labels`, merge the provided
  fields, and re-POST (upsert). Errors if no existing alert matches. On
  multiple matches, the first is used and its `fingerprint` is returned.
- `delete` — re-POST the alert with `endsAt <= now` to resolve it (Alertmanager
  has no `DELETE /alerts`). Idempotent; no pre-existence check.

## Output Limiting

All tools accept an optional `filter` parameter (Go RE2 regex) applied against
each result's JSON representation. Only matching results are returned.

List tools additionally support pagination: set `paginate.pageSize` and pass the
returned `paginateToken` to fetch subsequent pages.

## Breaking changes

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
