# Grafana Dashboard Render/Validate & Query Cardinality Tools

## Goal & Scope

The Grafana `grafana_dashboard_write` tool lets the LLM create dashboards, but
the LLM cannot inspect whether the Prometheus/Loki queries in those dashboards
actually return data, or whether they return one series or thousands. This
leads to dashboards with empty panels and panels with far too many series.

Add two **read-only** tools to `components/tool/grafana/` that close this loop:

1. **`grafana_query`** — execute a single PromQL (Prometheus) or LogQL (Loki)
   query against a datasource by UID via Grafana's datasource proxy, and return
   a cardinality-focused summary: `resultType`, `seriesCount`, per-series label
   sets, a sample value/line, and empty-result hints. Lets the LLM detect
   "no data" (`seriesCount == 0`) and "too many series" (`seriesCount` large)
   and inspect the label sets to narrow the query.
2. **`grafana_dashboard_validate`** — fetch a saved dashboard by UID, walk every
   panel, extract each Prometheus/Loki query, execute it, and return a
   per-panel per-query verdict (`ok` / `no-data` / `too-many-series` / `error` /
   `skipped`) plus a roll-up summary. Lets the LLM "render" a dashboard it just
   created and see which panels are broken.

### In scope
- Prometheus and Loki datasources only (via Grafana datasource proxy).
- Instant and range queries.
- v1 (classic) and v2 (schema v2, `elements` map) dashboard panel extraction.
- Single-panel validation (by `panelID`) and whole-dashboard validation.

### Out of scope
- Other datasource types (CloudWatch, Elasticsearch, ClickHouse, etc.). Panels
  of those types are reported with verdict `skipped` and a reason.
- Validating unsaved dashboard models (the validate tool fetches by UID only;
  the LLM must save first via `grafana_dashboard_write`).
- Rendering dashboard/panel PNG images (requires the Grafana Image Renderer
  service; not needed for the cardinality/no-data use case).
- Datasource health checks (already covered conceptually by `grafana_datasource`
  describe; not duplicated here).
- Loki label/pattern/stats metadata tools (only the query endpoint is used).

## Reference: what mcp-grafana does (research notes)

Studied `github.com/grafana/mcp-grafana` (main branch) via webfetch. Relevant
mechanisms:

- `tools/prometheus.go` — `query_prometheus` executes PromQL via the Grafana
  datasource proxy. `isPrometheusResultEmpty(result)` treats a `model.Vector`
  / `model.Matrix` of length 0 as empty. Series count = `len(result)`.
  `query_prometheus_histogram` adds hints when the result is empty or all-NaN.
- `tools/loki.go` / `tools/loki_backend.go` — `query_loki_logs` uses
  `/loki/api/v1/query` (instant) and `/loki/api/v1/query_range` (range) through
  Grafana's proxy. Response envelope `{status, data:{resultType, result}}`.
  `resultType` is `"streams"` (log queries), `"vector"`, or `"matrix"` (metric
  queries). For `streams`, `result` is `[{stream:{labels}, values:[[ts,line]]}]`;
  for `vector`/`matrix` it is `[{metric:{labels}, value|values:...}]`. Series
  count = `len(result)`.
- `tools/prom_backend.go` — Prometheus backend uses
  `/api/datasources/uid/{uid}/resources/api/v1/...`; Loki uses
  `/api/datasources/proxy/uid/{uid}/loki/api/v1/...`. Both are Grafana datasource
  proxy variants that forward to the datasource with Grafana auth.
- `tools/dashboard_helpers.go` — `collectAllPanels` walks v1 dashboards
  (top-level `panels`, with `row`-typed entries holding nested `panels`) and
  legacy `rows[].panels`. `extractPanelQueries` reads `targets[]`, probes
  `expr`/`query`/`expression`/`rawSql`/... for the query string, and reads
  `target.datasource` / `panel.datasource` `{uid, type}`.
- `tools/dashboard_v2.go` — v2 dashboards store panels under `elements` (a map
  of `{kind, spec}`); queries live at `spec.data.spec.queries[].spec.query.spec`
  with `datasource.name` as the UID and `query.group` as the type.
- `tools/datasources.go` — `check_datasources_health` hits
  `GET /api/datasources/uid/{uid}/health` and returns `{status, message}`.

### Adaptation decision
mcp-grafana depends on `grafana-plugin-sdk-go` and the Prometheus client
library for response parsing. Our component uses raw `net/http` only (see
`client.go`). To stay consistent and dependency-free, we use the **Grafana
datasource proxy endpoint** (`/api/datasources/proxy/uid/{uid}/...`) for both
Prometheus and Loki and parse the standard `{status, data:{resultType,
result}}` JSON ourselves. `len(data.result)` is the series/stream count.

## File paths

