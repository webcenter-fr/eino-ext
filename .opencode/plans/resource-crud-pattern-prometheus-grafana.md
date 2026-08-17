# Plan: Resource CRUD Pattern Consolidation — Prometheus & Grafana

Reshape every resource type in `components/tool/prometheus/` and `components/tool/grafana/` into the
single consistent pattern already established for Alertmanager alerts:

- **ONE get/list (read) tool per resource type** (read tools are NOT gated).
- **ONE create/update/delete (write) tool per resource type — ONLY if the backing API supports writes
  AND writes are in LLM scope.** Write tools use a required `operation` discriminator
  (`create`/`update`/`delete`) and are gated by `dryRun`/`confirmed` via
  `libs/toolkit/confirm.RequireConfirmation`.

Reference implementations: the existing Alertmanager alert write tool (write: `switch` on `operation`,
`dryRun`/`confirmed` gate, per-operation validation in code) and the existing Alertmanager alert list
tool (read: list + optional get-single via `fingerprint`).

The Prometheus-native alert readers (`alert_list.go`, `alert_describe.go`) are redundant with the
Alertmanager alert reader (same use case) and are removed entirely. The Alertmanager alert tools are
renamed to drop the `alertmanager_alert` prefix: `prometheus_alertmanager_alert_list` →
`prometheus_alert` and `prometheus_alertmanager_alert_write` → `prometheus_alert_write`. There is
**no `source` discriminator** and **no `alertmanager_alert` naming** anywhere in tool names or symbols.

---

## 1. Goal & Non-Goals

### Goal
Consolidate the per-resource-type tool surface in both components so that each resource type exposes
exactly one read tool and (where the API supports writes AND writes are in LLM scope) one write tool,
matching the Alertmanager alert pattern. Add the missing Grafana dashboard delete capability. Rename
the two Alertmanager alert tools to drop the `alertmanager_alert` prefix:
`prometheus_alertmanager_alert_list` → `prometheus_alert` (read, Alertmanager `/api/v2/alerts` only)
and `prometheus_alertmanager_alert_write` → `prometheus_alert_write` (write). Delete the redundant
Prometheus-native alert readers (`prometheus_alert_list`, `prometheus_alert_describe`).

### Non-Goals
- No new external Go dependencies (stdlib + existing `net/http`, `goccy/go-json`, `emperror.dev/errors`,
  `prometheus/client_golang`, `libs/toolkit/*` only).
- No changes to `Config`/`Configs`/`AlertmanagerConfig` structs (the dashboard protection config and
  datasource redaction config stay as-is).
- No changes to `alertmanager_client.go` (the Alertmanager HTTP client stays as-is — still used by
  `prometheus_alert` and `prometheus_alert_write`).
- No changes to `instance_list.go` in either component (read-only meta, already a single tool).
- No changes to `prometheus_target_list.go` (already a single read tool; Prometheus targets API is
  read-only).
- No Grafana data source write tool — data source writes are **not in LLM scope**. Grafana data
  sources remain READ-ONLY (`grafana_datasource` read tool only).
- No migration tooling or runtime compatibility shims (see Breaking changes decision below).
- No `source`/`detail` discriminator on `prometheus_alert` — it reads Alertmanager only.
- No Prometheus `/api/v1/alerts` read tools — the Prometheus-native alert readers are removed.

---

## 2. Breaking Changes (decision: remove old tools entirely)

**Decision: Remove the old per-resource tools and their public constructors entirely.** Do NOT keep
deprecated wrappers.

### Rationale
- The Alertmanager reference pattern was added fresh (no legacy tools to wrap); matching it requires a
  clean, single tool per resource type.
- Wrappers would expose two tool names per resource (e.g. `prometheus_metric_query` AND
  `prometheus_metric`) doing the same work, which confuses LLM tool selection and doubles maintenance.
- `NewAllTools`/`NewReadOnlyTools` would only return the new tools either way; wrappers only help direct
  constructor callers, a smaller audience than the confusion they introduce.
- The library is actively establishing its tool pattern; a clean break with documented breaking changes
  is healthier than a long-tail of deprecated aliases.

**Considered & rejected — option (b) thin deprecated wrappers:** keep e.g. `NewMetricQueryTool` as a
wrapper delegating to the new `MetricTool` logic but not registered in `readOnlyConstructors`. Rejected
because it leaves two tool names in the wild (`prometheus_metric_query` from direct construction vs
`prometheus_metric` from factories), invites drift, and the alertmanager precedent did not use wrappers.

### Removed / renamed symbols

#### Prometheus (`components/tool/prometheus/`)
| Removed symbol | Kind | Replacement |
|---|---|---|
| `MetricQueryTool` | type | `MetricTool` (`prometheus_metric`) |
| `MetricQueryParams` | type | `MetricParams` |
| `MetricQueryOutput` | type | `MetricInstantOutput` |
| `NewMetricQueryTool` | constructor | `NewMetricTool` |
| `prometheus_metric_query` | tool name | `prometheus_metric` |
| `MetricRangeTool` | type | `MetricTool` (same) |
| `MetricRangeParams` | type | `MetricParams` (same) |
| `MetricRangeOutput` | type | `MetricRangeOutput` (kept) |
| `NewMetricRangeTool` | constructor | `NewMetricTool` (same) |
| `prometheus_metric_range` | tool name | `prometheus_metric` |
| `AlertListTool` | type | **removed** (Prometheus-native alert reader deleted; use `prometheus_alert` for Alertmanager alerts) |
| `AlertListParams` | type | **removed** |
| `AlertListPaginate` | type | `AlertPaginate` (moved to `helper.go`, renamed; shared by `prometheus_alert`) |
| `AlertListOutput` | type | **removed** |
| `NewAlertListTool` | constructor | **removed** |
| `prometheus_alert_list` | tool name | **removed** |
| `AlertDescribeTool` | type | **removed** |
| `AlertDescribeParams` | type | **removed** |
| `alertDescribeOutput` | type | **removed** |
| `toAlertDescribeOutput` | func | **removed** |
| `NewAlertDescribeTool` | constructor | **removed** |
| `prometheus_alert_describe` | tool name | **removed** |
| `AlertmanagerAlertListTool` | type | `AlertTool` (`prometheus_alert`) |
| `AlertmanagerAlertListParams` | type | `AlertParams` |
| `AlertmanagerAlertListOutput` | type | `AlertOutput` |
| `NewAlertmanagerAlertListTool` | constructor | `NewAlertTool` |
| `prometheus_alertmanager_alert_list` | tool name | `prometheus_alert` |
| `AlertmanagerAlertWriteTool` | type | `AlertWriteTool` (`prometheus_alert_write`) |
| `AlertmanagerAlertWriteParams` | type | `AlertWriteParams` |
| `AlertmanagerAlertWriteOutput` | type | `AlertWriteOutput` |
| `NewAlertmanagerAlertWriteTool` | constructor | `NewAlertWriteTool` |
| `prometheus_alertmanager_alert_write` | tool name | `prometheus_alert_write` |
| `alertmanagerAlertListToolName` | const | **removed** (folded into `prometheus_alert`) |
| `alertmanagerAlertWriteToolName` | const | `alertWriteToolName` (renamed, value `"prometheus_alert_write"`) |

Files deleted: `metric_query.go`, `metric_range.go`, `alert_list.go`, `alert_describe.go`,
`alertmanager_alert_list.go` (renamed to `alert.go`), `alertmanager_alert_write.go` (renamed to
`alert_write.go`).

Unchanged public symbols: `InstanceListTool`/`NewInstanceListTool` (`prometheus_instance_list`),
`TargetListTool`/`NewTargetListTool` (`prometheus_target_list`), all `Config`/`Configs`/
`AlertmanagerConfig` types, `Check`, `NewAllTools`/`NewReadOnlyTools`/`WriteToolNames`/
`ExtractWriteToolNames`/`NewAllToolsWithSafety` (signatures unchanged; contents updated).

