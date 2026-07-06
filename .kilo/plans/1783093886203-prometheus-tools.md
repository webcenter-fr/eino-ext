# Plan: Prometheus Tools Component

## Goal
Add `components/tool/prometheus/` — a set of eino tools to query Prometheus metrics and alerts, following the same patterns as `components/tool/kubernetes/` and `components/tool/argocd/`.

## Files to Create (9 files)

| File | Purpose |
|------|---------|
| `config.go` | `Configs` map + `Config` struct (Address, auth, TLS) |
| `client.go` | Build `promapi.API` from Config |
| `base.go` | Shared `baseTool` with client map + known instances |
| `helper.go` | `listOutputGuidance`, `describeOutputGuidance`, `MustMarshal`, `instanceNotFoundError` |
| `metric_query.go` | Instant PromQL query tool |
| `metric_range.go` | Range PromQL query tool |
| `alert_list.go` | List current alerts tool |
| `alert_describe.go` | Describe specific alerts tool |
| `registry.go` | `NewAllTools`, `NewReadOnlyTools`, `WriteToolNames`, `NewAllToolsWithSafety` |

## Config Design

```go
type Configs map[string]Config

type Config struct {
    Address            string // e.g. "http://localhost:9090"
    Username           string // optional basic auth
    Password           string // optional basic auth
    BearerToken        string // optional bearer token
    InsecureSkipVerify bool   // skip TLS verification
}
```

## Client Design

Build a `prometheus/client_golang/api.Config` with `Address`, then wrap with a custom `http.RoundTripper` that injects auth headers (Bearer or Basic) and handles `InsecureSkipVerify`. Use `promapi.NewAPI(c)` to get the v1 client.

## Tool Details

### 1. `prometheus_metric_query` (instant)
- **Params**: `instance`, `query` (PromQL), `filter` (RE2 regex on result JSON), `time` (optional, RFC3339), `limit` (optional int)
- **Description**: Execute instant PromQL query, return light JSON array
- **Output**: `[{metric: {__name__: "...", label: "val"}, value: [timestamp, "value"]}]`
- **Context control**: `filter` regex, `limit` cap

### 2. `prometheus_metric_range` (range)
- **Params**: `instance`, `query` (PromQL), `start`, `end` (RFC3339), `step` (duration), `filter` (RE2 regex), `maxSamples` (pagination, default 100)
- **Description**: Execute range PromQL query over time window
- **Output**: `[{metric: {...}, values: [[ts, val], ...]}]`
- **Context control**: `filter` regex, `maxSamples` per metric, `step` coarseness

### 3. `prometheus_alert_list` (list)
- **Params**: `instance`, `filter` (RE2 regex), `state` (firing/pending/inactive), `paginate.pageSize`, `paginate.paginateToken`
- **Description**: List current alerts with lightweight output
- **Output**: `[{labels: {...}, annotations: {summary, description}, state, activeAt, value}]`
- **Context control**: `filter` regex, pagination, `state` filter

### 4. `prometheus_alert_describe` (describe)
- **Params**: `instance`, `filter` (RE2 regex on labels), `state` (optional)
- **Description**: Get full detail for alerts matching filter (all labels + all annotations)
- **Output**: Full alert JSON with all labels and annotations
- **Context control**: `filter` regex, `state` filter

## Guidance Text

```
** How to limit output (IMPORTANT) **
Always narrow the query to avoid large responses:
- Write precise PromQL queries (e.g. use metric name, label selectors).
- Use `filter` (Go RE2 regex, applied on each result JSON) to keep only matches.
- Use `state` to filter alerts by firing/pending/inactive.
...
```

## Registry Pattern

Same as existing components:
- `readOnlyConstructors` lists all 4 tools
- `writeConstructors` is empty (all tools are read-only)
- `WriteToolNames()` returns `nil`
- `NewAllToolsWithSafety()` auto-populates empty write tool names

## Dependencies

Already in `go.mod`:
- `github.com/prometheus/client_golang v1.23.2` (provides `api`, `api/prometheus/v1`, `prometheus/model`)
- `github.com/goccy/go-json` (JSON marshal)
- `github.com/cloudwego/eino/components/tool` and `.../tool/utils` (InferTool)
- `github.com/webcenter-fr/eino-ext/libs/toolkit/filter` (Compile/Match regex)
- `github.com/webcenter-fr/eino-ext/libs/toolkit/marshal` (MustMarshal)
- `github.com/webcenter-fr/eino-ext/libs/toolkit/validate` (Struct validation)
- `emperror.dev/errors` (error wrapping)

## Validation

- Run `go build ./components/tool/prometheus/...` to verify compilation
- Run `go vet ./components/tool/prometheus/...` for static analysis
- Existing test patterns use `suite_test.go` with testify — follow for future tests

## Risks / Notes

- Prometheus API `Alerts()` doesn't have a "get by ID" endpoint; `alert_describe` uses label-based regex filtering to identify specific alerts
- The `prometheus/client_model` proto types (MetricFamily) are heavy — we use lightweight hand-crafted output structs instead
- Auth is optional (no auth, Basic, or Bearer); the `RoundTripper` should handle all three cases gracefully
- Default `maxSamples` of 100 prevents context explosion on range queries