Create:
- `/projects/eino-ext/components/tool/grafana/query.go` — `grafana_query` tool.
- `/projects/eino-ext/components/tool/grafana/dashboard_validate.go` — `grafana_dashboard_validate` tool.
- `/projects/eino-ext/components/tool/grafana/query_client.go` — HTTP methods for proxy queries + wire/response types + response parsing.
- `/projects/eino-ext/components/tool/grafana/query_helper.go` — shared helpers: `parseQueryTime`, `executeQuery`, `collectPanels`, `extractPanelQueries`, verdict logic.
- `/projects/eino-ext/components/tool/grafana/query_test.go` — tests for the query tool.
- `/projects/eino-ext/components/tool/grafana/dashboard_validate_test.go` — tests for the validate tool.
- `/projects/eino-ext/components/tool/grafana/prompts/query_output_guidance.md` — embedded prompt for `grafana_query`.
- `/projects/eino-ext/components/tool/grafana/prompts/dashboard_validate_output_guidance.md` — embedded prompt for `grafana_dashboard_validate`.

Modify:
- `/projects/eino-ext/components/tool/grafana/client.go` — no change required (reuse `doRequest`); proxy path construction lives in `query_client.go`.
- `/projects/eino-ext/components/tool/grafana/registry.go` — register both tools in `readOnlyConstructors`; add compile-time interface checks.
- `/projects/eino-ext/components/tool/grafana/check.go` — add the two new tool names to `allComponentNames()` and a `probeQuery`/`probeDashboardValidate` to `probeInstance`.
- `/projects/eino-ext/components/tool/grafana/check_test.go` — update expected name count.
- `/projects/eino-ext/components/tool/grafana/suite_test.go` — add mock proxy endpoints for Prometheus and Loki query + a multi-panel dashboard fixture.
- `/projects/eino-ext/components/tool/grafana/README.md` — add the two tools to the table, tool-details section, and factory note.
- `/projects/eino-ext/components/tool/grafana/helper.go` — add `//go:embed` vars for the two new prompt files.

## Data structures

### `query.go`

```go
// Package grafana provides eino tools for Grafana dashboard management.
// (package comment already exists in config.go; do not duplicate.)

const queryToolName = "grafana_query"

const queryDescription = `
** General Purpose **
Execute a single PromQL (Prometheus) or LogQL (Loki) query against a Grafana
datasource by UID, via Grafana's datasource proxy. Returns a cardinality-focused
summary: the result type, the number of series/streams, the label set of each
series (up to maxSeries), and a sample value or log line. Use this to check
whether a query returns data and whether it returns one series or many.

The datasource type (prometheus or loki) is resolved automatically from the
datasource UID; you do not need to specify it. Non-Prometheus/Loki datasources
return an error.

** Output **
A JSON object with: datasourceUid, datasourceType, expr, queryType, resultType,
seriesCount, truncated, series[], hints[]. An empty result (seriesCount=0) is a
normal result, NOT an error; use it to detect "no data". A large seriesCount
means the query is too broad — narrow it by adding label filters.
`

// QueryParams defines the parameters for grafana_query.
type QueryParams struct {
    Instance      string `json:"instance" validate:"required" jsonschema:"(required) The Grafana instance to connect to."`
    DatasourceUID string `json:"datasourceUID" validate:"required" jsonschema:"(required) The UID of the Prometheus or Loki datasource to query."`
    Expr          string `json:"expr" validate:"required,max=8192" jsonschema:"(required) The PromQL or LogQL expression to execute."`
    QueryType     string `json:"queryType,omitempty" validate:"omitempty,oneof=instant range" jsonschema:"(optional) 'instant' (default) or 'range'. Use 'instant' to check cardinality / no-data; use 'range' only when you need a time series."`
    // instant-mode: anchor time. range-mode: end time.
    Time          string `json:"time,omitempty" validate:"omitempty,max=64" jsonschema:"(optional) Query anchor time. 'now' (default), 'now-1h', RFC3339, or Unix seconds. For instant this is the evaluation time; for range this is the end."`
    Start         string `json:"start,omitempty" validate:"omitempty,max=64" jsonschema:"(optional, range mode) Start time. 'now-1h', RFC3339, or Unix seconds. Defaults to time-1h when queryType=range."`
    StepSeconds   int    `json:"stepSeconds,omitempty" validate:"omitempty,min=1,max=86400" jsonschema:"(optional, range mode) Step size in seconds. Required for range queries (default 60)."`
    MaxSeries     int    `json:"maxSeries,omitempty" validate:"omitempty,min=1,max=1000" jsonschema:"(optional) Cap on the number of series returned in 'series' (default 20). seriesCount reflects the true total; 'series' is truncated to this many."`
}

// QueryResultOutput is the structured output of grafana_query.
type QueryResultOutput struct {
    DatasourceUID  string         `json:"datasourceUid"`
    DatasourceType string         `json:"datasourceType"` // "prometheus" | "loki"
    Expr           string         `json:"expr"`
    QueryType      string         `json:"queryType"`      // "instant" | "range"
    ResultType     string         `json:"resultType"`     // "vector" | "matrix" | "streams" | "scalar" | "string"
    SeriesCount    int            `json:"seriesCount"`
    Truncated      bool           `json:"truncated"`
    Series         []SeriesSummary `json:"series,omitempty"`
    Hints          []string       `json:"hints,omitempty"`
}

// SeriesSummary is a single series/stream summary.
type SeriesSummary struct {
    Labels map[string]string `json:"labels"`
    // instant vector / scalar: the single value.
    Value *float64 `json:"value,omitempty"`
    // range matrix: the last value in the series.
    Sample *MetricSample `json:"sample,omitempty"`
    // streams (Loki log queries): one sample log line.
    Line string `json:"line,omitempty"`
}

// MetricSample is a single timestamped value (used for range matrix samples).
type MetricSample struct {
    Timestamp string  `json:"timestamp"`
    Value     float64 `json:"value"`
}

// QueryTool is an eino tool for executing PromQL/LogQL queries via Grafana.
type QueryTool struct {
    *baseTool
    tool.InvokableTool
}

func (t *QueryTool) Invoke(ctx context.Context, params *QueryParams) (string, error)

// NewQueryTool creates a new QueryTool.
func NewQueryTool(ctx context.Context, configs Configs) (*QueryTool, error)
```

