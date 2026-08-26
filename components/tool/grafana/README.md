# Grafana Tools

eino tools for interacting with Grafana instances via the HTTP API (v9+).

## Design

- **HTTP API** — uses `net/http` with Bearer token auth (no dedicated Go client library).
- **Multi-instance** — `Configs map[string]Config`, matching the argocd/kubernetes pattern.
- **Dashboard protection** — per-instance blocklist (UID, title prefix, folder, tag) prevents modification or deletion of protected dashboards.
- **Safety** — the write tool enforces a dry-run/confirmed gate via `confirm.RequireConfirmation`.
- **Data source secrets redaction** — data source tools exclude top-level secrets and recursively redact sensitive `jsonData` keys. Data sources are read-only (no write tool).

## Configuration

```go
import "github.com/webcenter-fr/eino-ext/components/tool/grafana"

configs := grafana.Configs{
    "prod": {
        URL:   "https://grafana.prod.example.com",
        Token: "glsa_...", // service account token
        ProtectedDashboards: grafana.ProtectedDashboardsConfig{
            UIDs:          []string{"k8s-monitoring", "infra-overview"},
            TitlePrefixes: []string{"Kubernetes ", "Infra: "},
            Tags:          []string{"protected", "infrastructure"},
        },
        DefaultTimeout: "10s",
    },
    "staging": {
        URL:   "https://grafana.staging.example.com",
        Token: "glsa_...",
    },
}
```

## Available Tools

| Tool | Type | Description |
|---|---|---|
| `grafana_instance_list` | Read | List configured Grafana instances |
| `grafana_dashboard` | Read | Search dashboards (uid empty) or get full details by UID (uid set) |
| `grafana_dashboard_write` | Write | Create, update, or delete a dashboard (blocklist-enforced) |
| `grafana_datasource` | Read | List data sources (uid empty) or get full details by UID (uid set; secrets redacted). Read-only — no write tool |
| `grafana_query` | Read | Execute a PromQL/LogQL query and return cardinality + sample series |
| `grafana_dashboard_validate` | Read | Validate a saved dashboard's panels by executing their Prometheus/Loki queries |

## Factory Functions

```go
// All tools (read + write)
tools, err := grafana.NewAllTools(ctx, configs)

// Read-only tools (no write operations)
readOnlyTools, err := grafana.NewReadOnlyTools(ctx, configs)

// All tools with safety middleware
tools, mw, err := grafana.NewAllToolsWithSafety(ctx, configs, &safety.Config{
    Policy: myCELPolicy,
})

// Write tool names for safety middleware
names := grafana.WriteToolNames() // ["grafana_dashboard_write"]
```

`grafana_query` and `grafana_dashboard_validate` are read-only tools and are
included automatically in both `NewAllTools` and `NewReadOnlyTools`.

## Dashboard Protection

The dashboard write tool enforces a per-instance blocklist for `update` **and**
`delete`. A dashboard is protected if it matches **any** of the following criteria:

- **UID** — exact match in the UIDs list
- **Title prefix** — title starts with any string in TitlePrefixes
- **Folder** — resides in a folder listed in Folders
- **Tag** — carries a tag listed in Tags

All four criteria are optional. If none are configured, no dashboards are protected.

Example: with the config above, the write tool will reject any attempt to modify
or delete:

- Dashboard with UID `k8s-monitoring` or `infra-overview`
- Any dashboard whose title starts with "Kubernetes " or "Infra: "
- Any dashboard carrying the tag "protected" or "infrastructure"

## Tool Details

### grafana_dashboard

Reads dashboards. Set `uid` to describe a single dashboard; leave `uid` empty
to search.

| Parameter | Required | Description |
|---|---|---|
| `instance` | Yes | Grafana instance name |
| `uid` | No | If set, return the full dashboard with this UID (describe mode). If empty, search (list mode) |
| `query` | No | (search) Title search query |
| `type` | No | (search) Filter by type: `dash-db` or `dash-folder` |
| `tags` | No | (search) Filter by tags (ALL must match) |
| `folderUIDs` | No | (search) Filter by folder UIDs |
| `sort` | No | (search) Sort order |
| `filter` | No | (search) Go RE2 regex on each search output JSON |
| `paginate` | No | (search) `pageSize` (default 100) and `page` (default 1) |
| `excludeFieldsOutput` | No | (describe) Fields to exclude: `meta`, `panels`, `templating`, `time`, `annotations`, `schemaVersion`, `version` |

### grafana_dashboard_write

Creates, updates, or deletes a dashboard. The required `operation` param
selects `create`, `update`, or `delete`. This is a **write tool**: always use
`dryRun=true` first to preview, then set `confirmed=true` to execute.

| Parameter | Required | Description |
|---|---|---|
| `instance` | Yes | Grafana instance name |
| `operation` | Yes | `create`, `update`, or `delete` |
| `dashboard` | No | (create/update) Full dashboard model as a JSON string; must include `title`. Include `uid` to target an existing dashboard. Ignored for delete. For update, may be omitted when `changes` is provided — the tool auto-fetches the existing dashboard |
| `changes` | No | (update only) Partial dashboard model as a JSON object containing only the fields to change. The tool auto-fetches the existing dashboard by UID, deep-merges the changes on top, and saves. Use this for targeted updates (e.g. modifying a template variable) without providing the full dashboard model. Ignored for create and delete |
| `uid` | No | (delete/update) For delete: the dashboard UID to delete. For update: may be provided here instead of inside the model. Required when `changes` is provided without `dashboard`. Ignored for create |
| `folderUID` | No | (create/update) Folder UID to place the dashboard in |
| `message` | No | (create/update) Commit message for the version |
| `overwrite` | No | (create/update) Overwrite without version checking |
| `dryRun` | No | Preview without saving/deleting |
| `confirmed` | No | Must be true to execute |