#### Grafana (`components/tool/grafana/`)
| Removed symbol | Kind | Replacement |
|---|---|---|
| `DashboardSearchTool` | type | `DashboardTool` (`grafana_dashboard`) |
| `DashboardSearchParams` | type | `DashboardParams` |
| `DashboardSearchPaginate` | type | `DashboardPaginate` (renamed) |
| `DashboardSearchOutput` | type | `DashboardSearchOutput` (kept) |
| `NewDashboardSearchTool` | constructor | `NewDashboardTool` |
| `grafana_dashboard_search` | tool name | `grafana_dashboard` |
| `DashboardDescribeTool` | type | `DashboardTool` (same) |
| `DashboardDescribeParams` | type | `DashboardParams` (same) |
| `DashboardDescribeOutput` | type | `DashboardDescribeOutput` (kept) |
| `NewDashboardDescribeTool` | constructor | `NewDashboardTool` (same) |
| `grafana_dashboard_describe` | tool name | `grafana_dashboard` |
| `DashboardBuildTool` | type | `DashboardWriteTool` (`grafana_dashboard_write`) |
| `DashboardBuildParams` | type | `DashboardWriteParams` |
| `DashboardBuildOutput` | type | `DashboardSaveOutput` (renamed) |
| `NewDashboardBuildTool` | constructor | `NewDashboardWriteTool` |
| `grafana_dashboard_build` | tool name | `grafana_dashboard_write` |
| `DataSourceListTool` | type | `DataSourceTool` (`grafana_datasource`) |
| `DataSourceListParams` | type | `DataSourceParams` |
| `DataSourceListOutput` | type | `DataSourceListOutput` (kept) |
| `NewDataSourceListTool` | constructor | `NewDataSourceTool` |
| `grafana_datasource_list` | tool name | `grafana_datasource` |
| `DataSourceDescribeTool` | type | `DataSourceTool` (same) |
| `DataSourceDescribeParams` | type | `DataSourceParams` (same) |
| `DataSourceDescribeOutput` | type | `DataSourceDescribeOutput` (kept) |
| `NewDataSourceDescribeTool` | constructor | `NewDataSourceTool` (same) |
| `grafana_datasource_describe` | tool name | `grafana_datasource` |

Files deleted: `dashboard_build.go`, `dashboard_search.go`, `dashboard_describe.go`,
`datasource_list.go`, `datasource_describe.go`.

New public symbols: `DashboardWriteTool`/`NewDashboardWriteTool` (`grafana_dashboard_write`),
`DashboardDeleteOutput`.

No `DataSourceWriteTool` or datasource write output types — data source writes are out of scope.

Unchanged: `InstanceListTool`/`NewInstanceListTool`, all `Config`/`Configs`/
`ProtectedDashboardsConfig` types, `Check`, factory function signatures, `redact.go`, `base.go`
(protection logic), `helper.go` (`filterMapMarshal`, `applyExcludes`, `validateParams`).

---

## 3. Per-Component Resource-Type Tables

### Prometheus
| Resource | Read tool | Write tool | Discriminator (read) | Discriminator (write) |
|---|---|---|---|---|
| instance | `prometheus_instance_list` (keep) | — (read-only meta) | — | — |
| metric | `prometheus_metric` (new) | — (Prometheus has no metric write API) | `mode` ∈ `instant`\|`range` (required) | — |
| alert | `prometheus_alert` (renamed from `prometheus_alertmanager_alert_list`; reads Alertmanager `/api/v2/alerts` only) | `prometheus_alert_write` (renamed from `prometheus_alertmanager_alert_write`) | `fingerprint` set → get-single; empty → list | `operation` ∈ `create`\|`update`\|`delete` |
| target | `prometheus_target_list` (keep) | — (read-only) | — | — |

`WriteToolNames()` → `["prometheus_alert_write"]` (was `["prometheus_alertmanager_alert_write"]`).

### Grafana
| Resource | Read tool | Write tool | Discriminator (read) | Discriminator (write) |
|---|---|---|---|---|
| instance | `grafana_instance_list` (keep) | — (read-only meta) | — | — |
| dashboard | `grafana_dashboard` (new) | `grafana_dashboard_write` (new) | `uid` set → describe; empty → search | `operation` ∈ `create`\|`update`\|`delete` |
| datasource | `grafana_datasource` (new) | — (read-only, not in LLM scope) | `uid` set → describe; empty → list | — |

`WriteToolNames()` → `["grafana_dashboard_write"]`.

---

## 4. Detailed Per-File Specs

### 4.1 Prometheus — `metric.go` (new; replaces `metric_query.go` + `metric_range.go`)

**Tool name:** `prometheus_metric`
**Description:** merged from `metricQueryDescription` + `metricRangeDescription`, explaining the `mode`
discriminator and which fields apply to which mode.

```go
type MetricParams struct {
    Instance   string `json:"instance" validate:"required" jsonschema:"(required) The Prometheus instance to query."`
    Mode       string `json:"mode" validate:"required,oneof=instant range" jsonschema:"(required) Query mode: 'instant' (single-point evaluation) or 'range' (time-window series)."`
    Query      string `json:"query" validate:"required,max=4096" jsonschema:"(required) The PromQL query to execute."`
    Filter     string `json:"filter,omitempty" jsonschema:"(optional) Go RE2 regex applied on each result JSON. Keep only results that match. RE2 does NOT support lookahead/lookbehind/backreferences — such patterns return an error."`
    // instant-mode fields
    Time       string `json:"time,omitempty" jsonschema:"(optional, instant mode) Evaluation time in RFC3339. Defaults to now. Ignored in range mode."`
    Limit      int    `json:"limit,omitempty" validate:"omitempty,min=1,max=50000" jsonschema:"(optional, instant mode) Max result series (1-50000). Ignored in range mode."`
    // range-mode fields
    Start      string `json:"start,omitempty" jsonschema:"(optional, range mode) Start time in RFC3339. Required in range mode."`
    End        string `json:"end,omitempty" jsonschema:"(optional, range mode) End time in RFC3339. Required in range mode."`
    Step       string `json:"step,omitempty" jsonschema:"(optional, range mode) Resolution step as a Go duration (e.g. '15s', '1m'). Required in range mode; must be >= 15s."`
    MaxSamples int    `json:"maxSamples,omitempty" validate:"omitempty,min=1,max=10000" jsonschema:"(optional, range mode) Max samples per series (1-10000, default 100). Ignored in instant mode."`
}

type MetricInstantOutput struct {
    Metric model.Metric `json:"metric"`
    Value  any          `json:"value"` // [timestamp, "value"]
}

type MetricRangeOutput struct {
    Metric model.Metric       `json:"metric"`
    Values []model.SamplePair `json:"values"`
}

type MetricTool struct {
    *baseTool
    tool.InvokableTool
}
```

**Invoke logic** (`func (t *MetricTool) Invoke(ctx, *MetricParams) (string, error)`):
1. `validateParams(params)` (struct-level: `instance`, `mode`, `query` required; `limit`/`maxSamples`
   ranges enforced when present).
2. Compile `filter` regex via `filter.Compile` (wrap error with `errors.Wrap`).
3. Resolve client via `t.client(params.Instance)`.
4. **Subquery >7d rejection (applies to BOTH modes):** reuse the existing
   `subqueryLongRange := regexp.MustCompile(\[(\d+[smhwdy])\s*\])` + `parsePromQLDuration` loop from
   `metric_query.go`. Move `parsePromQLDuration` into `metric.go` (or keep in `helper.go`). Reject any
   subquery range > 7 days. (Behavior expansion: range mode now also rejects >7d subqueries — safer;
   previously only instant mode checked. Document in README.)