### `dashboard_validate.go`

```go
const dashboardValidateToolName = "grafana_dashboard_validate"

const dashboardValidateDescription = `
** General Purpose **
Fetch a saved dashboard by UID and validate every panel by executing its
Prometheus/Loki queries. Returns a per-panel, per-query verdict so you can see
which panels have no data, which return too many series, and which errored.
Use this AFTER creating or updating a dashboard to confirm its panels work.

Uses INSTANT queries (anchored at 'time', default 'now') — sufficient to detect
no-data and cardinality. Non-Prometheus/Loki panels are reported as 'skipped'
with a reason; they are not executed.

** Output **
A JSON object with: uid, title, panelCount, validatedPanels, summary{}, panels[].
Each panel has: panelId, title, type, datasource, verdict, reason, queries[].
Each query has: refId, expr, resultType, seriesCount, verdict, error, sampleLabels[].
Verdicts: 'ok' | 'no-data' | 'too-many-series' | 'error' | 'skipped'.
`

// DashboardValidateParams defines the parameters for grafana_dashboard_validate.
type DashboardValidateParams struct {
    Instance         string `json:"instance" validate:"required" jsonschema:"(required) The Grafana instance to connect to."`
    UID              string `json:"uid" validate:"required,max=256" jsonschema:"(required) The UID of the saved dashboard to validate."`
    PanelID          *int   `json:"panelID,omitempty" validate:"omitempty,min=1" jsonschema:"(optional) Validate only this panel by id. If empty, validate all panels."`
    Time             string `json:"time,omitempty" validate:"omitempty,max=64" jsonschema:"(optional) Instant-query anchor time. 'now' (default), 'now-1h', RFC3339, or Unix seconds."`
    MaxSeriesPerPanel int   `json:"maxSeriesPerPanel,omitempty" validate:"omitempty,min=1,max=10000" jsonschema:"(optional) Panels with more than this many series are flagged 'too-many-series' (default 20)."`
    MaxPanels        int    `json:"maxPanels,omitempty" validate:"omitempty,min=1,max=500" jsonschema:"(optional) Cap on the number of panels to validate (default 50). Excess panels are skipped with a reason."`
    MaxSeriesSample  int    `json:"maxSeriesSample,omitempty" validate:"omitempty,min=0,max=1000" jsonschema:"(optional) Number of sample label sets to include per query (default 5). 0 disables sample labels."`
}

// DashboardValidationOutput is the structured output of grafana_dashboard_validate.
type DashboardValidationOutput struct {
    UID             string                   `json:"uid"`
    Title           string                   `json:"title"`
    PanelCount      int                      `json:"panelCount"`
    ValidatedPanels int                      `json:"validatedPanels"`
    Summary         ValidationSummary        `json:"summary"`
    Panels          []PanelValidationResult  `json:"panels"`
}

type ValidationSummary struct {
    OK            int `json:"ok"`
    NoData        int `json:"noData"`
    TooManySeries int `json:"tooManySeries"`
    Errors        int `json:"errors"`
    Skipped       int `json:"skipped"`
}

type PanelValidationResult struct {
    PanelID    int                      `json:"panelId"`
    Title      string                   `json:"title"`
    Type       string                   `json:"type"`
    Datasource *DatasourceRef           `json:"datasource,omitempty"`
    Verdict    string                   `json:"verdict"`
    Reason     string                   `json:"reason,omitempty"`
    Queries    []QueryValidationResult   `json:"queries,omitempty"`
}

type QueryValidationResult struct {
    RefID        string              `json:"refId"`
    Expr         string              `json:"expr"`
    ResultType   string              `json:"resultType,omitempty"`
    SeriesCount  int                 `json:"seriesCount"`
    Verdict      string              `json:"verdict"`
    Error        string              `json:"error,omitempty"`
    SampleLabels []map[string]string `json:"sampleLabels,omitempty"`
}

// DatasourceRef is a minimal datasource reference extracted from a panel.
type DatasourceRef struct {
    UID  string `json:"uid,omitempty"`
    Type string `json:"type,omitempty"`
}

// DashboardValidateTool is an eino tool for validating a saved dashboard's panels.
type DashboardValidateTool struct {
    *baseTool
    tool.InvokableTool
}

func (t *DashboardValidateTool) Invoke(ctx context.Context, params *DashboardValidateParams) (string, error)

// NewDashboardValidateTool creates a new DashboardValidateTool.
func NewDashboardValidateTool(ctx context.Context, configs Configs) (*DashboardValidateTool, error)
```