Operation semantics:

- `create` / `update` — both POST to `/api/dashboards/db` (Grafana upsert
  semantics). `delete` uses `DELETE /api/dashboards/uid/:uid`.
- Protection applies to `update` **and** `delete`. `delete` first fetches the
  existing dashboard; a 404 is surfaced as a not-found error (not silent
  success).

### grafana_datasource

Reads data sources. Set `uid` to describe a single data source; leave `uid`
empty to list. **Read-only** — data source writes are not in LLM scope.

| Parameter | Required | Description |
|---|---|---|
| `instance` | Yes | Grafana instance name |
| `uid` | No | If set, return the full data source with this UID (describe mode). If empty, list all (array) |
| `filter` | No | (list) Go RE2 regex on each data source list output JSON |

Sensitive fields (`password`, `basicAuthPassword`, `secureJsonFields`,
`secureJsonData`) are excluded, and sensitive `jsonData` keys are recursively
redacted.

### grafana_query

Executes a single PromQL (Prometheus) or LogQL (Loki) query against a datasource
by UID via Grafana's datasource proxy, and returns a cardinality-focused
summary. The datasource type is resolved automatically from the UID.

| Parameter | Required | Description |
|---|---|---|
| `instance` | Yes | Grafana instance name |
| `datasourceUID` | Yes | UID of the Prometheus or Loki datasource to query |
| `expr` | Yes | The PromQL or LogQL expression to execute |
| `queryType` | No | `instant` (default) or `range` |
| `time` | No | Anchor time: `now` (default), `now-1h`, RFC3339, or Unix seconds |
| `start` | No | (range) Start time. Defaults to `time-1h` |
| `stepSeconds` | No | (range) Step size in seconds (default 60) |
| `maxSeries` | No | Cap on series returned in `series` (default 20); `seriesCount` is the true total |

Output: `datasourceUid`, `datasourceType`, `expr`, `queryType`, `resultType`,
`seriesCount`, `truncated`, `series[]` (label sets + a sample value/line), and
`hints[]`. An empty result (`seriesCount=0`) is a normal result, not an error.
Loki log lines are returned verbatim (truncated to 256 characters) and may
contain sensitive data from the operator's own logs.

### grafana_dashboard_validate

Fetches a saved dashboard by UID and validates every panel by executing its
Prometheus/Loki queries (instant queries, default `now`). Returns per-panel and
per-query verdicts: `ok`, `no-data`, `too-many-series`, `error`, or `skipped`,
plus a roll-up `summary`.

| Parameter | Required | Description |
|---|---|---|
| `instance` | Yes | Grafana instance name |
| `uid` | Yes | UID of the saved dashboard to validate |
| `panelID` | No | Validate only this panel by id |
| `time` | No | Instant-query anchor time (default `now`) |
| `maxSeriesPerPanel` | No | Panels with more series are flagged `too-many-series` (default 20) |
| `maxPanels` | No | Cap on panels to validate (default 50); excess panels are skipped |
| `maxSeriesSample` | No | Sample label sets per query (default 5; 0 disables sample labels) |

Use this after creating or updating a dashboard to confirm its panels return
data. Non-Prometheus/Loki panels are reported as `skipped` with a reason.

## Usage Example

```go
// Read dashboards (search when uid empty, describe when uid set)
dashboards := grafana.NewDashboardTool(ctx, configs) // returns (*DashboardTool, error)
result, _ := dashboards.InvokableRun(ctx, `{
    "instance": "prod",
    "query": "production",
    "type": "dash-db"
}`)

// Create a dashboard
write := grafana.NewDashboardWriteTool(ctx, configs) // returns (*DashboardWriteTool, error)
result, _ = write.InvokableRun(ctx, `{
    "instance": "prod",
    "operation": "create",
    "dashboard": "{\"title\": \"My Dashboard\", \"panels\": []}",
    "folderUID": "general",
    "confirmed": true
}`)
```

## Breaking changes

- Removed tools: `grafana_dashboard_search`, `grafana_dashboard_describe`,
  `grafana_dashboard_build`, `grafana_datasource_list`, `grafana_datasource_describe`.
- New tools: `grafana_dashboard` (read, `uid` discriminator),
  `grafana_dashboard_write` (write, `operation` discriminator), and
  `grafana_datasource` (read, `uid` discriminator).
- Removed constructors/types: `NewDashboardSearchTool`/`DashboardSearchTool`/
  `DashboardSearchParams`, `NewDashboardDescribeTool`/`DashboardDescribeTool`/
  `DashboardDescribeParams`, `NewDashboardBuildTool`/`DashboardBuildTool`/
  `DashboardBuildParams`, `NewDataSourceListTool`/`DataSourceListTool`/
  `DataSourceListParams`, `NewDataSourceDescribeTool`/
  `DataSourceDescribeTool`/`DataSourceDescribeParams`.
- Renamed types: `DashboardSearchPaginate` → `DashboardPaginate`,
  `DashboardBuildOutput` → `DashboardSaveOutput`. New:
  `DashboardWriteTool`/`NewDashboardWriteTool`/`DashboardWriteParams`/
  `DashboardDeleteOutput`.
