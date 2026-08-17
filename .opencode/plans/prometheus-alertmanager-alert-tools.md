# Plan: Alertmanager Alert Tools in the Prometheus Component

## Goal

Add **two** eino tools in `components/tool/prometheus/` that read and mutate
alerts on an **Alertmanager** v2 HTTP API, alongside the existing read-only
Prometheus tools. The design follows the Kubernetes "generic operation"
philosophy: one read tool, one write tool with an `operation` discriminator —
fewer tools, cleaner read/write safety split.

| Tool name | Read/Write | Operations | Alertmanager endpoint |
|---|---|---|---|
| `prometheus_alertmanager_alert_list` | read | list / get-single | `GET /api/v2/alerts` |
| `prometheus_alertmanager_alert_write` | write | `create` / `update` / `delete` (via `operation` param) | `POST /api/v2/alerts` (+ `GET` for `update`) |

- The **list** tool is read-only and is **not** gated by `dryRun`/`confirmed`.
- The **write** tool is the **only** write tool. It lives in `writeConstructors`
  and `WriteToolNames()` returns `[]string{"prometheus_alertmanager_alert_write"}`.
  All three operations (`create`/`update`/`delete`) are gated by
  `libs/toolkit/confirm.RequireConfirmation(DryRun, Confirmed)`.

## Non-goals

- No silences management (`POST /api/v2/silences`, `DELETE /api/v2/silence/{id}`).
  Alertmanager has no native `DELETE /alerts`; "delete" here = resolve/expire an
  alert by re-posting it with `endsAt <= now`. Out of scope: silences, RBAC
  management, alert routing config.
- No change to existing read-only Prometheus tools or their behavior.
- No Alertmanager Go-client library dependency (see Decision 1).

## Key decisions

### Decision 1 — Minimal HTTP client over Alertmanager REST API (option b)

Use a hand-rolled HTTP client (mirroring `components/tool/grafana/client.go`),
**not** `github.com/prometheus/alertmanager` client libs. Rationale:
- The Alertmanager v2 Go client (`github.com/prometheus/alertmanager/api/v2/client`)
  is generated from OpenAPI and pulls in `go-openapi/*` runtime deps not present
  in `go.mod`; adding it bloats the module graph for a 2-endpoint use case.
- The Grafana sibling already establishes the hand-rolled HTTP client pattern
  (`grafanaClient`, `doRequest`, typed `httpError`, truncated error bodies).
- Only two endpoints are needed: `GET /api/v2/alerts` and `POST /api/v2/alerts`.
- **No new `go.mod` dependencies.** Uses stdlib + existing
  `emperror.dev/errors`, `github.com/goccy/go-json`,
  `github.com/prometheus/common/model`, `github.com/webcenter-fr/eino-ext/libs/toolkit/...`.

### Decision 2 — Reuse `authRoundTripper` for auth/TLS

The existing `authRoundTripper` in `client.go` (Basic + Bearer, Bearer wins) is
reused as the `http.Client` transport for the Alertmanager client. This keeps a
single auth implementation and matches the Prometheus client style. TLS skip
verify is handled the same way as `NewClient` (clone `DefaultRoundTripper`).

### Decision 3 — Extend `Config` with optional `Alertmanager` sub-config

Add an optional `Alertmanager *AlertmanagerConfig` field to the existing
`Config`. Backward compatible: existing Prometheus-only configs are unchanged.
Instances without `Alertmanager` set simply have no Alertmanager client
(alertmanager tools return a not-found error for those instances). This lets a
single instance entry describe both its Prometheus server and its Alertmanager.

### Decision 4 — Separate `alertmanagerBaseTool`

Add a new `alertmanagerBaseTool` (new file `alertmanager_base.go`) holding
`amClients map[string]*alertmanagerClient` + `knownInstances []string` and an
`amClient(instance)` helper. It is built by `newAlertmanagerBaseTool`, which
iterates `Configs` and builds an Alertmanager client **only** for instances
whose `Alertmanager` field is non-nil. This keeps the existing `baseTool`
(Prometheus clients) untouched and avoids forcing every instance to configure
Alertmanager.

### Decision 5 — Single write tool with `operation` discriminator

Rather than 3 separate write tools (create/update/delete), expose **one**
`prometheus_alertmanager_alert_write` tool with a required `operation` param
whose value is `create`, `update`, or `delete`. All three operations share one
`Params` struct, one `Invoke`, and one confirmation gate. This mirrors the
Kubernetes "generic operation" pattern (one verb-tool, operation-discriminated)
and keeps the registry's `writeConstructors` to a single entry.

### Decision 6 — Confirmation gate on the write tool

Reuse `libs/toolkit/confirm.RequireConfirmation(dryRun, confirmed)` (same as
Grafana `dashboard_build`). The write tool accepts `dryRun` and `confirmed`
params; `dryRun=true` returns a JSON preview without calling Alertmanager,
`confirmed=true` performs the POST. This matches the safety middleware pattern
and AGENTS.md security guidance. The gate is enforced **before** the
`operation` switch (so a missing confirmation fails fast for every operation).

### Decision 7 — Delete = resolve via `endsAt` in the past

Alertmanager has no `DELETE /alerts`. The conventional delete/resolve is to
POST the alert again with the same labels and `endsAt` set to `time.Now()`
(Alertmanager marks it resolved and expires it after `resolve_timeout`).
The `delete` operation builds a `postableAlert` with the given labels, empty
annotations, `startsAt = now - 1m`, `endsAt = now`, and POSTs it. This is
idempotent: POSTing a resolve for a non-existent alert still returns 200
`{"status":"success"}`. **No pre-existence check** (avoids an extra GET and
matches Alertmanager semantics).

### Decision 8 — Update = fetch-then-merge-then-upsert