### `query_client.go` (wire + response types + HTTP methods)

```go
// proxyQueryResponse is the shared Prometheus/Loki query envelope.
// Both return {status, data:{resultType, result}}. For Loki streams,
// result entries use "stream" instead of "metric".
type proxyQueryResponse struct {
    Status    string          `json:"status"` // "success" | "error"
    Data      proxyQueryData  `json:"data"`
    ErrorType string          `json:"errorType,omitempty"`
    Error     string          `json:"error,omitempty"`
    Warnings  []string        `json:"warnings,omitempty"`
}

type proxyQueryData struct {
    ResultType string          `json:"resultType"`
    Result     json.RawMessage `json:"result"`
}

// Wire shapes for parsing result by resultType.
type wireVectorSample struct {
    Metric map[string]string   `json:"metric"`
    Value  []json.RawMessage   `json:"value"`  // [ts, "val"]
}
type wireMatrixSample struct {
    Metric map[string]string   `json:"metric"`
    Values [][]json.RawMessage `json:"values"` // [[ts, "val"], ...]
}
type wireStream struct {
    Stream map[string]string   `json:"stream"`
    Values [][]json.RawMessage `json:"values"` // [[ts_nanos, "line"], ...]
}

// datasourceProxyGet performs a GET to /api/datasources/proxy/uid/{uid}{path}
// with the given query params and returns the raw body. The uid is path-escaped.
func (c *grafanaClient) datasourceProxyGet(ctx context.Context, uid, path string, q url.Values) ([]byte, error)

// QueryPrometheus executes a PromQL query via the Prometheus proxy.
// queryType is "instant" or "range". For instant, only evalTime is used.
// For range, start, end, and stepSeconds are used. Returns the parsed response.
func (c *grafanaClient) QueryPrometheus(ctx context.Context, uid, expr, queryType string, evalTime, start, end time.Time, stepSeconds int) (*proxyQueryResponse, error)

// QueryLoki executes a LogQL query via the Loki proxy. Same semantics as
// QueryPrometheus but uses /loki/api/v1/... and nanosecond start/end for range.
// limit (log lines) and direction ("forward"/"backward") apply to range log queries.
func (c *grafanaClient) QueryLoki(ctx context.Context, uid, expr, queryType string, evalTime, start, end time.Time, stepSeconds int, limit int, direction string) (*proxyQueryResponse, error)

// parseProxyQueryResult decodes proxyQueryResponse.Data.Result into a slice
// of SeriesSummary based on ResultType. Returns the series and any parse error.
func parseProxyQueryResult(resp *proxyQueryResponse) ([]SeriesSummary, error)
```

### `query_helper.go` (shared logic)