5. `switch params.Mode`:
   - **`instant`**: parse `params.Time` as RFC3339 if non-empty (else `time.Time{}` = now). Call
     `c.Query(ctx, params.Query, evalTime)`. Assert `model.Vector`. Build `[]MetricInstantOutput`,
     apply `filter.Match`, apply `params.Limit` (break when reached). Return `marshalOutputs`.
   - **`range`**: per-mode required-field check in code: return error if `Start`/`End`/`Step` empty.
     Default `MaxSamples` to 100 if 0. Parse `Start`/`End` as RFC3339, `Step` as `time.ParseDuration`.
     Enforce `step >= 15s` and `end.Sub(start) <= 7*24h` (preserve existing error messages). Call
     `c.QueryRange(ctx, params.Query, promapi.Range{Start, End, Step})`. Assert `model.Matrix`. Tail
     `values` to `MaxSamples`. Build `[]MetricRangeOutput`, apply `filter.Match`. Return
     `marshalOutputs`.
   - **default**: `errors.Errorf("unsupported mode %q", params.Mode)` (unreachable due to validate tag,
     but mirror alertmanager write's defensive default).

**Constructor** `NewMetricTool(ctx, configs)`: `newBaseTool`, `utils.InferTool("prometheus_metric",
description+listOutputGuidance, Invoke)`, embed `tool.InvokableTool`. No `validate.Struct(cfg)` needed
here (configs validated in `newBaseTool`/`BuildClients`); the AGENTS rule "every New… must call
validate.Struct after applying defaults" is satisfied at the client-builder layer
(`NewClient`/`NewAlertmanagerClient` already call `validate.Struct`). The tool constructor validates
params at Invoke time via `validateParams`.

**Preserved behaviors:** subquery >7d rejection, `parsePromQLDuration`, RFC3339 parsing, `filter`
regex, `limit` (1-50000, instant), `maxSamples` (1-10000, default 100, range), `step >= 15s`, range
window <= 7d, `marshalOutputs` + `listOutputGuidance`.

---

### 4.2 Prometheus — `alert.go` (new; STRAIGHT RENAME of `alertmanager_alert_list.go`)

**Tool name:** `prometheus_alert` (renamed from `prometheus_alertmanager_alert_list`)
**Description:** the existing `alertmanagerAlertListDescription` with the tool name updated. Reads
Alertmanager `/api/v2/alerts` only. No `source` discriminator, no `detail` flag.

This is a **pure rename** — the Invoke logic, validation, fingerprint precedence, pagination, error
wrapping, and output shape are identical to the existing `alertmanager_alert_list.go`. Only the type
names and tool name change. The `AlertTool` embeds `*alertmanagerBaseTool` only (no Prometheus
`baseTool` — it does not touch the Prometheus API).

```go
// AlertParams defines the parameters for reading alerts from Alertmanager
// (renamed from AlertmanagerAlertListParams). Fields identical.
type AlertParams struct {
    Instance    string         `json:"instance" validate:"required" jsonschema:"(required) The Prometheus instance to query (must have Alertmanager configured)."`
    Fingerprint string         `json:"fingerprint,omitempty" jsonschema:"(optional) If set, return only the alert with this fingerprint. Takes precedence over AlertFilter/State."`
    Filter      string         `json:"filter,omitempty" jsonschema:"(optional) Go RE2 regex applied on each alert JSON. Keep only alerts that match."`
    State       string         `json:"state,omitempty" validate:"omitempty,oneof=active unprocessed suppressed" jsonschema:"(optional) Filter by Alertmanager alert state: 'active', 'unprocessed', or 'suppressed'."`
    AlertFilter string         `json:"alertFilter,omitempty" jsonschema:"(optional) Alertmanager matcher string passed to the API, e.g. alertname=\"HighCPU\". Multiple matchers can be comma-separated."`
    Paginate    *AlertPaginate `json:"paginate,omitempty" jsonschema:"(optional) Pagination parameters."`
}

// AlertOutput is the structured output for an Alertmanager alert
// (renamed from AlertmanagerAlertListOutput). Fields identical.
type AlertOutput struct {
    Labels      model.LabelSet `json:"labels"`
    Annotations model.LabelSet `json:"annotations"`
    State       string         `json:"state"`
    StartsAt    string         `json:"startsAt"`
    EndsAt      string         `json:"endsAt"`
    Fingerprint string         `json:"fingerprint"`
    SilencedBy  []string       `json:"silencedBy"`
    Receivers   []string       `json:"receivers"`
}

// AlertTool is the eino tool for reading Alertmanager alerts
// (renamed from AlertmanagerAlertListTool). Embeds alertmanagerBaseTool only.
type AlertTool struct {
    *alertmanagerBaseTool
    tool.InvokableTool
}
```

**Invoke logic** (`func (t *AlertTool) Invoke(ctx, *AlertParams) (string, error)`): identical to the
existing `AlertmanagerAlertListTool.Invoke`:
1. If `Paginate != nil && Paginate.PageSize == 0`: default `PageSize` to 20.
2. `validateParams(params)`.
3. Compile `filter` regex via `filter.Compile` (wrap error with `errors.Wrap`).
4. Resolve client via `t.amClient(params.Instance)`.
5. Build `amListAlertsParams{Active: boolPtr(true), Silenced: boolPtr(false), Inhibited:
   boolPtr(false)}`.
6. If `Fingerprint != ""`: set `Silenced=true`, `Inhibited=true` (fetch all states for client-side
   fingerprint match). Ignore `AlertFilter`/`State`.
7. Else: if `State == "suppressed"`: set `Silenced=true`, `Inhibited=true`. Parse `AlertFilter`
   comma-separated matchers into `p.Filter`.
8. Call `c.ListAlerts(ctx, p)`.
9. If `Fingerprint != ""`: filter client-side by `a.Fingerprint == params.Fingerprint`.
10. Else if `State != ""`: filter client-side by `a.Status.State == params.State`.
11. `paginateWindow(params.Paginate, len(alerts))`.
12. Build `AlertOutput` per alert (format `StartsAt`/`EndsAt` as RFC3339, `receiverNames(a.Receivers)`).
13. Apply `filter.Match` on output JSON. Append. `nextPageToken` if more.
14. Return `marshalOutputs`.

**Constructor** `NewAlertTool(ctx, configs)`: `newAlertmanagerBaseTool(ctx, configs)`,
`utils.InferTool("prometheus_alert", description+listOutputGuidance, Invoke)`, embed
`tool.InvokableTool`.

**Preserved behaviors:** `fingerprint` precedence over `alertFilter`/`state`, state filter
(`active`/`unprocessed`/`suppressed`), `alertFilter` matcher parsing, suppressed-state fetches both
silenced+inhibited, regex `filter` on output JSON, pagination, `receiverNames` flattening, all error
wrapping with `emperror.dev/errors`.

**Note on `receiverNames`:** move `receiverNames` from `alertmanager_alert_list.go` into `alert.go`
(or `helper.go`) so it survives the file rename.

---

### 4.3 Prometheus — `alert_write.go` (new; STRAIGHT RENAME of `alertmanager_alert_write.go`)

**Tool name:** `prometheus_alert_write` (renamed from `prometheus_alertmanager_alert_write`)
**Description:** the existing `alertmanagerAlertWriteDescription` with the tool name updated. Explains
`operation` = create/update/delete, dry-run/confirmed gate, required labels including `alertname`.

This is a **pure rename** — the logic, validation, and edge cases are identical to the existing
`alertmanager_alert_write.go`. Only the type names and tool name change.

```go
// AlertWriteParams (renamed from AlertmanagerAlertWriteParams) — fields identical.
type AlertWriteParams struct {
    Instance     string            `json:"instance" validate:"required" jsonschema:"(required) The Prometheus instance (must have Alertmanager configured)."`
    Operation    string            `json:"operation" validate:"required,oneof=create update delete" jsonschema:"(required) Operation to perform: 'create', 'update', or 'delete'."`
    Labels       map[string]string `json:"labels" validate:"required,min=1,dive,keys,required,endkeys,required" jsonschema:"(required) Alert labels as key/value pairs. Must include 'alertname'."`
    Annotations  map[string]string `json:"annotations,omitempty" jsonschema:"(optional) Alert annotations."`
    StartsAt     string            `json:"startsAt,omitempty" jsonschema:"(optional) Start time in RFC3339."`
    EndsAt       string            `json:"endsAt,omitempty" jsonschema:"(optional) End time in RFC3339."`
    GeneratorURL string            `json:"generatorURL,omitempty" validate:"omitempty,url" jsonschema:"(optional) URL of the source that generated the alert."`
    DryRun       bool              `json:"dryRun,omitempty" jsonschema:"(optional) If true, preview without posting."`
    Confirmed    bool              `json:"confirmed,omitempty" jsonschema:"(optional) Must be true to actually post."`
}

// AlertWriteOutput (renamed from AlertmanagerAlertWriteOutput) — fields identical.
type AlertWriteOutput struct {
    Status      string `json:"status"`
    Action      string `json:"action"`
    Fingerprint string `json:"fingerprint,omitempty"`
    EndsAt      string `json:"endsAt,omitempty"`
}

// AlertWriteTool (renamed from AlertmanagerAlertWriteTool).
type AlertWriteTool struct {
    *alertmanagerBaseTool
    tool.InvokableTool
}
```

**Invoke logic:** identical to the existing `AlertmanagerAlertWriteTool.Invoke` — `validateParams`,
`confirm.RequireConfirmation(params.DryRun, params.Confirmed)`, require `labels["alertname"]`,
`toLabelSet`, `generatorURL` http/https check, `switch params.Operation` (create/update/delete with
the exact same semantics: create defaults now/now+5m, update fetches-by-matcher-merges-reposts, delete
reposts with endsAt=now). Keep `buildMatcherFilter`, `validateMatcherLabelKeys`, `coalesceTime`,
`postAlert` helpers (move into `alert_write.go` or `helper.go`).

**Constructor** `NewAlertWriteTool(ctx, configs)`: `newAlertmanagerBaseTool`,
`utils.InferTool(alertWriteToolName, alertWriteToolDescription, Invoke)`, embed `tool.InvokableTool`.

**Preserved behaviors (ALL edge cases):** matcher escaping (`\` and `"`), label-key validation via
`model.LabelNameRE`, `endsAt` must be future for create, `endsAt` after `startsAt`, update requires
existing match (error if none), update merges annotations/startsAt/endsAt/generatorURL, delete is
idempotent resolve (startsAt=now-1m, endsAt=now), dry-run preview payloads, confirmation gate.

---

### 4.4 Prometheus — `helper.go` (update)

The pagination helpers `alertPaginateToken`, `paginateWindow`, `nextPageToken` currently live in
`helper.go` and reference `AlertListPaginate` (defined in `alert_list.go`). After `alert_list.go` is
deleted, `alertmanager_alert_list.go` (now `alert.go` / `prometheus_alert`) is the sole user.

- **Move `AlertListPaginate` from `alert_list.go` into `helper.go`** and **rename it to
  `AlertPaginate`** (avoids the `AlertList*` naming from the deleted Prometheus-native reader; matches
  the new `AlertParams.Paginate` field type).
- **Move `alertPaginateToken` from `alert_list.go` into `helper.go`** (keep the name — it is the
  internal pagination token struct, no collision).
- **Update `paginateWindow` signature** in `helper.go`:
  ```go
  // paginateWindow computes the [start, end) slice window for client-side index
  // pagination of alert listings.
  func paginateWindow(paginate *AlertPaginate, total int) (start, end int, err error) {
      // ... existing body unchanged (uses alertPaginateToken) ...
  }
  ```
- Keep `nextPageToken`, `marshalOutputs`, `marshalString`, `instanceNotFoundError`, `validateParams`,
  `listOutputGuidance`, `describeOutputGuidance` unchanged.
- `parsePromQLDuration` currently lives in `metric_query.go`; move it to `helper.go` (or keep in
  `metric.go`) so it survives the file deletion. Prefer `helper.go` for reuse.
- Move `receiverNames` from `alertmanager_alert_list.go` into `helper.go` (or `alert.go`) so it
  survives the file rename.
- Move `buildMatcherFilter`, `validateMatcherLabelKeys`, `coalesceTime` from
  `alertmanager_alert_write.go` into `helper.go` (or keep in `alert_write.go`).

This precisely avoids any dangling reference: after the deletion, `AlertPaginate` and
`alertPaginateToken` are defined in `helper.go`, `paginateWindow`/`nextPageToken` reference
`AlertPaginate`/`alertPaginateToken` from the same package, and `alert.go`'s `AlertParams.Paginate`
field uses `*AlertPaginate`.

---

### 4.5 Prometheus — `alertmanager_base.go` (update)
Update the tool-name constants:
```go
// Alertmanager tool names, shared across constructors, registry and check.
const (
    // alertmanagerAlertListToolName removed — folded into prometheus_alert.
    alertWriteToolName = "prometheus_alert_write" // renamed from alertmanagerAlertWriteToolName
)
```
Keep `alertmanagerBaseTool`, `amClient`, `newAlertmanagerBaseTool` unchanged (still used by
`AlertTool` and `AlertWriteTool`).

---

### 4.6 Prometheus — `registry.go` (update)
```go
var readOnlyConstructors = []toolConstructor{
    func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewInstanceListTool(ctx, c) },
    func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewMetricTool(ctx, c) },
    func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewAlertTool(ctx, c) },
    func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewTargetListTool(ctx, c) },
}

var writeConstructors = []toolConstructor{
    func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewAlertWriteTool(ctx, c) },
}

func WriteToolNames() []string {
    return []string{alertWriteToolName}
}

var (
    _ tool.InvokableTool = (*InstanceListTool)(nil)
    _ tool.InvokableTool = (*MetricTool)(nil)
    _ tool.InvokableTool = (*AlertTool)(nil)
    _ tool.InvokableTool = (*TargetListTool)(nil)
    _ tool.InvokableTool = (*AlertWriteTool)(nil)
)
```
`NewAllTools`, `NewReadOnlyTools`, `ExtractWriteToolNames`, `NewAllToolsWithSafety` signatures
unchanged.

---

### 4.7 Prometheus — `check.go` (update)

Since `prometheus_alert` now reads Alertmanager only (no Prometheus `/api/v1/alerts` probe), the
Prometheus-client probes and Alertmanager-client probes are cleanly separated:

- **`clientErrorResults(instance, err)`** returns **3** results (was 6) for the Prometheus-client
  components: `prometheus_instance_list`, `prometheus_metric`, `prometheus_target_list`.
- **`alertmanagerClientErrorResults(instance, err)`** returns **2** results for the Alertmanager-client
  components: `prometheus_alert`, `prometheus_alert_write`.
- **`Check` per-instance flow:**
  1. Build Prometheus client. If error: append `clientErrorResults` (3 results). Else: append
     `probeInstance` (3 results).
  2. Build Alertmanager client if `cfg.Alertmanager != nil`.
     - If build failed: append `alertmanagerClientErrorResults` (2 results, `StatusError`).
     - Else: append `probeAlert` (1 result, real `ListAlerts` GET probe for `prometheus_alert`) +
       `prometheus_alert_write` result (`StatusLimited` "write tool, not probed to avoid side
       effects").
  3. If `cfg.Alertmanager == nil`: append 2 `StatusLimited` results for `prometheus_alert` and
     `prometheus_alert_write` with message "alertmanager not configured for this instance".
- **`probeInstance(ctx, client, instance)`** returns 3 results:
  - `probeInstanceList` (OK).
  - `probeMetric` (one instant `up` query — sufficient for RBAC probe; do NOT probe range mode to
    keep the check cheap). Rename `probeMetricQuery` → `probeMetric`.
  - `probeTargetList`.
  - Remove `probeAlertList`, `probeAlertDescribe`, `probeMetricRange` (Prometheus-native alert
    readers deleted; range probe dropped to keep the check cheap — instant `up` is sufficient).
- **`probeAlert(ctx, amClient, instance)`** returns 1 result for `prometheus_alert`:
  - Call `amClient.ListAlerts(ctx, &amListAlertsParams{Active: boolPtr(true)})`.
  - On error: `StatusError` "failed to list Alertmanager alerts: <err>".
  - On success: `StatusOK` "%d alerts found, RBAC ok".
- Remove `probeAlertmanagerInstance`, `probeAlertmanagerAlertList` (folded into `probeAlert` and the
  `prometheus_alert_write` limited result).
- Component-name strings in results: `prometheus_instance_list`, `prometheus_metric`,
  `prometheus_target_list`, `prometheus_alert`, `prometheus_alert_write`.

---

### 4.8 Prometheus — `check_test.go` (update)
- `TestCheckInvalidInstance`: `len(results) != 8` → `!= 5` (3 prometheus error + 2 alertmanager
  limited). Update the switch over `r.Component`: `prometheus_alert` and `prometheus_alert_write` →
  `StatusLimited` (alertmanager not configured for the bad instance); the default-error case now
  covers `prometheus_instance_list`, `prometheus_metric`, `prometheus_target_list`.
- `TestCheckClientErrorResults`: `len(r) != 6` → `!= 3`.
- `TestAlertmanagerClientErrorResults`: `len(r) != 2` (unchanged count, but components change).
  Update the component checks: expect `prometheus_alert` and `prometheus_alert_write` (not
  `prometheus_alertmanager_alert_list`/`_write`).
- Other tests (`TestCheckEmptyConfigs`, `TestCheckNilConfigs`, `TestCheckResultStatuses`) unchanged.

---

### 4.9 Grafana — `dashboard.go` (new; replaces `dashboard_search.go` + `dashboard_describe.go`)

**Tool name:** `grafana_dashboard`
**Description:** merged, explaining that `uid` set → describe (single object), `uid` empty → search
(array).

```go
type DashboardParams struct {
    Instance            string           `json:"instance" validate:"required" jsonschema:"(required) The Grafana instance to connect to."`
    UID                 string           `json:"uid,omitempty" jsonschema:"(optional) If set, return the full dashboard with this UID (describe mode, single object). If empty, search dashboards (list mode, array)."`
    // search-mode fields (ignored when UID is set)
    Query               string           `json:"query,omitempty" jsonschema:"(optional, search mode) Title search query."`
    Type                string           `json:"type,omitempty" validate:"omitempty,oneof=dash-db dash-folder" jsonschema:"(optional, search mode) Filter by type."`
    Tags                []string         `json:"tags,omitempty" jsonschema:"(optional, search mode) Filter by tags (ALL must match)."`
    FolderUIDs          []string         `json:"folderUIDs,omitempty" jsonschema:"(optional, search mode) Filter by folder UIDs."`
    Sort                string           `json:"sort,omitempty" validate:"omitempty,oneof=alpha_asc alpha_desc created_asc created_desc updated_asc updated_desc" jsonschema:"(optional, search mode) Sort order."`
    Filter              string           `json:"filter,omitempty" jsonschema:"(optional, search mode) Go RE2 regex on each dashboard search output JSON."`
    Paginate            *DashboardPaginate `json:"paginate,omitempty" jsonschema:"(optional, search mode) Pagination."`
    // describe-mode fields (ignored when UID is empty)
    ExcludeFieldsOutput []string         `json:"excludeFieldsOutput,omitempty" validate:"omitempty,dive,oneof=meta panels templating time annotations schemaVersion version" jsonschema:"(optional, describe mode) Fields to exclude from the dashboard output."`
}

type DashboardPaginate struct { // renamed from DashboardSearchPaginate
    PageSize int `json:"pageSize,omitempty" validate:"omitempty,min=1,max=5000"`
    Page     int `json:"page,omitempty" validate:"omitempty,min=1"`
}

// DashboardSearchOutput, DashboardDescribeOutput — unchanged (kept as-is).

type DashboardTool struct {
    *baseTool
    tool.InvokableTool
}
```

**Invoke logic:**
1. `validateParams(params)` (applies defaults: `Paginate.PageSize=100`, `Paginate.Page=1` when
   `Paginate != nil`).
2. Resolve client.
3. `if params.UID != ""` (describe mode — port `DashboardDescribeTool.Invoke`):
   - `c.GetDashboard(ctx, params.UID)`, unmarshal `dashboardResponse`.
   - Build `DashboardDescribeOutput{Dashboard, Meta}`.
   - `applyExcludes(params.ExcludeFieldsOutput, setters)` (same setter map as today).
   - Marshal, return single JSON object.
4. `else` (search mode — port `DashboardSearchTool.Invoke`):
   - Compile `filter` regex.
   - Build `searchParams` from `Query/Type/Tags/FolderUIDs/Sort` + `Paginate.PageSize/Page`.
   - `c.SearchDashboards`, unmarshal `[]searchHit`.
   - `filterMapMarshal(hits, re, toDashboardSearchOutput)` (map to `DashboardSearchOutput` with
     `URL: c.baseURL + item.URL`).
   - Return JSON array.

**Preserved behaviors:** filter regex, pagination via `searchParams` (limit/page), `excludeFieldsOutput`
+ `applyExcludes`, `url.PathEscape` for UID (in `GetDashboard`), full URL construction
(`c.baseURL + item.URL`).

---

### 4.10 Grafana — `dashboard_write.go` (new; replaces `dashboard_build.go`)

**Tool name:** `grafana_dashboard_write`
**Description:** explains `operation` = create/update/delete, dry-run/confirmed gate, dashboard
protection (applies to update AND delete), and that create/update both POST to `/api/dashboards/db`
(Grafana upsert semantics).

```go
type DashboardWriteParams struct {
    Instance   string `json:"instance" validate:"required" jsonschema:"(required) The Grafana instance to connect to."`
    Operation  string `json:"operation" validate:"required,oneof=create update delete" jsonschema:"(required) Operation: 'create', 'update', or 'delete'."`
    Dashboard  string `json:"dashboard,omitempty" jsonschema:"(optional, create/update) The full Grafana dashboard model as a JSON string. Must include 'title'. Include 'uid' to target an existing dashboard (update). Ignored for delete."`
    UID        string `json:"uid,omitempty" jsonschema:"(optional, delete/update by UID) For delete: the dashboard UID to delete. For update: may be provided here instead of inside the dashboard model. Ignored for create."`
    FolderUID  string `json:"folderUID,omitempty" jsonschema:"(optional, create/update) Folder UID to place the dashboard in."`
    Message    string `json:"message,omitempty" jsonschema:"(optional, create/update) Commit message for the version."`
    Overwrite  bool   `json:"overwrite,omitempty" jsonschema:"(optional, create/update) Overwrite without version checking."`
    DryRun     bool   `json:"dryRun,omitempty" jsonschema:"(optional) Preview without saving/deleting."`
    Confirmed  bool   `json:"confirmed,omitempty" jsonschema:"(optional) Must be true to execute."`
}

type DashboardSaveOutput struct { // renamed from DashboardBuildOutput
    UID     string `json:"uid"`
    URL     string `json:"url"`
    Status  string `json:"status"`
    Version int    `json:"version"`
    Slug    string `json:"slug"`
}

type DashboardDeleteOutput struct {
    UID     string `json:"uid"`
    Title   string `json:"title"`
    Message string `json:"message"`
    Status  string `json:"status"`
}

type DashboardWriteTool struct {
    *baseTool
    tool.InvokableTool
}
```

**Invoke logic:**
1. `validateParams(params)`.
2. `confirm.RequireConfirmation(params.DryRun, params.Confirmed)`.
3. Resolve client.
4. `switch params.Operation`:
   - **`create`** and **`update`** (share the save path; mirror alertmanager write's coalescing):
     - Require `Dashboard` non-empty (code-level). Unmarshal into `map[string]any`.
     - Require `title` non-empty.
     - Determine `uid`: for update, prefer `params.UID` if set, else `dashboardModel["uid"]`. For
       create, `uid = dashboardModel["uid"]` (may be empty for new dashboards).
     - If `uid != ""`: `checkProtected(ctx, instance, uid)` (fetch existing, blocklist check; 404 →
       not protected).
     - `checkProtectedModel(instance, dashboardModel, params.FolderUID)` (defense-in-depth).
     - Dry-run: return `marshalString(map[string]any{"dryRun": true, "operation": params.Operation,
       "dashboard": dashboardModel, "folderUID": params.FolderUID, "overwrite": params.Overwrite})`.
     - Build `saveDashboardRequest{Dashboard, FolderUID, Message, Overwrite}`, marshal, call
       `c.SaveDashboard`. Unmarshal `saveDashboardResponse`, build `DashboardSaveOutput{UID, URL:
       c.baseURL+resp.URL, Status, Version, Slug}`, return.
   - **`delete`**:
     - Require `params.UID` non-empty (code-level).
     - `checkProtected(ctx, instance, params.UID)` (must fetch existing to (a) confirm it exists and
       (b) blocklist-check before deleting; 404 → return a clear not-found error, NOT silent success).
     - Dry-run: return `marshalString(map[string]any{"dryRun": true, "operation": "delete", "uid":
       params.UID})`.
     - Call `c.DeleteDashboard(ctx, params.UID)`. Unmarshal `deleteDashboardResponse`. Build
       `DashboardDeleteOutput{UID: params.UID, Title: resp.Title, Message: resp.Message, Status:
       "success"}`, return.
   - **default**: `errors.Errorf("unsupported operation %q", params.Operation)`.

**Preserved behaviors:** `checkProtected`, `checkProtectedModel`, `dashboardProtection` blocklist
(UID/title-prefix/folder/tag) for update AND delete; `dryRun`/`confirmed` gate on all ops;
`saveDashboardRequest`/`saveDashboardResponse` wire types; full URL construction.

**New behavior:** delete requires the existing dashboard to be fetched first (for protection check);
a 404 on that fetch is surfaced as a not-found error (do NOT proceed to DELETE a non-existent UID
silently — avoids confusing "deleted" success for typos).

---

### 4.11 Grafana — `datasource.go` (new; replaces `datasource_list.go` + `datasource_describe.go`)

**Tool name:** `grafana_datasource`
**Description:** merged, explaining `uid` set → describe (single object, secrets redacted), `uid` empty
→ list (array, secrets redacted). Data sources are READ-ONLY (no write tool).

```go
type DataSourceParams struct {
    Instance string `json:"instance" validate:"required" jsonschema:"(required) The Grafana instance to connect to."`
    UID      string `json:"uid,omitempty" jsonschema:"(optional) If set, return the full data source with this UID (describe mode, single object). If empty, list all data sources (array)."`
    Filter   string `json:"filter,omitempty" jsonschema:"(optional, list mode) Go RE2 regex on each data source list output JSON."`
}

// DataSourceListOutput, DataSourceDescribeOutput — unchanged.

type DataSourceTool struct {
    *baseTool
    tool.InvokableTool
}
```

**Invoke logic:**
1. `validateParams(params)`.
2. Resolve client.
3. `if params.UID != ""` (describe — port `DataSourceDescribeTool.Invoke`):
   - `c.GetDataSource(ctx, params.UID)`. On 404, `errors.Wrapf(err, "data source with UID %q not
     found", params.UID)`.
   - Unmarshal `dataSource`, `ds.toDescribeOutput()`, marshal, return single object.
4. `else` (list — port `DataSourceListTool.Invoke`):
   - Compile `filter` regex.
   - `c.ListDataSources`, unmarshal `[]dataSource`.
   - `filterMapMarshal(sources, re, dataSource.toListOutput)`, return array.

**Preserved behaviors:** secret redaction (`dataSource.toListOutput`/`toDescribeOutput`,
`redactedJSONData`, top-level secret fields dropped via wire-type omission), `url.PathEscape` for UID
(in `GetDataSource`), 404 → clear not-found error.

---

### 4.12 Grafana — `client.go` (add new method + wire type)

Add to the wire-types section:
```go
// deleteDashboardResponse is the DELETE /api/dashboards/uid/:uid response.
type deleteDashboardResponse struct {
    Title   string `json:"title"`
    Message string `json:"message"`
    ID      int64  `json:"id"`
}
```

Add API method (path-escapes the UID; reuses `doRequest`):
```go
// DeleteDashboard calls DELETE /api/dashboards/uid/:uid.
func (c *grafanaClient) DeleteDashboard(ctx context.Context, uid string) ([]byte, error) {
    path := "/api/dashboards/uid/" + url.PathEscape(uid)
    body, _, err := c.doRequest(ctx, http.MethodDelete, path, nil)
    if err != nil {
        return nil, errors.Wrap(err, "failed to delete dashboard")
    }
    return body, nil
}
```

No new dependencies (`net/http`, `net/url` already imported). `doRequest` already handles
timeouts, Bearer auth, `*httpError` for status >= 400, body truncation.

### API contract for the new write endpoint
| Endpoint | Method | Request body | Success response | Status codes |
|---|---|---|---|---|
| `/api/dashboards/uid/:uid` | DELETE | none | `{"title": "...", "message": "Dashboard deleted", "id": <int>}` | 200, 401, 403, 404 |

---

### 4.13 Grafana — `registry.go` (update)
```go
var readOnlyConstructors = []toolConstructor{
    func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewInstanceListTool(ctx, c) },
    func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewDashboardTool(ctx, c) },
    func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewDataSourceTool(ctx, c) },
}

var writeConstructors = []toolConstructor{
    func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewDashboardWriteTool(ctx, c) },
}

func WriteToolNames() []string {
    return []string{dashboardWriteToolName}
}

var (
    _ tool.InvokableTool = (*InstanceListTool)(nil)
    _ tool.InvokableTool = (*DashboardTool)(nil)
    _ tool.InvokableTool = (*DashboardWriteTool)(nil)
    _ tool.InvokableTool = (*DataSourceTool)(nil)
)
```
Add tool-name constant (mirroring prometheus `alertmanager_base.go`):
```go
const (
    dashboardWriteToolName = "grafana_dashboard_write"
)
```
(prefer placing this const in `base.go` or a new `toolnames.go` to match the prometheus pattern;
`registry.go` references it). `NewAllTools`/`NewReadOnlyTools`/`ExtractWriteToolNames`/
`NewAllToolsWithSafety` signatures unchanged.

---

### 4.14 Grafana — `check.go` (update)
- `allComponentNames()`: return **4** names: `grafana_instance_list`, `grafana_dashboard`,
  `grafana_dashboard_write`, `grafana_datasource`.
- `probeInstance`:
  - `grafana_instance_list` → OK.
  - `grafana_dashboard` → `probeDashboard` (search with `Limit:1`; if first UID found, also GET it to
    probe describe path; single result line for the consolidated tool, message `"%d dashboards found,
    describe ok, RBAC ok"`).
  - `grafana_dashboard_write` → `StatusLimited` "write tool, not probed to avoid side effects".
  - `grafana_datasource` → `probeDataSource` (list; if first UID found, GET it; one result line for
    `grafana_datasource`).
- Remove `probeSearch`/`probeDescribe`/`probeDataSourceList`/`probeDataSourceDescribe` split; collapse
  into `probeDashboard`/`probeDataSource` each returning a single `checkup.Result` (component name =
  the consolidated tool name).

---

### 4.15 Grafana — `check_test.go` (update)
- `TestCheckInvalidInstance`: `len(results) != 6` → `!= 4`.
- `TestCheckClientErrorResults`: `len(r) != 6` → `!= 4`.
- `TestAllComponentNames`: `len(names) != 6` → `!= 4`.
- Other tests unchanged.

---

## 5. Tests

### Prometheus (new + updated)
- **`metric_test.go`** (new): table-driven, using a `mockAPI` embedding `v1.API` (pattern from
  `target_list_test.go`). Cases: instant happy path (vector → `MetricInstantOutput`), instant with
  `time`/`limit`/`filter`, range happy path (matrix → `MetricRangeOutput`), range with `maxSamples`
  tail-truncation, range `step < 15s` error, range window > 7d error, subquery > 7d rejection (both
  modes), invalid `mode` (validate error), missing `query` (validate error), range missing `start`
  (code-level error), unknown instance error, invalid filter regex error.
- **`alert_test.go`** (new, RENAME of `alertmanager_alert_list_test.go`): alertmanager httptest mock.
  Cases (all preserved from the renamed file): list with `state` filter (`active`/`unprocessed`/
  `suppressed`), `alertFilter` matcher, `filter` on output JSON, pagination (page size + token),
  get-single via `fingerprint` (precedence over `alertFilter`/`state`), unknown instance, invalid
  filter regex, invalid `state` (validate error). Update type/constructor references:
  `AlertmanagerAlertListParams` → `AlertParams`, `AlertmanagerAlertListOutput` → `AlertOutput`,
  `AlertListPaginate` → `AlertPaginate`, `NewAlertmanagerAlertListTool` → `NewAlertTool`,
  `AlertmanagerAlertListTool` → `AlertTool`.
- **`alert_write_test.go`** (new, RENAME of `alertmanager_alert_write_test.go`): all existing cases
  preserved with renamed types/constructors. Cases: create dry-run, create confirmed, create
  with `endsAt` in past (error), update dry-run (existing + merged), update confirmed, update no
  match (error), delete dry-run, delete confirmed, no confirmation (error), missing `alertname`
  (error), invalid label key (error), invalid `operation` (validate error), `generatorURL` non-http
  (error). Update references: `AlertmanagerAlertWriteParams` → `AlertWriteParams`,
  `AlertmanagerAlertWriteOutput` → `AlertWriteOutput`, `NewAlertmanagerAlertWriteTool` →
  `NewAlertWriteTool`, `AlertmanagerAlertWriteTool` → `AlertWriteTool`.
- **`prometheus_test.go`** (update): remove `AlertListParams`/`AlertListPaginate`/
  `AlertDescribeParams`/`AlertmanagerAlertListParams`/`AlertmanagerAlertWriteParams` references.
  Replace with `AlertParams`/`AlertPaginate`/`AlertWriteParams` in the `validateParams` tests where
  applicable. Remove any test cases that exercised the deleted Prometheus-native alert readers.
- **`check_test.go`** (update): per §4.8.
- **`alertmanager_client_test.go`** (unchanged): client-level tests stay as-is.
- Existing `instance_list_test.go`, `target_list_test.go` unchanged.
- **Delete** `alertmanager_alert_list_test.go` (renamed to `alert_test.go`),
  `alertmanager_alert_write_test.go` (renamed to `alert_write_test.go`). Do NOT create
  `alert_list_test.go` or `alert_describe_test.go` (those tools are removed).

### Grafana (new + updated)
- **`dashboard_test.go`** (new, replaces dashboard search/describe tests): httptest, cases: search
  all, search by query/type/tags/folder/sort, search with filter, search with pagination, describe
  by UID, describe with `excludeFieldsOutput`, describe nonexistent UID (404 → error), unknown
  instance, invalid filter regex, invalid exclude field.
- **`dashboard_write_test.go`** (new, replaces dashboard build tests): cases: create dry-run,
  create confirmed, update existing (with `overwrite`), update protected by UID (error), create
  blocked by protected title prefix (error), delete dry-run, delete confirmed, delete protected
  (error), delete nonexistent (404 → error), no confirmation (error), missing title (create/update
  error), missing `dashboard` (create/update error), missing `uid` (delete error), invalid JSON,
  unknown instance, invalid `operation` (validate error).
- **`datasource_test.go`** (new, replaces datasource list/describe tests): cases: list all (secrets
  redacted), list with filter, describe by UID (secrets redacted), describe nonexistent (404 → error),
  unknown instance, invalid filter regex.
- **`suite_test.go`** (update): add handler for `DELETE /api/dashboards/uid/:uid` (return
  `{"title":"...","message":"Dashboard deleted","id":1}`). Keep existing handlers. No datasource
  POST/PUT/DELETE handlers (datasource write is out of scope).
- **`grafana_test.go`** (update): rename `TestDashboardSearch`/`TestDashboardDescribe` →
  `TestDashboard`; `TestDashboardBuild` → `TestDashboardWrite`; `TestDataSourceList`/
  `TestDataSourceDescribe` → `TestDataSource`. Update constructor calls and param struct names.
- **`security_test.go`** (update): add path-escape test for `DeleteDashboard` (mirror
  `TestGetDashboardPathEscape`/`TestGetDataSourcePathEscape`). Existing redaction tests unchanged.
  No datasource write path-escape tests (datasource write is out of scope).
- **`integration_test.go`** (update): rename tool constructors/param structs; add integration case
  for dashboard delete (gated behind `GRAFANA_TOKEN`). No datasource create/update/delete integration
  cases (out of scope).
- **`datasource_list_test.go`**, **`datasource_describe_test.go`** (delete): consolidated into
  `datasource_test.go`.

---

## 6. README Updates

### Prometheus `README.md`
- Update the "Available Tools" table:
  - Remove `prometheus_metric_query`, `prometheus_metric_range`, `prometheus_alert_list`,
    `prometheus_alert_describe`, `prometheus_alertmanager_alert_list`,
    `prometheus_alertmanager_alert_write`.
  - Add `prometheus_metric` (read), `prometheus_alert` (read, Alertmanager alerts — get via
    `fingerprint` / list), `prometheus_alert_write` (write).
- Update "Tool Details": replace the four metric/alert subsections + two alertmanager subsections with:
  - `prometheus_metric` (mode discriminator, instant vs range fields, subquery >7d rejection now
    applies to both modes).
  - `prometheus_alert` (Alertmanager alerts only; `fingerprint` for get-single, `state`/`alertFilter`
    for list; `filter` regex on output JSON; pagination).
  - `prometheus_alert_write` (operation discriminator, dry-run/confirmed gate, create/update/delete
    semantics).
- Update "Factory Functions" example: `WriteToolNames()` → `["prometheus_alert_write"]`.
- Add a "Breaking changes" subsection listing the removed tool names/constructors (per §2):
  `prometheus_metric_query`, `prometheus_metric_range`, `prometheus_alert_list`,
  `prometheus_alert_describe` (removed); `prometheus_alertmanager_alert_list` → `prometheus_alert`,
  `prometheus_alertmanager_alert_write` → `prometheus_alert_write` (renamed).

### Grafana `README.md`
- Update the "Available Tools" table:
  - Remove `grafana_dashboard_search`, `grafana_dashboard_describe`, `grafana_dashboard_build`,
    `grafana_datasource_list`, `grafana_datasource_describe`.
  - Add `grafana_dashboard` (read), `grafana_dashboard_write` (write), `grafana_datasource` (read).
  - Note that `grafana_datasource` is read-only (no write tool — data source writes are not in LLM
    scope).
- Update "Tool Details" with the three new tools' param tables (operation discriminator for dashboard
  write, uid discriminator for dashboard/datasource read, dry-run/confirmed, protection applies to
  update+delete).
- Update "Factory Functions": `WriteToolNames()` → `["grafana_dashboard_write"]`.
- Update "Usage Example" with new constructor names.
- Add a "Breaking changes" subsection listing the removed tool names/constructors (per §2).

---

## 7. Ordered Implementation Checklist

### Prometheus
1. Move `parsePromQLDuration` from `metric_query.go` to `helper.go` (so it survives file deletion).
2. Move `receiverNames` from `alertmanager_alert_list.go` to `helper.go` (so it survives file
   rename).
3. Move `buildMatcherFilter`, `validateMatcherLabelKeys`, `coalesceTime` from
   `alertmanager_alert_write.go` to `helper.go` (or keep in `alert_write.go`).
4. Move `AlertListPaginate` and `alertPaginateToken` from `alert_list.go` into `helper.go`; rename
   `AlertListPaginate` → `AlertPaginate`. Update `paginateWindow` signature to take
   `*AlertPaginate`. (This must happen before deleting `alert_list.go` to avoid a dangling
   reference.)
5. Create `metric.go` with `MetricParams`/`MetricInstantOutput`/`MetricRangeOutput`/`MetricTool` +
   `Invoke` (switch on `mode`) + `NewMetricTool`. Reuse `parsePromQLDuration`, `filter.Compile`,
   `marshalOutputs`, `listOutputGuidance`.
6. Create `alert.go` with `AlertParams`/`AlertOutput`/`AlertTool` (embeds `*alertmanagerBaseTool`
   only) + `Invoke` (identical to the existing `AlertmanagerAlertListTool.Invoke`) + `NewAlertTool`.
   Reuse `paginateWindow`, `nextPageToken`, `receiverNames`. Tool name `prometheus_alert`.
7. Create `alert_write.go` with `AlertWriteParams`/`AlertWriteOutput`/`AlertWriteTool` + `Invoke`
   (switch on `operation`, identical to the existing `AlertmanagerAlertWriteTool.Invoke`) +
   `NewAlertWriteTool`. Reuse `buildMatcherFilter`, `validateMatcherLabelKeys`, `coalesceTime`,
   `postAlert`. Tool name `prometheus_alert_write`.
8. Update `alertmanager_base.go`: remove `alertmanagerAlertListToolName`, rename
   `alertmanagerAlertWriteToolName` → `alertWriteToolName` = `"prometheus_alert_write"`.
9. Delete `metric_query.go`, `metric_range.go`, `alert_list.go`, `alert_describe.go`,
   `alertmanager_alert_list.go`, `alertmanager_alert_write.go`.
10. Update `registry.go` (readOnlyConstructors 4, writeConstructors 1, WriteToolNames, `var _` per
    §4.6).
11. Update `check.go` (clientErrorResults 3, alertmanagerClientErrorResults 2, probeInstance 3,
    probeAlert 1, remove `probeAlertList`/`probeAlertDescribe`/`probeMetricRange`/
    `probeAlertmanagerInstance`/`probeAlertmanagerAlertList` per §4.7).
12. Update `check_test.go` (counts 5, 3, 2 per §4.8).
13. Update `prometheus_test.go` (AlertParams/AlertPaginate/AlertWriteParams references; remove
    deleted-reader test cases).
14. Add `metric_test.go`, `alert_test.go`, `alert_write_test.go`.
15. Delete `alertmanager_alert_list_test.go` (renamed to `alert_test.go`);
    rename `alertmanager_alert_write_test.go` → `alert_write_test.go`. Do NOT create
    `alert_list_test.go`/`alert_describe_test.go`.
16. Update `README.md`.

### Grafana
1. Add wire type `deleteDashboardResponse` + client method `DeleteDashboard` to `client.go`
   (per §4.12).
2. Add tool-name const `dashboardWriteToolName` — place in `base.go` or a new `toolnames.go`.
3. Create `dashboard.go` (`DashboardTool`, `DashboardParams`, `DashboardPaginate`, consolidated
   Invoke) per §4.9.
4. Create `dashboard_write.go` (`DashboardWriteTool`, `DashboardWriteParams`, `DashboardSaveOutput`,
   `DashboardDeleteOutput`, switch on operation) per §4.10.
5. Create `datasource.go` (`DataSourceTool`, `DataSourceParams`, consolidated Invoke) per §4.11.
6. Delete `dashboard_build.go`, `dashboard_search.go`, `dashboard_describe.go`,
   `datasource_list.go`, `datasource_describe.go`.
7. Update `registry.go` (readOnlyConstructors, writeConstructors, WriteToolNames, `var _` per §4.13).
8. Update `check.go` (allComponentNames 4, probeInstance, collapse probes per §4.14).
9. Update `check_test.go` (counts 4 per §4.15).
10. Update `suite_test.go` (add DELETE dashboard handler).
11. Update `grafana_test.go` (rename tests/constructors/params).
12. Update `security_test.go` (add path-escape test for `DeleteDashboard`).
13. Delete `datasource_list_test.go`, `datasource_describe_test.go`; create `dashboard_test.go`,
    `dashboard_write_test.go`, `datasource_test.go`.
14. Update `integration_test.go` (rename + add dashboard delete integration case).
15. Update `README.md`.

---

## 8. Verification Commands

```bash
# Build everything
go build ./...

# Vet the two components
go vet ./components/tool/prometheus/... ./components/tool/grafana/...

# Unit tests for both components
go test ./components/tool/prometheus/... ./components/tool/grafana/...

# (optional) Grafana integration tests — requires GRAFANA_URL + GRAFANA_TOKEN
go test -tags=integration ./components/tool/grafana/...
```

Expected: all green. No new dependencies in `go.mod`. `go vet` clean (naming: `URL`/`UID`/`API`/`HTTP`
casing preserved; `redactScrapeURL`/`ScrapeUrl` field naming unchanged to avoid churn).

---

## 9. Key Decisions Summary

1. **Backward compatibility:** remove old tools/constructors entirely (no deprecated wrappers);
   document all removed symbols in §2 and in each README's "Breaking changes" subsection.
2. **Discriminators:** metric `mode` (instant|range, required, validate oneof); alert read uses
   `fingerprint` (set → get-single, empty → list — no `source`/`detail` discriminator, Alertmanager
   only); grafana dashboard/datasource read uses `uid` (set → describe, empty → list); grafana
   dashboard write and prometheus alert write use `operation` (create|update|delete, required,
   validate oneof).
3. **Merged param structs:** one `MetricParams` with mode-specific required-field checks in code
   (range: start/end/step required; instant: time/limit optional); `AlertParams` is a straight rename
   of `AlertmanagerAlertListParams` (fields identical, Alertmanager-only); one `DashboardParams` with
   uid-gated search/describe fields; one `DashboardWriteParams`/`AlertWriteParams` with
   operation-gated fields. Mirror the alertmanager write `switch` pattern.
4. **Alert collapse:** the three existing alert readers (`alert_list.go`, `alert_describe.go`,
   `alertmanager_alert_list.go`) collapse to ONE `prometheus_alert` tool — a straight rename of
   `alertmanager_alert_list.go` reading Alertmanager `/api/v2/alerts` only. The Prometheus-native
   readers (`alert_list.go`, `alert_describe.go`) are deleted (same use case, redundant). NO `source`
   discriminator, NO `detail` flag, NO `alertmanager_alert` naming anywhere in tool names/symbols.
   `prometheus_alertmanager_alert_write` is RENAMED to `prometheus_alert_write` (types renamed:
   `AlertmanagerAlertWriteTool` → `AlertWriteTool`, etc.) with identical logic. `AlertTool` embeds
   `*alertmanagerBaseTool` only (no Prometheus `baseTool`).
5. **Pagination helper relocation:** `AlertListPaginate` (defined in `alert_list.go`) and
   `alertPaginateToken` (defined in `alert_list.go`) move to `helper.go`; `AlertListPaginate` is
   renamed to `AlertPaginate` to avoid the deleted-reader naming. `paginateWindow`/`nextPageToken`
   stay in `helper.go` and reference `AlertPaginate`/`alertPaginateToken`. No dangling references
   after `alert_list.go` deletion.
6. **Datasource write removal:** Grafana data source writes are not in LLM scope. The
   `grafana_datasource` tool is READ-ONLY (consolidated list+describe per §4.11). No
   `grafana_datasource_write` tool, no datasource write client methods, no datasource write wire types,
   no datasource write output structs, no datasource write tests.
7. **Preservation:** all existing safety behaviors preserved (subquery >7d now applied to both metric
   modes; RFC3339 parsing; filter regex; pagination token scheme; redactScrapeURL; alertmanager
   fingerprint precedence; alertmanager matcher escaping + label-key validation; dashboard
   protection blocklist for update AND delete; datasource secret redaction; url.PathEscape for all
   UIDs; dryRun/confirmed gate on all write ops; error wrapping with emperror.dev/errors).
8. **Registry/safety wiring:** prometheus `WriteToolNames()` → `["prometheus_alert_write"]` (was
   `["prometheus_alertmanager_alert_write"]`); grafana `WriteToolNames()` →
   `["grafana_dashboard_write"]`; `var _ tool.InvokableTool` assertions updated in both `registry.go`
   files.
9. **Health check wiring:** both `check.go` updated to probe the consolidated read tools via their
   real GET/list endpoints; write tools marked `StatusLimited` "not probed to avoid side effects".
   Prometheus `clientErrorResults` → 3 entries (`prometheus_instance_list`, `prometheus_metric`,
   `prometheus_target_list`); `alertmanagerClientErrorResults` → 2 entries (`prometheus_alert`,
   `prometheus_alert_write`). `probeAlert` does a real Alertmanager `ListAlerts` GET for
   `prometheus_alert`; `prometheus_alert_write` is `StatusLimited`. When an instance has no
   Alertmanager configured, both alert results are `StatusLimited` "alertmanager not configured".
   Fixed-count test assertions updated (prometheus 8→5, 6→3, 2→2 with renamed components; grafana
   6→4).
10. **Client methods:** grafana adds `DeleteDashboard` only (stdlib + existing `net/http`,
    path-escaped UID); prometheus adds no new client methods (consolidation is tool-level only;
    `alertmanager_client.go` unchanged).
11. **Tests + README:** new test files per §5; both READMEs updated with new tool tables, param tables,
    factory functions, read/write split, and breaking-change notes.

---

## 10. Open Questions / Out of Scope

- **Dashboard create vs update wire call:** both POST to `/api/dashboards/db` (Grafana upsert). The
  `operation` discriminator is semantic for create/update and structural for delete. No separate
  PUT endpoint is used for dashboards (Grafana legacy API is POST-only for save). Documented.
- **Grafana datasource write:** explicitly out of scope (not in LLM scope). If write capability is
  needed later, it would be a separate `grafana_datasource_write` tool following the same pattern as
  `grafana_dashboard_write`.
- **Prometheus-native alert read:** explicitly removed. The Prometheus `/api/v1/alerts` endpoint
  returns the same alerts that Alertmanager `/api/v2/alerts` exposes (Prometheus fires them,
  Alertmanager receives and routes them), so a single Alertmanager-backed `prometheus_alert` tool
  covers the use case. If a future need arises to read alerts directly from Prometheus (e.g. an
  instance with no Alertmanager configured), that would be a separate follow-up tool — out of scope
  here.