To make "modify" a true partial update (not just a re-POST), the `update`
operation:
1. `GET /api/v2/alerts` with a matcher filter built from the provided labels
   (all states included: `Active=Silenced=Inhibited=true`).
2. If no existing alert matches → error `"no existing alert matches labels"`.
   (Update must not silently create — that is `create`'s job.)
3. If >1 match → use the first; record its `fingerprint` in the output.
   (Alertmanager dedupes by label fingerprint; duplicates indicate a bug.)
4. Merge: start from the existing alert's annotations/startsAt/endsAt/
   generatorURL. Override annotations only if `params.Annotations != nil`
   (replace the whole set). Override startsAt/endsAt/generatorURL only if the
   provided string is non-empty.
5. Validate `EndsAt.After(StartsAt)`.
6. `POST /api/v2/alerts` with the merged alert (upsert by label fingerprint).

## Alertmanager API v2 contract (the implementation relies on this)

### `GET /api/v2/alerts`
Query params (all optional):
- `active` (bool) — include active alerts. Default behavior when omitted
  returns active + unprocessed; we send `active=true` by default.
- `silenced` (bool) — include silenced. Default `false`.
- `inhibited` (bool) — include inhibited. Default `false`.
- `filter` (repeated string) — Alertmanager matchers, e.g.
  `filter=alertname="HighCPU"` (URL-encoded). Multiple `filter` params = AND.
- `receiver` (string) — regex on receiver name.

Response 200: JSON array of `gettableAlert`:
```json
[{
  "labels": {"alertname":"HighCPU","instance":"srv1"},
  "annotations": {"summary":"..."},
  "generatorURL": "http://...",
  "startsAt": "2026-08-17T10:00:00Z",
  "updatedAt": "2026-08-17T10:01:00Z",
  "endsAt":   "2026-08-17T10:30:00Z",
  "fingerprint": "abc123",
  "receivers": [{"name":"slack"}],
  "status": {"state":"active","silencedBy":[],"inhibitedBy":[],"mutedBy":[]}
}]
```
`status.state` ∈ `unprocessed | active | suppressed`.

### `POST /api/v2/alerts`
Request body: JSON array of `postableAlert`:
```json
[{
  "labels": {"alertname":"HighCPU","instance":"srv1"},
  "annotations": {"summary":"..."},
  "startsAt": "2026-08-17T10:00:00Z",
  "endsAt":   "2026-08-17T10:30:00Z",
  "generatorURL": "http://prometheus:9090/..."
}]
```
- `labels` required; `alertname` label required by Alertmanager.
- `startsAt` optional (defaults to now server-side). `endsAt` optional (defaults
  to now + `resolve_timeout` server-side). Both RFC3339.
- Response 200: `{"status":"success"}`. 400 on bad input, 500 on server error.

### Delete mechanism
No `DELETE /alerts`. Re-POST the alert with `endsAt <= now` to resolve it.
Alertmanager expires resolved alerts after `resolve_timeout`.

## Files to create

| Path | Purpose |
|---|---|
| `components/tool/prometheus/alertmanager_client.go` | `alertmanagerClient`, `NewAlertmanagerClient`, `BuildAlertmanagerClients`, `doRequest`, `amHTTPError`, wire types, `ListAlerts`, `PostAlerts` |
| `components/tool/prometheus/alertmanager_base.go` | `alertmanagerBaseTool`, `newAlertmanagerBaseTool`, `amClient` |
| `components/tool/prometheus/alertmanager_alert_list.go` | read tool `prometheus_alertmanager_alert_list` |
| `components/tool/prometheus/alertmanager_alert_write.go` | write tool `prometheus_alertmanager_alert_write` (operation = create/update/delete) |
| `components/tool/prometheus/alertmanager_client_test.go` | client/security tests (error wrapping, redaction, SSRF via address scheme, auth headers) |
| `components/tool/prometheus/alertmanager_alert_list_test.go` | list tool tests (httptest server) |
| `components/tool/prometheus/alertmanager_alert_write_test.go` | write tool tests (per-operation, dryRun, confirmation, validation, errors) |

## Files to modify

| Path | Change |
|---|---|
| `components/tool/prometheus/config.go` | Add `Alertmanager *AlertmanagerConfig` field + `AlertmanagerConfig` struct |
| `components/tool/prometheus/registry.go` | Add `writeConstructors` (ONE entry: the write tool); add list tool to `readOnlyConstructors`; update `NewAllTools`/`WriteToolNames`/`ExtractWriteToolNames`/`NewAllToolsWithSafety`/assertions |
| `components/tool/prometheus/check.go` | Add alertmanager probes (2 components: list + write) + `alertmanagerClientErrorResults`; wire into `Check`/`probeInstance` |
| `components/tool/prometheus/README.md` | Document 2 new tools, `operation` param, Alertmanager config, factory functions, read/write split |
| `components/tool/prometheus/check_test.go` | Update fixed-count assertions if they break (see Risks) |
| `go.mod` / `go.sum` | **No changes** (stdlib + existing deps only) |

---

## Detailed file specs

### `config.go` (modify)

Add to `Config`:
```go
// Alertmanager holds optional Alertmanager connection settings for this
// instance. When nil, the Alertmanager tools are unavailable for this
// instance. Backward compatible: existing Prometheus-only configs are
// unchanged.
Alertmanager *AlertmanagerConfig `validate:"omitempty" jsonschema:"(optional) Alertmanager connection settings. When set, the Alertmanager tools become available for this instance."`
```

Add new struct:
```go
// AlertmanagerConfig holds the connection configuration for an Alertmanager
// instance associated with a Prometheus instance.
type AlertmanagerConfig struct {
    // Address is the Alertmanager server URL (e.g. "http://localhost:9093").
    Address string `validate:"required" jsonschema:"description=Alertmanager server URL, e.g. http://localhost:9093"`
    // Username is the optional username for basic auth.
    Username string `json:"-"`
    // Password is the optional password for basic auth.
    Password string `json:"-"`
    // BearerToken is the optional bearer token for authentication.
    BearerToken string `json:"-"`
    // TLSSkipVerify disables TLS certificate verification.
    TLSSkipVerify bool `json:"-"`
    // Timeout is a Go duration string for per-request timeouts.
    // Defaults to "30s" when empty.
    Timeout string `validate:"omitempty" jsonschema:"description=Per-request timeout (Go duration string, e.g. '30s'), defaults to 30s"`
}
```
Auth fields use `json:"-"` so they never appear in JSON schema output or logs
(matches existing `Config` convention).

### `alertmanager_client.go` (new)

```go
package prometheus

import (
    "context"
    "crypto/tls"
    "io"
    "net/http"
    "net/url"
    "strings"
    "time"

    "emperror.dev/errors"
    "github.com/goccy/go-json"
    "github.com/prometheus/common/model"
    "github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
)

// alertmanagerClient is a minimal HTTP client for the Alertmanager v2 API.
type alertmanagerClient struct {
    baseURL    string
    httpClient *http.Client
    timeout    time.Duration
}

// NewAlertmanagerClient creates an Alertmanager HTTP client from config.
// Reuses authRoundTripper for Basic/Bearer auth and TLS skip verify.
func NewAlertmanagerClient(ctx context.Context, cfg AlertmanagerConfig) (*alertmanagerClient, error)

// BuildAlertmanagerClients builds clients for every instance whose Config
// has a non-nil Alertmanager field. Instances without Alertmanager config are
// skipped (not an error).
func BuildAlertmanagerClients(ctx context.Context, configs Configs) (map[string]*alertmanagerClient, error)
```

Key logic for `NewAlertmanagerClient`:
1. Apply default `Timeout = "30s"` if empty.
2. `validate.Struct(&cfg)` — wrap error with `errors.Wrap(err, "invalid Alertmanager config")`.
3. Require scheme: must start with `http://` or `https://`, else
   `errors.Errorf("Alertmanager address must include scheme (http:// or https://): %s", cfg.Address)`.
4. Parse timeout via `time.ParseDuration`, default 30s.
5. Build transport: if `TLSSkipVerify`, `&http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}` (nolint:gosec), else `http.DefaultTransport`.
6. Wrap with `&authRoundTripper{rt, username, password, bearerToken}` (reuse from `client.go`).
7. Return `&alertmanagerClient{baseURL: strings.TrimRight(cfg.Address, "/"), httpClient: &http.Client{Transport: rt, Timeout: timeout}, timeout: timeout}`.

`doRequest` (mirror grafana's, with `alertmanager` in error messages):
```go
func (c *alertmanagerClient) doRequest(ctx context.Context, method, path string, body io.Reader) ([]byte, int, error)
```
- Per-request timeout via `context.WithTimeout(ctx, c.timeout)`.
- Set `Content-Type: application/json`, `Accept: application/json`. Auth is in the round tripper.
- On status >= 400: return `*amHTTPError{statusCode, method, path, body}` with body truncated to `amMaxErrorBodyLen = 512` (local const; same value as grafana's `maxErrorBodyLen` but defined locally to avoid cross-package coupling).
- Define a local `amHTTPError` type in this file with `errors.As` helper `isAMStatus(err, code)`.

Wire types:
```go
type postableAlert struct {
    Labels       model.LabelSet `json:"labels"`
    Annotations  model.LabelSet `json:"annotations,omitempty"`
    StartsAt     *time.Time     `json:"startsAt,omitempty"`
    EndsAt       *time.Time     `json:"endsAt,omitempty"`
    GeneratorURL string         `json:"generatorURL,omitempty"`
}

type gettableAlert struct {
    Labels       model.LabelSet `json:"labels"`
    Annotations  model.LabelSet `json:"annotations"`
    GeneratorURL string         `json:"generatorURL"`
    StartsAt     time.Time      `json:"startsAt"`
    UpdatedAt    time.Time      `json:"updatedAt"`
    EndsAt       time.Time      `json:"endsAt"`
    Fingerprint  string         `json:"fingerprint"`
    Receivers    []amReceiver   `json:"receivers"`
    Status       amAlertStatus  `json:"status"`
}

type amReceiver struct{ Name string `json:"name"` }

type amAlertStatus struct {
    State       string   `json:"state"`
    SilencedBy  []string `json:"silencedBy"`
    InhibitedBy []string `json:"inhibitedBy"`
    MutedBy     []string `json:"mutedBy"`
}
```

API methods:
```go
type amListAlertsParams struct {
    Active    *bool
    Silenced  *bool
    Inhibited *bool
    Filter    []string // matchers, e.g. `alertname="HighCPU"`
    Receiver  string
}

func (c *alertmanagerClient) ListAlerts(ctx context.Context, p *amListAlertsParams) ([]gettableAlert, error)
func (c *alertmanagerClient) PostAlerts(ctx context.Context, alerts []postableAlert) error
```
- `ListAlerts`: build query string (`url.Values`), `GET /api/v2/alerts`, unmarshal `[]gettableAlert`. Wrap errors with `errors.Wrap(err, "failed to list Alertmanager alerts")`.
- `PostAlerts`: marshal `[]postableAlert`, `POST /api/v2/alerts`, expect 200 `{"status":"success"}`. Wrap errors with `errors.Wrap(err, "failed to post alerts to Alertmanager")`.

### `alertmanager_base.go` (new)

```go
type alertmanagerBaseTool struct {
    amClients      map[string]*alertmanagerClient
    knownInstances []string
}

func (b *alertmanagerBaseTool) amClient(instance string) (*alertmanagerClient, error) {
    c, ok := b.amClients[instance]
    if !ok {
        return nil, instanceNotFoundError(instance, b.knownInstances)
    }
    return c, nil
}

func newAlertmanagerBaseTool(ctx context.Context, configs Configs) (*alertmanagerBaseTool, error) {
    clients, err := BuildAlertmanagerClients(ctx, configs)
    if err != nil {
        return nil, err
    }
    // knownInstances = only instances that actually have an alertmanager client,
    // sorted (use toolutil.SortedKeys on clients map).
    return &alertmanagerBaseTool{amClients: clients, knownInstances: toolutil.SortedKeys(clients)}, nil
}
```
Note: `knownInstances` here is the subset with Alertmanager configured, so the
not-found error lists only valid Alertmanager instances (not all Prometheus ones).

### `alertmanager_alert_list.go` (new, READ tool — not gated)

Description constant `alertmanagerAlertListDescription` (general purpose + output
fields + note that this is a read-only tool, no confirmation needed).

```go
type AlertmanagerAlertListParams struct {
    Instance    string             `json:"instance" validate:"required" jsonschema:"(required) The Prometheus instance to query (must have Alertmanager configured)."`
    Fingerprint string             `json:"fingerprint,omitempty" jsonschema:"(optional) If set, return only the alert with this fingerprint. Takes precedence over AlertFilter/State."`
    Filter      string             `json:"filter,omitempty" jsonschema:"(optional) Go RE2 regex applied on each alert JSON. Keep only alerts that match."`
    State       string             `json:"state,omitempty" validate:"omitempty,oneof=active unprocessed suppressed" jsonschema:"(optional) Filter by Alertmanager alert state: 'active', 'unprocessed', or 'suppressed'."`
    AlertFilter string             `json:"alertFilter,omitempty" jsonschema:"(optional) Alertmanager matcher string passed to the API, e.g. alertname=\"HighCPU\". Multiple matchers can be comma-separated."`
    Paginate    *AlertListPaginate `json:"paginate,omitempty" jsonschema:"(optional) Pagination parameters."`
}
```
Reuses `AlertListPaginate` from `alert_list.go` (same package; client-side index
pagination over the GET response).

```go
type AlertmanagerAlertListOutput struct {
    Labels      model.LabelSet `json:"labels"`
    Annotations model.LabelSet `json:"annotations"`
    State       string         `json:"state"`
    StartsAt    string         `json:"startsAt"`
    EndsAt      string         `json:"endsAt"`
    Fingerprint string         `json:"fingerprint"`
    SilencedBy  []string       `json:"silencedBy"`
    Receivers   []string       `json:"receivers"`
}

type AlertmanagerAlertListTool struct {
    *alertmanagerBaseTool
    tool.InvokableTool
}

func (t *AlertmanagerAlertListTool) Invoke(ctx context.Context, params *AlertmanagerAlertListParams) (result string, err error)
func NewAlertmanagerAlertListTool(ctx context.Context, configs Configs) (*AlertmanagerAlertListTool, error)
```

Invoke logic:
1. Default `Paginate.PageSize = 20` if `Paginate != nil && PageSize == 0`.
2. `validateParams(params)`.
3. `filter.Compile(params.Filter)` (regex on output JSON).
4. `t.amClient(params.Instance)`.
5. Build `amListAlertsParams`:
   - If `params.Fingerprint != ""`: do NOT pass it to the API (Alertmanager v2
     has no `fingerprint` query param). Instead `ListAlerts` with
     `Active=Silenced=Inhibited=boolPtr(true)` (all states) and no filter, then
     filter client-side by `gettableAlert.Fingerprint == params.Fingerprint`.
     If none matches → return `[]` (empty list, not an error — read tool).
   - Else: `Active: boolPtr(true)`; if `params.AlertFilter != ""`, split on
     comma into `Filter []string`; if `params.State == "suppressed"`, set
     `Silenced: boolPtr(true)` (so suppressed alerts are returned by the API
     before the client-side state filter is applied). Otherwise default
     `Silenced=false`.
6. `c.ListAlerts(ctx, p)`.
7. If `Fingerprint` set: filter client-side by fingerprint (single match).
8. Else if `params.State != ""`: filter client-side by `status.state`.
9. Paginate using the same `alertPaginateToken` scheme as `alert_list.go`.
10. For each alert, build `AlertmanagerAlertListOutput` (format `StartsAt`/`EndsAt`
    as RFC3339 `time.RFC3339`), marshal via `marshal.MustMarshal`, apply
    `filter.Match`, append.
11. Append next-page token if more remain; `marshalOutputs`.

`Fingerprint` vs `AlertFilter`/`State` interaction (precise):
- `Fingerprint` takes precedence: when set, `AlertFilter` and `State` are
  **ignored** (the tool fetches all states and matches by fingerprint only).
  Document this in the jsonschema description and the tool description.
- When `Fingerprint` is empty, `AlertFilter` is sent to the API as matchers and
  `State` is applied client-side.

Constructor: `utils.InferTool("prometheus_alertmanager_alert_list", desc + listOutputGuidance, Invoke)`.

### `alertmanager_alert_write.go` (new, WRITE tool — gated, operation-discriminated)

```go
const alertmanagerAlertWriteDescription = `
** General Purpose **
A single tool that creates, updates, or deletes (resolves) an alert on the
Alertmanager associated with a Prometheus instance. The required 'operation'
param selects the action:

- create: POST a new firing alert (endsAt must be in the future).
- update: Fetch an existing alert by labels, merge the provided fields, and
  re-POST (upsert). Errors if no existing alert matches.
- delete: Re-POST the alert with endsAt <= now to resolve it (Alertmanager has
  no DELETE /alerts). Idempotent.

** Safety **
This is a write tool. Always use dryRun=true first to preview the resolved
postableAlert payload (and, for update, the existing alert). After reviewing,
set confirmed=true to perform the POST. Neither dryRun nor confirmed returns
an error.

** Required labels **
All operations require a 'labels' map that includes the 'alertname' label.
`
```

Params struct (single, shared across all operations):
```go
type AlertmanagerAlertWriteParams struct {
    Instance     string            `json:"instance" validate:"required" jsonschema:"(required) The Prometheus instance (must have Alertmanager configured)."`
    Operation    string            `json:"operation" validate:"required,oneof=create update delete" jsonschema:"(required) Operation to perform: 'create', 'update', or 'delete'."`
    Labels       map[string]string `json:"labels" validate:"required,min=1,dive,keys,required,endkeys,required" jsonschema:"(required) Alert labels as key/value pairs. Must include 'alertname'. For create/delete these are the alert's labels; for update these identify the existing alert to modify."`
    Annotations  map[string]string `json:"annotations,omitempty" jsonschema:"(optional) Alert annotations. Used by create and update. For update, omit to keep existing annotations; set to {} or a new map to replace them."`
    StartsAt     string            `json:"startsAt,omitempty" jsonschema:"(optional) Start time in RFC3339. create: defaults to now. update: omit to keep existing. delete: ignored (resolved alert uses now-1m)."`
    EndsAt       string            `json:"endsAt,omitempty" jsonschema:"(optional) End time in RFC3339. create: defaults to now+5m, must be in the future. update: omit to keep existing. delete: ignored (resolved alert uses now)."`
    GeneratorURL string            `json:"generatorURL,omitempty" validate:"omitempty,url" jsonschema:"(optional) URL of the source that generated the alert. create: sets it. update: omit to keep existing. delete: ignored."`
    DryRun       bool              `json:"dryRun,omitempty" jsonschema:"(optional) If true, preview the resolved postableAlert payload without posting."`
    Confirmed    bool              `json:"confirmed,omitempty" jsonschema:"(optional) Must be true to actually post. Set after reviewing the dry-run."`
}
```

Output struct (single, shared):
```go
type AlertmanagerAlertWriteOutput struct {
    Status     string `json:"status"`
    Action     string `json:"action"`               // "created" | "updated" | "deleted"
    Fingerprint string `json:"fingerprint,omitempty"` // set for update (existing alert's fingerprint)
    EndsAt     string `json:"endsAt,omitempty"`       // set for delete (the resolve endsAt, RFC3339)
}
```

```go
type AlertmanagerAlertWriteTool struct {
    *alertmanagerBaseTool
    tool.InvokableTool
}

func (t *AlertmanagerAlertWriteTool) Invoke(ctx context.Context, params *AlertmanagerAlertWriteParams) (result string, err error)
func NewAlertmanagerAlertWriteTool(ctx context.Context, configs Configs) (*AlertmanagerAlertWriteTool, error)
```

Invoke logic (precise order):
1. `validateParams(params)` (runs `validate.Struct`; enforces `operation` oneof
   and `labels` required/non-empty keys/values).
2. `confirm.RequireConfirmation(params.DryRun, params.Confirmed)` — enforced
   **before** the switch so a missing confirmation fails fast for every op.
3. Require `params.Labels["alertname"] != ""` else
   `errors.Errorf("labels must include 'alertname'")`. (Required for all three
   ops; checked once before the switch.)
4. Convert `map[string]string` → `model.LabelSet` (helper `toLabelSet`).
5. Validate `GeneratorURL` parses as URL with http/https scheme if non-empty
   (defense-in-depth; reject other schemes) — `url.Parse` + scheme check. Done
   once before the switch (only `create`/`update` use it, but validating here
   keeps the flow uniform; `delete` simply ignores the field).
6. `switch params.Operation`:

   **case "create":**
   - Parse `StartsAt` (default `time.Now().UTC()`), `EndsAt` (default
     `now + 5m`). Use `time.Parse(time.RFC3339, s)`; wrap parse errors with
     `errors.Wrapf(err, "invalid startsAt/endsAt, expected RFC3339")`.
   - Require `EndsAt.After(time.Now())` (a firing alert must end in the future)
     else `errors.Errorf("endsAt must be in the future for a firing alert")`.
   - Require `EndsAt.After(StartsAt)` else
     `errors.Errorf("endsAt must be after startsAt")`.
   - Build `postableAlert{Labels, Annotations (may be nil), StartsAt, EndsAt, GeneratorURL}`.
   - If `DryRun` → return JSON preview `{"dryRun":true,"operation":"create","alert":<postableAlert>}` (no POST).
   - `t.amClient(params.Instance)`.
   - `c.PostAlerts(ctx, []postableAlert{...})`.
   - Return `marshal.MustMarshal(AlertmanagerAlertWriteOutput{Status:"success", Action:"created"})`.

   **case "update":**
   - Build Alertmanager matcher filter from `Labels`: for each k/v, emit
     `k="escaped_v"` where `escaped_v` replaces `\` → `\\` and `"` → `\"`.
     Join with `,`. Pass as `Filter []string` (single comma-joined string is
     acceptable; alternatively split into multiple `filter` params — either
     works since Alertmanager ANDs them).
   - `t.amClient(params.Instance)`.
   - `c.ListAlerts(ctx, &amListAlertsParams{Active: boolPtr(true), Silenced: boolPtr(true), Inhibited: boolPtr(true), Filter: filter})`.
   - If no match → `errors.Errorf("no existing alert matches labels %v", params.Labels)`.
   - If >1 match → use the first; record `existing := matches[0]` and its
     `fingerprint`. (Alertmanager dedupes by fingerprint; duplicates indicate a
     bug. Optionally include a `note` in the output, but the spec only requires
     `fingerprint`.)
   - Merge: start from `existing` (its labels, annotations, startsAt, endsAt,
     generatorURL). Override:
     - `Annotations`: if `params.Annotations != nil`, replace the whole set.
     - `StartsAt`: if `params.StartsAt != ""`, parse RFC3339 (wrap error);
       else keep `existing.StartsAt`.
     - `EndsAt`: if `params.EndsAt != ""`, parse RFC3339; else keep `existing.EndsAt`.
     - `GeneratorURL`: if `params.GeneratorURL != ""`, use it; else keep
       `existing.GeneratorURL`.
   - Require `EndsAt.After(StartsAt)` else
     `errors.Errorf("endsAt must be after startsAt")`.
   - Build `postableAlert{Labels: existing.Labels, Annotations: mergedAnnotations, StartsAt: mergedStartsAt, EndsAt: mergedEndsAt, GeneratorURL: mergedGeneratorURL}`.
   - If `DryRun` → return JSON preview
     `{"dryRun":true,"operation":"update","existing":<gettableAlert summary>,"merged":<postableAlert>}` (no POST).
   - `c.PostAlerts(ctx, []postableAlert{merged})`.
   - Return `marshal.MustMarshal(AlertmanagerAlertWriteOutput{Status:"success", Action:"updated", Fingerprint: existing.Fingerprint})`.

   **case "delete":**
   - Build `postableAlert{Labels: labelSet, Annotations: nil, StartsAt: now-1m, EndsAt: now}` (resolve).
   - If `DryRun` → return JSON preview
     `{"dryRun":true,"operation":"delete","resolve":<postableAlert>}` (no POST).
   - `t.amClient(params.Instance)`.
   - `c.PostAlerts(ctx, []postableAlert{...})`.
   - Return `marshal.MustMarshal(AlertmanagerAlertWriteOutput{Status:"success", Action:"deleted", EndsAt: now.Format(time.RFC3339)})`.
   - Idempotent: no pre-existence check; POSTing a resolve for a non-existent
     alert still returns 200 success.

Constructor: `utils.InferTool("prometheus_alertmanager_alert_write", desc + writeOutputGuidance, Invoke)`.

### `registry.go` (modify)

Add `writeConstructors` with **one** entry:
```go
var writeConstructors = []toolConstructor{
    func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewAlertmanagerAlertWriteTool(ctx, c) },
}
```
Add the list tool to `readOnlyConstructors`:
```go
func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewAlertmanagerAlertListTool(ctx, c) },
```
Update:
- `NewAllTools` → `buildTools(ctx, configs, append(readOnlyConstructors, writeConstructors...))`.
- `NewReadOnlyTools` → unchanged (still `readOnlyConstructors`, now including the alertmanager list tool).
- `WriteToolNames()` → `return []string{"prometheus_alertmanager_alert_write"}`.
- `ExtractWriteToolNames` → build via `buildTools(ctx, configs, writeConstructors)`, read `Info(ctx).Name` (mirror grafana).
- `NewAllToolsWithSafety` → unchanged logic; `safetyCfg.WriteToolNames` auto-populated from `WriteToolNames()`.
- Update the `var _ tool.InvokableTool` assertions to include the 2 new tool types:
  `(*AlertmanagerAlertListTool)(nil)` and `(*AlertmanagerAlertWriteTool)(nil)`.
- Update comments: "all read-only" → "read + write".

### `check.go` (modify)

Add alertmanager component names and probes. Per instance in `Check`:
1. Existing: build Prometheus client; on fail → `clientErrorResults` (6 prometheus components). On success → `probeInstance` (existing 6 probes).
2. **New**: if `cfg.Alertmanager != nil`, build `NewAlertmanagerClient`; on fail → `alertmanagerClientErrorResults(instance, err)` (**2** alertmanager components: list + write). On success → `probeAlertmanagerInstance(ctx, amClient, instance)`. If `cfg.Alertmanager == nil` → append **2** `StatusLimited` results: `"alertmanager not configured for this instance"`.

New functions:
```go
func alertmanagerClientErrorResults(instance string, err error) checkup.Results // 2 entries: list, write
func probeAlertmanagerInstance(ctx context.Context, c *alertmanagerClient, instance string) checkup.Results
func probeAlertmanagerAlertList(ctx context.Context, c *alertmanagerClient, instance string) checkup.Result
```
`probeAlertmanagerInstance`:
- `probeAlertmanagerAlertList` → real `GET /api/v2/alerts` (read, safe). Status OK / Error.
- `prometheus_alertmanager_alert_write` → `StatusLimited` with message
  `"write tool, not probed to avoid side effects"` (mirror grafana `dashboard_build`).

Component names used in `checkup.Result.Component`:
- `prometheus_alertmanager_alert_list`
- `prometheus_alertmanager_alert_write`

Keep `clientErrorResults` as the Prometheus-client failure path (6 prometheus
components). Alertmanager has its own `alertmanagerClientErrorResults` (2
components). The two services are independent: a Prometheus client failure does
not imply Alertmanager is down.

### `README.md` (modify)

- Update Design section: remove "all read-only" claim; note read + write split
  and Alertmanager.
- Add Alertmanager config example.
- Add **2** rows to the Available Tools table:
  `prometheus_alertmanager_alert_list` (read) and
  `prometheus_alertmanager_alert_write` (write, operation = create/update/delete).
- Add tool detail sections for each new tool with parameter tables. For the
  write tool, document the `operation` param's three values and their distinct
  field semantics (which fields apply to which operation), plus the
  `dryRun`/`confirmed` safety flow.
- Update Factory Functions section: `NewAllTools` now includes the write tool;
  `WriteToolNames` returns `["prometheus_alertmanager_alert_write"]`;
  `NewReadOnlyTools` excludes it.
- Note the confirmation gate (`dryRun`/`confirmed`) on the write tool.

---

## Edge cases & error handling

| Case | Handling |
|---|---|
| Instance has no Alertmanager configured | `amClient` returns `instanceNotFoundError` listing only Alertmanager-enabled instances. |
| Empty alert list | Return `[]` (marshal empty slice, not `null`). |
| `Fingerprint` set, no match | Return `[]` (read tool; not an error). |
| `Fingerprint` set together with `AlertFilter`/`State` | `Fingerprint` wins; `AlertFilter`/`State` ignored. Documented in jsonschema. |
| Invalid `startsAt`/`endsAt` (not RFC3339) | `errors.Wrapf(err, "invalid startsAt/endsAt, expected RFC3339")`. |
| `endsAt <= startsAt` (create/update) | `errors.Errorf("endsAt must be after startsAt")`. |
| `endsAt` in the past for create | `errors.Errorf("endsAt must be in the future for a firing alert")`. |
| Missing `alertname` label | `errors.Errorf("labels must include 'alertname'")` (checked once before the switch). |
| Invalid `operation` value | Rejected by `validate.Struct` via `oneof=create update delete` before the switch runs. |
| Update: no existing alert matches labels | `errors.Errorf("no existing alert matches labels %v", params.Labels)`. |
| Update: multiple matches | Use first; include `fingerprint` in output. (Alertmanager dedupes by fingerprint; duplicates indicate a bug.) |
| Delete: alert doesn't exist | Idempotent — POST resolve still returns 200 success. No pre-check. |
| Neither `dryRun` nor `confirmed` | `confirm.RequireConfirmation` returns `"confirmed must be true to execute (set dryRun=true first to preview)"`. |
| `dryRun=true` | Returns JSON preview, no POST. For update, preview includes existing + merged. |
| Auth failure (401/403) | `*amHTTPError` with status code; surface wrapped error `"failed to post alerts to Alertmanager"` / `"failed to list Alertmanager alerts"`. |
| TLS skip verify | Honored via transport config. |
| `generatorURL` non-URL or non-http scheme | Reject with `errors.Errorf("generatorURL must be an http/https URL")`. |
| Large error body from Alertmanager | Truncated to `amMaxErrorBodyLen` (512) in `amHTTPError`. |
| Secret redaction | Auth fields are `json:"-"` on configs; `alertmanagerClient` does not expose them; tool outputs contain no credentials. List output contains no secrets. Sentinel tests assert no leak. |
| Pagination | Client-side index token (same as `alert_list.go`); empty page → `[]`. |
| Context timeout / cancellation | Propagated via `context.WithTimeout` in `doRequest`; wrapped error. |
| SSRF | Address is operator-configured in `Config` (not user-supplied at invoke). `generatorURL` is validated to http/https URL but is sent to Alertmanager, never fetched by the tool. No user-supplied path segments hit the wire (fixed `/api/v2/alerts`), so no path traversal. |

All errors wrapped with `emperror.dev/errors` and include operation context.

## Validation rules summary

- Every `New...` constructor calls `validate.Struct` (via `validateParams` for tool params, and `NewAlertmanagerClient` calls `validate.Struct(&cfg)`).
- `AlertmanagerConfig.Address` required, must have http/https scheme.
- `AlertmanagerConfig.Timeout` optional Go duration string.
- Tool `Instance` required.
- Write tool `Operation` required, `oneof=create update delete`.
- Write tool `Labels` required, min 1 entry, all keys/values non-empty, must include `alertname` (checked in `Invoke`).
- `State` on list tool: `oneof=active unprocessed suppressed`.
- `GeneratorURL`: `omitempty,url` + manual http/https scheme check.
- `StartsAt`/`EndsAt`: manual RFC3339 parse (no validator tag dependency).

## Tests

### `alertmanager_client_test.go`
- `NewAlertmanagerClient` validation: missing address → error; invalid scheme → error; valid config → ok; default timeout applied.
- `BuildAlertmanagerClients`: skips instances without `Alertmanager`; builds for those with it; error wraps instance name.
- `doRequest` error wrapping: 400 → `*amHTTPError` with status 400; 500 → status 500; body truncated at 512.
- `ListAlerts` happy path via `httptest` server returning a `gettableAlert` array; verifies query params (`active=true`, `filter`).
- `PostAlerts` happy path: verifies POST body is a `[]postableAlert` with correct labels; verifies 200 success.
- Auth: server asserts `Authorization: Bearer <token>` header present when `BearerToken` set; `Authorization: Basic ...` when username/password set.
- Secret redaction: error messages and tool outputs never contain `BearerToken`/`Password` values (sentinel test).
- SSRF/scheme: `NewAlertmanagerClient` rejects `ftp://`, `gopher://`, etc.

### `alertmanager_alert_list_test.go` (httptest)
- Happy path: server returns 2 alerts → tool returns 2 outputs with formatted fields.
- `fingerprint` get-single: server returns 2 alerts, params set `Fingerprint` to one of them → returns exactly 1; unknown fingerprint → `[]`.
- `fingerprint` precedence: set `Fingerprint` + `AlertFilter` + `State` together → only `Fingerprint` applied.
- State filter: only `active` returned.
- `alertFilter` matcher: passed through to server query string.
- Regex `filter` on output JSON.
- Pagination: pageSize 1 → first page 1 result + token; second page next.
- Empty result → `[]`.
- Missing instance → not-found error.
- Invalid state → validation error.
- API error (500) → wrapped error `"failed to list Alertmanager alerts"`.
- Constructor: `NewAlertmanagerAlertListTool(ctx, Configs{})` succeeds (no clients); `Info().Name == "prometheus_alertmanager_alert_list"`.

### `alertmanager_alert_write_test.go` (httptest)
Per-operation happy paths:
- **create** happy path: `confirmed=true`, server asserts POST body has `alertname` label, `endsAt` in future → returns `{"status":"success","action":"created"}`.
- **update** happy path: GET returns one matching alert; POST receives merged annotations/timing; returns `{"status":"success","action":"updated","fingerprint":...}`.
- **delete** happy path: `confirmed=true`, server asserts POST body has `endsAt <= now` (resolved) → returns `{"status":"success","action":"deleted","endsAt":...}`.

DryRun for each operation:
- `operation=create, dryRun=true` → no POST; preview shows `postableAlert`.
- `operation=update, dryRun=true` → no POST; preview shows existing + merged.
- `operation=delete, dryRun=true` → no POST; preview shows resolve `postableAlert`.

Confirmation / validation:
- Neither `dryRun` nor `confirmed` (each operation) → `confirm` error.
- Missing `alertname` (each operation) → error.
- Invalid `operation` (e.g. `"foo"`) → validation error from `validate.Struct`.
- Invalid RFC3339 `startsAt`/`endsAt` (create/update) → error.
- `endsAt` in past for create → error.
- `endsAt <= startsAt` for create/update → error.
- No-match for update → error `"no existing alert matches labels"`.
- Multiple matches for update → uses first, returns its `fingerprint`.
- Delete idempotency: server returns 200 even with no prior GET; no pre-check performed.
- `generatorURL` with `javascript:` scheme → error (create/update).
- Missing instance → not-found (each operation).
- API 400/500 on POST → wrapped error (each operation).
- API error on GET (update) → wrapped error.

Auth & redaction:
- Server asserts Bearer header on POST (write tool) and on GET (update).
- Sentinel test: tool outputs and error strings never contain `BearerToken`/`Password` values.

All tests use `httptest.NewServer` with a `http.HandlerFunc` that mimics the
Alertmanager v2 API. Use `github.com/stretchr/testify/assert`. Mirror the style
of `target_list_test.go` (table-driven where possible) and `security_test.go`
(sentinel-based leak checks).

## Ordered implementation checklist

1. **`config.go`** — Add `AlertmanagerConfig` struct + `Alertmanager *AlertmanagerConfig` field on `Config`. Run `go build ./components/tool/prometheus/...`.
2. **`alertmanager_client.go`** — Implement `alertmanagerClient`, `NewAlertmanagerClient`, `BuildAlertmanagerClients`, `doRequest`, `amHTTPError`, wire types, `ListAlerts`, `PostAlerts`. Reuse `authRoundTripper`. Run `go build`.
3. **`alertmanager_base.go`** — Implement `alertmanagerBaseTool`, `newAlertmanagerBaseTool`, `amClient`. Run `go build`.
4. **`alertmanager_alert_list.go`** — Read tool (list + fingerprint get-single). Run `go build`.
5. **`alertmanager_alert_write.go`** — Write tool with `operation` discriminator (create/update/delete) and confirm gate. Run `go build`.
6. **`registry.go`** — Add `writeConstructors` (ONE entry), add list tool to `readOnlyConstructors`, update `NewAllTools`/`WriteToolNames`/`ExtractWriteToolNames`/`NewAllToolsWithSafety`/assertions. Run `go build`.
7. **`check.go`** — Add `alertmanagerClientErrorResults` (2 entries), `probeAlertmanagerInstance`, `probeAlertmanagerAlertList`; wire into `Check`. Run `go build`.
8. **`alertmanager_client_test.go`** — Client + security tests. Run `go test`.
9. **`alertmanager_alert_list_test.go`** — List tool tests. Run `go test`.
10. **`alertmanager_alert_write_test.go`** — Write tool tests (per-operation, dryRun, confirmation, validation, errors, auth, redaction). Run `go test`.
11. **`README.md`** — Document 2 new tools, `operation` param, config, factory functions, read/write split.
12. **`check_test.go`** — Update `TestCheckInvalidInstance` and `TestCheckClientErrorResults` expected counts if they assert fixed counts (currently 6; with `Alertmanager == nil` an instance now also gets 2 `StatusLimited` alertmanager results → total 8). Review and adjust.

## Verification commands

```sh
go build ./...
go vet ./components/tool/prometheus/...
go test ./components/tool/prometheus/...
```

## Risks / open notes

- **`check_test.go` fixed counts**: `TestCheckInvalidInstance` asserts `len(results) == 6` and `TestCheckClientErrorResults` asserts `len(r) == 6`. After wiring alertmanager, an instance with `Alertmanager == nil` will append **2** `StatusLimited` results → total **8**. These tests must be updated in step 12. (The invalid-instance case `Config{Address:""}` has `Alertmanager == nil`, so 2 limited alertmanager results are added.) If a test constructs an instance *with* `Alertmanager` set and the Alertmanager client build fails, `alertmanagerClientErrorResults` adds 2 error results instead.
- **`EndsAt` exact semantics for delete**: Alertmanager resolves when `endsAt <= now`. Using `time.Now()` is safe; some deployments prefer `now - 1s` to avoid clock-skew edge cases. The plan uses `now` for `EndsAt` and `now - 1m` for `StartsAt` to be safely in the past.
- **Update GET filter**: Alertmanager `filter` matchers use PromQL-like syntax `label="value"`. Values containing `"` or `\` must be escaped. The implementation must escape values when building the matcher string (replace `\` → `\\` and `"` → `\"`).
- **Single write tool param surface**: The shared `AlertmanagerAlertWriteParams` has fields that only apply to some operations (e.g. `Annotations` is ignored by `delete`). The jsonschema descriptions and the tool description must clearly document which fields apply to which `operation`, so callers don't expect `delete` to honor `EndsAt`. This is a usability tradeoff for the smaller tool count.
- **No new dependencies**: confirmed — the HTTP approach needs only stdlib + existing module deps.