```go
// parseQueryTime parses a query time string. Supports: "" (returns def),
// "now", "now-1h" / "now-2h30m" (Go-duration suffix), RFC3339, and Unix seconds
// (integer or float). Returns def for "".
func parseQueryTime(s string, def time.Time) (time.Time, error)

// executeQuery is the shared single-query executor used by both tools.
// It resolves the datasource type (unless hint is non-empty), routes to the
// right proxy path/time format, executes, and returns a QueryResultOutput.
// seriesCount is the true total; series is truncated to maxSeries.
// An empty result is a normal result (SeriesCount=0), not an error.
func (b *baseTool) executeQuery(ctx context.Context, instance, datasourceUID, datasourceTypeHint, expr, queryType string, evalTime, start, end time.Time, stepSeconds, maxSeries int) (*QueryResultOutput, error)

// collectPanels returns all panels from a dashboard model, handling v1
// (top-level "panels" with row nesting, falling back to legacy "rows[].panels")
// and v2 ("elements" map of {kind:"Panel", spec:{...}}).
func collectPanels(model map[string]any) []map[string]any

// extractPanelQueries extracts the queries from a single panel. Returns one
// entry per target with a non-empty expression. Probes expr/query/expression/
// rawSql/rawSQL/rawQuery for the query string (first non-empty wins). Reads
// datasource {uid,type} from the target, then the panel-level datasource.
func extractPanelQueries(panel map[string]any) []panelQuery

// panelQuery is an intermediate extracted query.
type panelQuery struct {
    RefID      string
    Expr       string
    Datasource DatasourceRef
}

// queryVerdict returns the verdict string for a series count vs. threshold.
func queryVerdict(seriesCount, maxSeriesPerPanel int, err error) (verdict string, errMsg string)

// panelVerdict rolls up per-query verdicts to a single panel verdict.
// Precedence (worst first): error > no-data > too-many-series > skipped > ok.
func panelVerdict(queryVerdicts []string) string

// emptyResultHints returns LLM-facing hints for an empty query result.
func emptyResultHints(datasourceType, expr string, evalTime time.Time) []string
```

## API endpoints & request/response shape

All requests reuse `grafanaClient.doRequest` (Bearer auth, JSON accept, per-client timeout). The datasource UID is `url.PathEscape`d to prevent path traversal (mirrors `GetDataSource`).

### Datasource type resolution (for `grafana_query`)
- `GET /api/datasources/uid/{uid}` (existing `GetDataSource`) → `dataSource.Type` is `"prometheus"` or `"loki"`. Any other type → error `"datasource %q is of unsupported type %q; only prometheus and loki are supported"`.
- The validate tool does NOT call this: it reads the type from the panel's `datasource.type` field directly.

### Prometheus query (proxy)
- Instant: `GET /api/datasources/proxy/uid/{uid}/api/v1/query?query={expr}&time={unix_seconds}`
- Range:   `GET /api/datasources/proxy/uid/{uid}/api/v1/query_range?query={expr}&start={unix_seconds}&end={unix_seconds}&step={seconds}`
- `time`/`start`/`end` are Unix seconds (float string, e.g. `fmt.Sprintf("%d", t.Unix())`).
- Response: `{status:"success", data:{resultType:"vector"|"matrix"|"scalar"|"string", result:[...]}}`.
  - `vector`: `result` is `[{metric:{...}, value:[ts, "val"]}]`. `len` = series count.
  - `matrix`: `result` is `[{metric:{...}, values:[[ts,"val"], ...]}]`. `len` = series count.
- On `status:"error"`: return error wrapping `errorType` + `error`.

### Loki query (proxy)
- Instant: `GET /api/datasources/proxy/uid/{uid}/loki/api/v1/query?query={expr}&time={unix_seconds}`
- Range:   `GET /api/datasources/proxy/uid/{uid}/loki/api/v1/query_range?query={expr}&start={unix_nanos}&end={unix_nanos}&step={seconds}&limit={limit}&direction={direction}`
- `time` is Unix seconds; `start`/`end` are Unix **nanoseconds** (`t.UnixNano()`).
- Response: same envelope. `resultType` may be `"streams"`, `"vector"`, or `"matrix"`.
  - `streams`: `result` is `[{stream:{labels}, values:[[ts_nanos, "line"], ...]}]`. `len` = stream count.
  - `vector`/`matrix`: same as Prometheus.
- Default `limit=10`, `direction="backward"` for range log queries (mirrors mcp-grafana).

### Response parsing (`parseProxyQueryResult`)
Switch on `ResultType`:
- `"vector"`: unmarshal `[]wireVectorSample`; for each, `SeriesSummary{Labels: Metric, Value: parsed float64 of value[1]}`.
- `"matrix"`: unmarshal `[]wireMatrixSample`; for each, `SeriesSummary{Labels: Metric, Sample: last (ts,value) pair}`.
- `"streams"`: unmarshal `[]wireStream`; for each, `SeriesSummary{Labels: Stream, Line: first value[1] string}`.
- `"scalar"`/`"string"`: single-element `SeriesSummary` with `Value` (scalar) or `Line` (string); `SeriesCount=1`.
- Unknown: error `"unsupported resultType %q"`.

Value parsing: Loki/Prometheus return values as JSON strings (`"1.234"`); parse with `strconv.ParseFloat`, fall back to direct float unmarshal (mirrors `parseMetricValue`).

### Series truncation
`executeQuery` returns `SeriesCount = len(allSeries)` (true total) and `Series = allSeries[:min(maxSeries, len)]`, with `Truncated = len(allSeries) > maxSeries`. Default `maxSeries=20`.

## Edge cases

- **Empty result** (`seriesCount=0`): a normal result, NOT an error. `Hints` populated via `emptyResultHints` (suggest: check metric/label exists, extend time range, check selector). Mirrors mcp-grafana `isPrometheusResultEmpty`.
- **All-NaN vector/matrix** (Prometheus): treated as no-data (verdict `no-data`) — mirrors `isPrometheusResultEmptyOrNaN`.
- **Non-Prometheus/Loki datasource** (`grafana_query`): error with the datasource type. (`grafana_dashboard_validate`: per-query verdict `skipped`, reason `"unsupported datasource type: {type}"`.)
- **Panel with no datasource** (validate): verdict `skipped`, reason `"panel has no datasource configured"`.
- **Panel with no targets / empty expr**: skipped, reason `"panel has no queries"`. Not counted as no-data.
- **Unknown datasource UID** (proxy 404): `httpError` 404 → wrapped error `"datasource with UID %q not found"`. Validate: per-query verdict `error`.
- **Bad PromQL/LogQL** (HTTP 200, `status:"error"`): return/wrap error with `errorType`+`error`. Validate: per-query verdict `error`.
- **Timeout**: per-client timeout (default 30s) via `doRequest`'s `context.WithTimeout`. Surfaced as a wrapped error.
- **Auth error** (401/403): `httpError` → wrapped error. Validate: per-query verdict `error`.
- **Large responses**: `series` truncated to `maxSeries`; log `Line` truncated to 256 chars; `expr` capped at 8192 chars in params. Validate: `maxPanels` cap (default 50); excess panels skipped with reason `"panel limit reached"`.
- **v2 dashboards** (`elements` map): `collectPanels` returns `spec` of each `kind:"Panel"` element; `extractPanelQueries` reads `data.spec.queries[].spec.query.spec` for the expression and `datasource.name`/`query.group` for UID/type (mirrors `dashboard_v2.go`).
- **Legacy dashboards** (`rows[].panels`, no top-level `panels`): `collectPanels` falls back to walking `rows`.
- **Row panels** (v1 `type:"row"`): their nested `panels` are flattened into the list; the row itself is not a query panel.
- **Dashboard not found** (validate, 404): wrapped error `"dashboard with UID %q not found"` (mirrors `grafana_dashboard_write` delete path).
- **`panelID` not found** (validate): error `"panel with ID %d not found in dashboard %q"`.
- **Redaction**: query results may contain user data (log lines, metric labels). Log `Line` is truncated to 256 chars. No Grafana secrets are returned by the proxy (auth is Grafana's bearer token, not datasource credentials). The bearer token is never included in output (it is only set as a request header in `doRequest`).
- **`stepSeconds` missing for range query**: default 60 (validate) / required-with-default for `grafana_query` (apply default 60 if 0 and queryType=range).

## Error handling

- All errors wrapped with `emperror.dev/errors` including operation context, e.g. `errors.Wrapf(err, "failed to query datasource %q", uid)`.
- 404 from datasource lookup/proxy → `"datasource with UID %q not found"` (typed `*httpError` checked via `isHTTPStatus`).
- 404 from dashboard fetch → `"dashboard with UID %q not found"`.
- `status:"error"` from Prometheus/Loki → `errors.Errorf("query failed: %s: %s", errorType, error)`.
- No-data is never an error: `SeriesCount=0` with `Hints` populated.

## Validation

- `QueryParams` and `DashboardValidateParams` carry `validate` + `jsonschema` tags (see structs above).
- Both `Invoke` methods call `validateParams(params)` (the shared `validate.Struct` wrapper) as the first step, before any I/O.
- Defaults are applied BEFORE validation: `QueryType` → `"instant"`, `Time` → `"now"`, `MaxSeries` → `20`, `StepSeconds` → `60` (range), `MaxSeriesPerPanel` → `20`, `MaxPanels` → `50`, `MaxSeriesSample` → `5`. Use `omitempty` tags (not `required`) on defaulted fields so validation runs against final values (per CONTRIBUTING.md).
- `NewQueryTool` / `NewDashboardValidateTool` call `newBaseTool(ctx, configs)` (which calls `BuildClients` → `NewClient` → `validate.Struct` on each `Config`); no extra `Config` validation needed.

## Security

- **SSRF / URL validation**: the proxy path is built from the already-validated `Config.URL` (scheme enforced in `NewClient`) and a path-escaped datasource UID (`url.PathEscape`). The `expr` is a query parameter (URL-encoded via `url.Values`), never a URL component. No user input becomes part of the request URL beyond the escaped UID.
- **Path traversal**: datasource UID and dashboard UID are `url.PathEscape`d (mirrors `GetDashboard`/`GetDataSource`); covered by a security test (see Testing).
- **Auth token**: the Grafana bearer token is set only as a request header by `doRequest`; it never appears in params, output, or error messages. `maxErrorBodyLen` (512) caps error-body leakage.
- **Timeouts**: every request uses the per-client timeout via `context.WithTimeout` in `doRequest`.
- **Output redaction**: log `Line` truncated to 256 chars; no datasource credentials are returned by the proxy (Grafana holds them). Existing `redact.go` is not needed here (no `jsonData` is returned).
- **No blocklist needed**: these are read-only query tools; no command execution or file access.

## Factory & registration

### `registry.go` changes
- Add to `readOnlyConstructors`:
  ```go
  func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewQueryTool(ctx, c) },
  func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewDashboardValidateTool(ctx, c) },
  ```
- Add compile-time checks:
  ```go
  _ tool.InvokableTool = (*QueryTool)(nil)
  _ tool.InvokableTool = (*DashboardValidateTool)(nil)
  ```
- `WriteToolNames()` unchanged (both tools are read-only).
- `NewAllTools` / `NewReadOnlyTools` automatically include the new tools via `readOnlyConstructors`.

### `helper.go` changes
Add embeds:
```go
//go:embed prompts/query_output_guidance.md
var queryOutputGuidance string

//go:embed prompts/dashboard_validate_output_guidance.md
var dashboardValidateOutputGuidance string
```

## Checkup (`check.go`)

- `allComponentNames()`: add `"grafana_query"` and `"grafana_dashboard_validate"` (new total: 6).
- `probeInstance`: add `probeQuery(ctx, client, instance)` and `probeDashboardValidate(ctx, client, instance)`.
  - `probeQuery`: list datasources; if a `prometheus` or `loki` datasource exists, run a trivial instant query (`up` for prometheus, `vector(1)` for loki) against its UID via the proxy. Status `ok` on HTTP 200 (even empty result), `error` on failure, `limited` if no prometheus/loki datasource is configured.
  - `probeDashboardValidate`: search dashboards (`Limit:1`); if one exists, call the validate path's panel-extraction on it (no query execution, to avoid cost) — `ok` if extraction succeeds, `error` otherwise; `limited` if no dashboards exist.
- `clientErrorResults` already iterates `allComponentNames()`, so it automatically covers the new tools once added.

## Testing & docs

### `query_test.go` (table-driven, mock server)
- instant prometheus vector: 3 series → `seriesCount=3`, `truncated=false`, 3 label sets.
- instant prometheus vector with `maxSeries=2` → `seriesCount=3`, `truncated=true`, 2 series.
- empty prometheus vector → `seriesCount=0`, `hints` non-empty, no error.
- range prometheus matrix → `resultType=matrix`, `sample` = last value.
- loki instant vector (metric query) → parsed like prometheus.
- loki range streams → `resultType=streams`, `seriesCount` = stream count, `line` = first log line.
- non-prometheus/loki datasource (mock type `"mysql"`) → error mentioning unsupported type.
- unknown datasource UID → 404 → error containing "not found".
- bad PromQL (mock `status:"error"`) → error containing the errorType.
- path-escape: datasource UID `"foo/bar"` produces path `/api/datasources/proxy/uid/foo%2Fbar/...` (assert via captured request URI, mirrors `security_test.go`).
- invalid params: missing `instance`, missing `datasourceUID`, missing `expr`, bad `queryType` → validation error.
- relative time parsing: `"now-1h"` resolves to ~1h ago.

### `dashboard_validate_test.go` (table-driven, mock server)
- multi-panel dashboard fixture (in `suite_test.go`): 2 prometheus panels (one with data, one empty), 1 loki panel, 1 non-prometheus panel (skipped), 1 panel with no datasource (skipped).
- validate all panels: summary counts correct; per-panel verdicts `ok`/`no-data`/`ok`/`skipped`/`skipped`.
- `too-many-series`: a panel whose query returns 25 series with `maxSeriesPerPanel=20` → verdict `too-many-series`.
- `panelID` set: validate only that panel.
- `maxPanels=2`: only 2 panels validated, rest skipped with reason.
- dashboard not found (404) → error "not found".
- `panelID` not found → error "panel with ID ... not found".
- v2 dashboard fixture (`elements` map) → panels extracted correctly.
- error query (mock `status:"error"`) → per-query verdict `error`, panel verdict `error`.
- path-escape of dashboard UID.

### `suite_test.go` additions
- Mock `GET /api/datasources/proxy/uid/{prom-uid}/api/v1/query` and `/query_range` returning configurable vector/matrix/empty/error responses (keyed on `query` param).
- Mock `GET /api/datasources/proxy/uid/{loki-uid}/loki/api/v1/query` and `/query_range` returning streams/vector/matrix.
- A `ds-mysql` datasource (type `mysql`) for the unsupported-type case.
- A multi-panel dashboard at `/api/dashboards/uid/validate-dash` with the fixture above.
- A v2 dashboard at `/api/dashboards/uid/v2-dash` with `elements`.

### `check_test.go`
- Update `TestAllComponentNames` expected count to 6.
- Update `TestCheckInvalidInstance` / `TestCheckClientErrorResults` expected result count to 6.

### `integration_test.go` (optional, `//go:build integration`)
- Add a `query` and `dashboard_validate` sub-test using the live Grafana's first prometheus/loki datasource and a test dashboard. Skip if no `GRAFANA_TOKEN`.

### `README.md`
- Add rows to the Available Tools table:
  - `grafana_query` | Read | Execute a PromQL/LogQL query and return cardinality + sample series.
  - `grafana_dashboard_validate` | Read | Validate a saved dashboard's panels by executing their Prometheus/Loki queries.
- Add a "Tool Details" subsection for each (parameter table + output description), mirroring the existing `grafana_dashboard` / `grafana_datasource` subsections.
- Note: both tools are included in `NewAllTools` and `NewReadOnlyTools` automatically.

### Prompt files
- `prompts/query_output_guidance.md`: explain `seriesCount` (0 = no data, large = too broad — narrow with label filters), `truncated`, `series[].labels`, and that empty results are not errors.
- `prompts/dashboard_validate_output_guidance.md`: explain verdicts, the summary roll-up, and the recommended workflow (create/update dashboard → validate → fix no-data/too-many-series panels → re-validate).

## Ordered implementation steps

1. **`query_client.go`**: add `proxyQueryResponse` + wire types, `datasourceProxyGet`, `QueryPrometheus`, `QueryLoki`, `parseProxyQueryResult`. Reuse `doRequest`, `url.PathEscape`, `emperror.dev/errors`. No new deps.
2. **`query_helper.go`**: add `parseQueryTime`, `executeQuery`, `collectPanels`, `extractPanelQueries`, `panelQuery`, `queryVerdict`, `panelVerdict`, `emptyResultHints`.
3. **`prompts/query_output_guidance.md` + `prompts/dashboard_validate_output_guidance.md`**: write the two prompt files.
4. **`helper.go`**: add the two `//go:embed` vars.
5. **`query.go`**: implement `QueryTool.Invoke` (resolve datasource type via `GetDataSource`, call `executeQuery`, marshal `QueryResultOutput`) and `NewQueryTool` (embed `baseTool`, `utils.InferTool(queryToolName, queryDescription + "\n" + queryOutputGuidance, Invoke)`).
6. **`dashboard_validate.go`**: implement `DashboardValidateTool.Invoke` (fetch dashboard via `fetchDashboard`, apply defaults, `collectPanels`, filter by `panelID`, cap `maxPanels`, for each panel `extractPanelQueries` → `executeQuery` per query → build `QueryValidationResult`/`PanelValidationResult`, roll up `panelVerdict` and `ValidationSummary`, marshal `DashboardValidationOutput`) and `NewDashboardValidateTool`.
7. **`registry.go`**: add the two constructors to `readOnlyConstructors` and the two compile-time checks.
8. **`check.go`**: add the two names to `allComponentNames()` and the two probes to `probeInstance`.
9. **`suite_test.go`**: add the proxy mock endpoints, the `ds-mysql` datasource, the multi-panel `validate-dash` fixture, and the v2 `v2-dash` fixture.
10. **`query_test.go`**: write the table-driven tests listed above.
11. **`dashboard_validate_test.go`**: write the table-driven tests listed above.
12. **`check_test.go`**: update expected counts to 6.
13. **`README.md`**: add the two tools to the table and tool-details sections.
14. **Validate**: run `go build ./...`, `go vet ./...`, `go test ./components/tool/grafana/...`. Fix until green.

## Risks & open notes

- **Proxy vs resources endpoint**: mcp-grafana uses `/api/datasources/uid/{uid}/resources/...` for Prometheus and `/api/datasources/proxy/uid/{uid}/...` for Loki, with a fallback transport between them for managed-Grafana compatibility. We use the **proxy** endpoint for both for simplicity. If a managed Grafana deployment rejects the proxy path for Prometheus, a follow-up can add the resources-path fallback (out of scope here).
- **Loki `httpMethod: GET`**: some Prometheus datasources are configured with `httpMethod: GET`; the proxy GET used here is unaffected (we always GET). No `postToGetRoundTripper` needed.
- **v2 dashboard prevalence**: v2 (`dashboard.grafana.app`) is newer; most existing dashboards are v1. Both are supported, but v2 extraction is based on `dashboard_v2.go` shapes and should be tested against a real v2 dashboard if available.
- **Log-line sensitivity**: Loki log lines may contain secrets; we truncate to 256 chars but do not redact. This is the user's own log data surfaced through their own Grafana; acceptable, but noted.
