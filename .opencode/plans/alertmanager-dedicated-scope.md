# Plan: Move Alertmanager tools to a dedicated `alertmanager` component (official client)

## Goal

The Alertmanager tools inside `components/tool/prometheus/` use a **different
client** than the Prometheus tools: a hand-rolled `alertmanagerClient` that
talks directly to the Alertmanager v2 API via `net/http`, whereas the
Prometheus tools use the official `promapi.API` from
`github.com/prometheus/client_golang/api/prometheus/v1`. For consistency with
the Prometheus side, the new dedicated `alertmanager` component MUST use the
**official** Alertmanager v2 client
(`github.com/prometheus/alertmanager/api/v2/client`) instead of the hand-rolled
one. The hand-rolled `alertmanager_client.go` is therefore **deleted, not
moved**.

Target state:

- New package `components/tool/alertmanager/` (package `alertmanager`) holding
  the two alert tools + a thin wrapper around the official
  `client.AlertmanagerAPI`, with tool names `alertmanager_alert` (read) and
  `alertmanager_alert_write` (write).
- `components/tool/prometheus/` stripped of all Alertmanager functionality and
  returned to a Prometheus-only component.

## Scope and non-goals

- **Preserve all existing runtime behavior** of the two alert tools and their
  client, EXCEPT:
  - tool-name strings change from `prometheus_*` to `alertmanager_*`;
  - the config surface changes from a nested `Config.Alertmanager
    *AlertmanagerConfig` (on the prometheus `Config`) to a top-level
    `alertmanager.Config` per instance;
  - the HTTP client is replaced by the official Alertmanager v2 client; the
    wire-format is unchanged (same `/api/v2/alerts` endpoints, same JSON
    bodies), so server-side behavior is identical.
- **Do not invent new features** beyond the standard component scaffolding
  required by CONTRIBUTING.md (instance_list, check, registry, README). One
  judgment call is called out below (decision #6): adding
  `alertmanager_instance_list`.
- **No behavior change** to the Prometheus tools (`prometheus_instance_list`,
  `prometheus_metric`, `prometheus_target_list`).

## Verified official-client signatures (pkg.go.dev / GitHub sources relied on)

Cited sources (all fetched for this plan):
- `https://pkg.go.dev/github.com/prometheus/alertmanager/api/v2/client` —
  `AlertmanagerAPI`, `New`, `NewHTTPClient`, `NewHTTPClientWithConfig`,
  `TransportConfig`, `DefaultTransportConfig`, `DefaultBasePath = "/api/v2/"`,
  `DefaultSchemes = ["http"]`.
- `https://pkg.go.dev/github.com/prometheus/alertmanager/api/v2/client/alert` —
  `Client.GetAlerts`, `Client.PostAlerts`, `GetAlertsParams`,
  `PostAlertsParams`, `GetAlertsOK.GetPayload() models.GettableAlerts`.
- `https://pkg.go.dev/github.com/prometheus/alertmanager/api/v2/models` —
  `GettableAlert`, `PostableAlert`, `PostableAlerts`, `LabelSet`,
  `AlertStatus`, `ReceiverReference`.
- `https://pkg.go.dev/github.com/go-openapi/runtime/client` — `New`,
  `NewWithClient`, `BasicAuth`, `BearerToken`, `Compose`, `Runtime` struct
  (`Transport http.RoundTripper`, `DefaultAuthentication
  runtime.ClientAuthInfoWriter`, `Host`, `BasePath`).
- `https://pkg.go.dev/github.com/go-openapi/runtime` — `ClientTransport`
  interface (`Submit(*ClientOperation) (any, error)`), `ClientResponse`
  interface (`Code() int`, `Message() string`, `GetHeader(string) string`,
  `GetHeaders(string) []string`, `Body() io.ReadCloser`), `APIError` struct
  (`OperationName string`, `Response any`, `Code int`), `ClientAuthInfoWriter`,
  `ClientOperation` (`AuthInfo ClientAuthInfoWriter`, `Client *http.Client`,
  `Context context.Context`).
- `https://raw.githubusercontent.com/prometheus/alertmanager/v0.34.0/api/v2/client/alert/get_alerts_responses.go`
  — confirms `ReadResponse` returns `(nil, *GetAlertsBadRequest)` /
  `(nil, *GetAlertsInternalServerError)` for 400/500 (these types implement
  `error` and carry `Payload string`), and
  `runtime.NewAPIError("[GET /alerts] getAlerts", response, code)` for unknown
  status codes.
- `https://raw.githubusercontent.com/prometheus/alertmanager/v0.34.0/api/v2/models/postable_alert.go`
  — `PostableAlert{ Annotations LabelSet, EndsAt strfmt.DateTime, StartsAt
  strfmt.DateTime, Alert }` where embedded `Alert{ GeneratorURL strfmt.URI,
  Labels LabelSet }`.
- `https://raw.githubusercontent.com/prometheus/alertmanager/v0.34.0/api/v2/models/postable_alerts.go`
  — `type PostableAlerts []*PostableAlert`.
- `https://raw.githubusercontent.com/prometheus/alertmanager/v0.34.0/api/v2/models/receiver_reference.go`
  — `ReceiverReference{ Name *string }` (Required).
- `https://raw.githubusercontent.com/prometheus/alertmanager/v0.34.0/api/v2/models/label_set.go`
  — `type LabelSet map[string]string`.
- `https://raw.githubusercontent.com/prometheus/alertmanager/v0.34.0/api/v2/models/gettable_alert.go`
  (via pkg.go.dev) — `GettableAlert{ Annotations LabelSet, EndsAt
  *strfmt.DateTime, Fingerprint *string, Receivers []*ReceiverReference,
  StartsAt *strfmt.DateTime, Status *AlertStatus, UpdatedAt *strfmt.DateTime,
  Alert }`; `AlertStatus{ InhibitedBy []string, MutedBy []string, SilencedBy
  []string, State *string }`.
- `https://raw.githubusercontent.com/go-openapi/runtime/v0.33.0/client/runtime.go`
  — `Runtime.SubmitContext` calls `operation.Reader.ReadResponse(...)` and
  returns its `(any, error)` verbatim; `prepareRequest` uses
  `r.DefaultAuthentication` when `operation.AuthInfo == nil` (and the request
  has no `Authorization` header yet); `NewWithClient(host, basePath, schemes,
  *http.Client)` lets us inject a custom `*http.Client` (TLS skip + timeout).
- `https://raw.githubusercontent.com/go-openapi/runtime/v0.33.0/client_response.go`
  — `APIError.Error()` renders `"%s (status %d): %s"` where the body comes from
  `o.Response` (the `runtime.ClientResponse` for the default-case path; for
  the 400/500 typed paths the error is the generated response struct, not an
  `*APIError`).
- `https://github.com/prometheus/alertmanager/blob/v0.34.0/go.mod` —
  dependency enumeration (see "go.mod change" below).

Key exact signatures used by the wrapper:

```go
// alert sub-service
func (a *alert.Client) GetAlerts(params *alert.GetAlertsParams, opts ...alert.ClientOption) (*alert.GetAlertsOK, error)
func (a *alert.Client) PostAlerts(params *alert.PostAlertsParams, opts ...alert.ClientOption) (*alert.PostAlertsOK, error)
func (o *alert.GetAlertsOK) GetPayload() models.GettableAlerts   // == []*models.GettableAlert

// GetAlertsParams fields (all exported, set via Set* / With*)
type GetAlertsParams struct {
    Active           *bool
    Filter           []string
    Inhibited         *bool
    Receiver         *string
    ReceiverMatchers []string
    Silenced         *bool
    Unprocessed      *bool
    Context          context.Context
    HTTPClient       *http.Client
    Timeout          time.Duration
}
func alert.NewGetAlertsParamsWithContext(ctx context.Context) *alert.GetAlertsParams

// PostAlertsParams
func (o *alert.PostAlertsParams) SetAlerts(alerts models.PostableAlerts)
func alert.NewPostAlertsParamsWithContext(ctx context.Context) *alert.PostAlertsParams

// top-level client
func client.New(transport runtime.ClientTransport, formats strfmt.Registry) *client.AlertmanagerAPI
type client.AlertmanagerAPI struct {
    Alert      alert.ClientService
    Alertgroup alertgroup.ClientService
    General    general.ClientService
    Receiver   receiver.ClientService
    Silence    silence.ClientService
    Transport  runtime.ClientTransport
}
type client.TransportConfig struct { Host, BasePath string; Schemes []string }
const client.DefaultBasePath = "/api/v2/"

// go-openapi runtime/client (aliased import)
func httpclient.NewWithClient(host, basePath string, schemes []string, client *http.Client) *httpclient.Runtime
func httpclient.BasicAuth(username, password string) runtime.ClientAuthInfoWriter
func httpclient.BearerToken(token string) runtime.ClientAuthInfoWriter
func httpclient.Compose(auths ...runtime.ClientAuthInfoWriter) runtime.ClientAuthInfoWriter
// (*httpclient.Runtime) has exported field: DefaultAuthentication runtime.ClientAuthInfoWriter

// runtime (go-openapi)
type runtime.ClientTransport interface { Submit(*runtime.ClientOperation) (any, error) }
type runtime.APIError struct { OperationName string; Response any; Code int }
```

`strfmt.DateTime` is `type DateTime time.Time`; convert with
`time.Time(dt)` / `strfmt.DateTime(t)`. Its zero value renders as
`"0001-01-01T00:00:00.000Z"`; the conversion below uses
`time.Time(*a.StartsAt).Format(time.RFC3339)` to preserve the existing
second-precision RFC3339 output and to render the zero value as
`"0001-01-01T00:00:00Z"` (matching `time.Time{}.Format(time.RFC3339)`).

## Key design decisions (definitive)

### 1. Config surface

The new `alertmanager` package gets its own `Configs map[string]Config` and a
top-level `Config` struct whose fields are exactly the current
`prometheus.AlertmanagerConfig` fields, with identical tags. The prometheus
`Config` DROPS its `Alertmanager *AlertmanagerConfig` field, and the
`AlertmanagerConfig` type is removed from the prometheus package entirely.

Rationale: natural consequence of moving to a dedicated scope. This is a
**breaking change** for prometheus users who configured `Config.Alertmanager`;
they must now use the separate `alertmanager.Configs` map. Documented in both
READMEs.

### 2. Tool naming and type/constructor names

- `prometheus_alert` → `alertmanager_alert`
- `prometheus_alert_write` → `alertmanager_alert_write`

Keep the generic Go type/constructor names (`AlertTool`/`NewAlertTool`,
`AlertWriteTool`/`NewAlertWriteTool`): they are now in package `alertmanager`,
so `alertmanager.AlertTool` is unambiguous and the names match the existing
code verbatim (less churn). Introduce two named constants in `base.go`:
`alertToolName = "alertmanager_alert"` and
`alertWriteToolName = "alertmanager_alert_write"`, and use them in
`NewAlertTool`, `NewAlertWriteTool`, `check.go`, and `registry.go` (replacing
the current string literal `"prometheus_alert"` and the old
`alertWriteToolName = "prometheus_alert_write"`).

`WriteToolNames()` returns `[]string{alertWriteToolName}` i.e.
`["alertmanager_alert_write"]`.

### 3. Client — use the OFFICIAL Alertmanager v2 client (replaces `authRoundTripper` duplication)

The previous plan duplicated `authRoundTripper` into the new package. That
decision is **REMOVED**. The new `alertmanager` component wraps the official
`github.com/prometheus/alertmanager/api/v2/client.AlertmanagerAPI` and uses
`github.com/go-openapi/runtime/client`'s `BasicAuth` / `BearerToken`
auth writers (Bearer takes priority over Basic — same semantics as the old
`authRoundTripper`). The prometheus package keeps its own `authRoundTripper`
unchanged (still used by prometheus `NewClient`); the alertmanager package no
longer references it.

Rationale: the user's directive is consistency with Prometheus, which uses the
official `prometheus/client_golang` client. The official Alertmanager client
(`github.com/prometheus/alertmanager/api/v2/client`, generated by go-swagger)
gives us the same benefit: maintained wire types, model validation, and a
transport interface we can decorate for secret redaction. The hand-rolled
`alertmanager_client.go` is deleted.

### 4. `listOutputGuidance` embed — duplicate the prompt file

`prompts/list_output_guidance.md` is embedded by prometheus `helper.go` and
used by prometheus `metric.go` and `target_list.go` (and currently `alert.go`).
Decision: **copy** the file verbatim into
`components/tool/alertmanager/prompts/list_output_guidance.md` and embed it in
the new `alertmanager/helper.go`. The prometheus copy stays (still used by
metric + target_list).

Rationale: self-contained component (CONTRIBUTING.md prompts convention: store
prompts under the component's own `prompts/` dir). Avoids a cross-package
embed dependency.

### 5. Shared helpers — duplicate per component (follow precedent)

The generically-named helpers `marshalOutputs`, `marshalString`,
`instanceNotFoundError`, `validateParams`, `parseRFC3339` are small and used
by both prometheus and alertmanager tools. Decision: **duplicate** them in the
new `alertmanager/helper.go` (adapting `instanceNotFoundError` to say
"Alertmanager instance"). The alertmanager-specific helpers
(`AlertPaginate`, `alertPaginateToken`, `paginateWindow`, `nextPageToken`,
`receiverNames`) **move** to the alertmanager package and are **removed** from
prometheus `helper.go`. `receiverNames` is adapted to the official
`[]*models.ReceiverReference` type (see "Tool-layer adaptations" below).

Rationale: the existing repo duplicates these per component (grafana has its
own `instanceNotFoundError`, `validateParams`, etc.); each component is
self-contained. `parsePromQLDuration` is Prometheus-only and stays in
prometheus.

### 6. `instance_list` tool — YES, add `alertmanager_instance_list`

Decision: **add** an `alertmanager_instance_list` read tool mirroring
grafana/prometheus `instance_list.go`.

Justification: (a) every tool component in this repo (grafana, prometheus,
argocd, kubernetes) exposes an `*_instance_list` tool — it is the standard
discovery pattern; (b) without it, users have no way to discover configured
Alertmanager instances except by trial-and-error on the alert tool; (c) it is
a trivial read tool with no client dependency (just
`configs.GetInstanceNames()`), so it adds no risk; (d) CONTRIBUTING.md's
component-completeness checklist expects the standard pattern. This is
standard scaffolding, not a new behavioral feature of the existing alert
tools. It is called out explicitly here as a judgment call beyond the strict
"move".

### 7. Checkup

New `components/tool/alertmanager/check.go`:

- `Check(ctx context.Context, configs Configs) checkup.Results`.
- Empty/nil configs → single error result `{Component: "alertmanager",
  Status: StatusError, Error: "no Alertmanager instances configured"}`.
- For each instance (sorted): build client via `NewClient(ctx, cfg)` with a
  10s timeout context.
  - On client-build error → `clientErrorResults(instance, err)` returning 3
    error results for `alertmanager_instance_list`, `alertmanager_alert`,
    `alertmanager_alert_write`.
  - On success → append:
    - `{Component: "alertmanager_instance_list", Instance, StatusOK}` (always
      ok, like grafana/prometheus instance_list probe);
    - `probeAlert(ctx, client, instance)` — real `ListAlerts` with
      `Active: boolPtr(true)` (all other params nil), returning `StatusOK`
      with `"%d alerts found, RBAC ok"` or `StatusError` with
      `errors.Wrap(err, "failed to list Alertmanager alerts").Error()`;
    - `{Component: alertWriteToolName, Instance, StatusLimited, Message:
      "write tool, not probed to avoid side effects"}`.
- `clientErrorResults(instance, err)` returns 3 results (one per component
  name) with `StatusError` and `errStr`.
- `allComponentNames()` returns
  `["alertmanager_instance_list", "alertmanager_alert", "alertmanager_alert_write"]`.

Note: `probeAlert` now calls the NEW `ListAlerts` wrapper over the official
`GetAlerts` (see client spec).

Prometheus `check.go` AFTER removal:

- `Check` returns ONLY the 3 prometheus results per instance
  (`prometheus_instance_list`, `prometheus_metric`, `prometheus_target_list`).
- DELETE `alertmanagerClientErrorResults` and `probeAlert`.
- `clientErrorResults` stays at 3 results (unchanged).
- DELETE the entire `if cfg.Alertmanager != nil { ... } else { ... }` block
  from the per-instance loop. The loop body becomes: build prometheus client;
  on error append `clientErrorResults(instance, err)`; on success append
  `probeInstance(...)`.
- `promCheckTimeout` const stays.

### 8. registry.go

New `components/tool/alertmanager/registry.go` (mirror grafana):

- `type toolConstructor func(context.Context, Configs) (tool.InvokableTool, error)`.
- `readOnlyConstructors` = `NewInstanceListTool`, `NewAlertTool`.
- `writeConstructors` = `NewAlertWriteTool`.
- `buildTools`, `NewAllTools`, `NewReadOnlyTools`, `WriteToolNames`,
  `ExtractWriteToolNames`, `NewAllToolsWithSafety` — mirror grafana signatures
  and logic (error message prefix `"failed to create alertmanager tool %d"`).
- Compile-time assertions:
  `var _ tool.InvokableTool = (*InstanceListTool)(nil)`,
  `(*AlertTool)(nil)`, `(*AlertWriteTool)(nil)`.

Prometheus `registry.go` AFTER removal:

- Remove `NewAlertTool` from `readOnlyConstructors`.
- Remove `NewAlertWriteTool` from `writeConstructors` (becomes empty slice).
- Remove `(*AlertTool)(nil)` and `(*AlertWriteTool)(nil)` assertions.
- `WriteToolNames()` returns `[]string{}` (empty slice). Document in a comment
  that Prometheus currently has no write tools; the function remains for API
  stability and safety-middleware wiring. `NewAllToolsWithSafety` still works:
  when `safetyCfg.WriteToolNames` is empty it stays empty (no write tools to
  gate); `safety.New` must accept an empty write-tool list (it already does —
  grafana/prometheus both call it; verify no panic, see verification checklist).
- `buildTools` error prefix stays `"failed to create prometheus tool %d"`.

### 9. Tests — see "Test changes" section below.

### 10. README — see "README changes" section below.

---

## go.mod change

Add `github.com/prometheus/alertmanager v0.34.0` as a direct require. Exact
command:

```bash
go get github.com/prometheus/alertmanager@v0.34.0
go mod tidy
```

New direct dependencies pulled by `github.com/prometheus/alertmanager/api/v2/{client,models}` (verified against https://github.com/prometheus/alertmanager/blob/v0.34.0/go.mod):

- `github.com/go-openapi/runtime v0.33.0` — **NEW** direct (we import
  `github.com/go-openapi/runtime/client` and `github.com/go-openapi/runtime`).
- `github.com/go-openapi/strfmt v0.27.0` — **NEW** (used for
  `strfmt.Registry`, `strfmt.DateTime`, `strfmt.URI`, `strfmt.Default`).
- `github.com/go-openapi/errors v0.22.8` — **NEW** indirect (pulled by models
  validation).
- `github.com/go-openapi/validate v0.26.1` — **NEW** indirect (pulled by
  models validation).
- `github.com/go-openapi/loads v0.25.0` — **NEW** indirect (pulled by client
  init).
- `github.com/go-openapi/spec v0.22.9` — **NEW** indirect (pulled by loads).
- `github.com/go-openapi/analysis v0.25.5` — **NEW** indirect (pulled by
  loads).
- `github.com/go-openapi/swag v0.28.0` — already present as indirect
  (`v0.25.4`); `go mod tidy` will bump/keep as needed. The sub-packages
  `github.com/go-openapi/swag/cmdutils`, `conv`, `fileutils`, `jsonname`,
  `jsonutils`, `loading`, `mangling`, `netutils`, `pools`, `stringutils`,
  `typeutils`, `yamlutils` are already indirect in the repo go.mod.
- `github.com/go-openapi/jsonpointer` — already indirect in repo go.mod
  (`v0.22.4`); alertmanager pins `v1.0.0` indirectly; `go mod tidy` resolves.
- `github.com/go-openapi/jsonreference` — already indirect in repo go.mod
  (`v0.21.4`); alertmanager pins `v1.0.0` indirectly; `go mod tidy` resolves.
- `github.com/mitchellh/mapstructure` — note: alertmanager v0.34.0 actually
  uses `github.com/go-viper/mapstructure/v2 v2.5.0` (indirect). The repo does
  NOT currently have it; `go mod tidy` adds it as indirect.
- `github.com/asaskevich/govalidator` — pulled indirectly by
  `go-openapi/validate` (transitive). `go mod tidy` adds it as indirect if not
  already present (it is NOT in the current repo go.mod).
- `github.com/oklog/ulid/v2 v2.1.2` — pulled indirectly by alertmanager
  (transitive). NOT in current repo go.mod; `go mod tidy` adds as indirect.
  (Note: alertmanager v0.34.0 uses `github.com/oklog/ulid/v2`, not the older
  `github.com/oklog/ulid`.)
- `github.com/go-viper/mapstructure/v2`, `google.golang.org/grpc`,
  `connectrpc.com/connect`, `github.com/twmb/franz-go`, etc. are pulled by
  alertmanager's server/cli code but `go mod tidy` will only retain what is
  actually imported by the packages we use (`api/v2/client` + `api/v2/models`
  + `go-openapi/runtime`); most server-only deps will NOT be retained. Verify
  after `go mod tidy` that the diff is limited to the go-openapi stack +
  `oklog/ulid/v2` + `go-viper/mapstructure/v2` + `asaskevich/govalidator`.

The repo ALREADY has `go-openapi/swag`, `jsonpointer`, `jsonreference` as
indirect (see current go.mod lines 117–130) — note this in the PR description
so reviewers do not flag them as newly added.

---

## Symbol map (old → new / removed)

### Tool-name strings
| Old | New |
|---|---|
| `"prometheus_alert"` | `"alertmanager_alert"` (`alertToolName`) |
| `"prometheus_alert_write"` (`alertWriteToolName`) | `"alertmanager_alert_write"` (`alertWriteToolName`) |

### Client-layer symbols (REWRITTEN — official client)
| Old (package `prometheus`, hand-rolled) | New (package `alertmanager`, official-client wrapper) |
|---|---|
| `alertmanagerClient` (hand-rolled: `baseURL`, `httpClient`, `timeout`, `redactSecrets`) | `alertmanagerClient` (wrapper: `api *client.AlertmanagerAPI`, `timeout time.Duration`, `redactSecrets []string`) |
| `NewAlertmanagerClient(ctx, AlertmanagerConfig)` | `NewClient(ctx context.Context, config Config) (*alertmanagerClient, error)` |
| `BuildAlertmanagerClients(ctx, Configs)` (nil-skip) | `BuildClients(ctx context.Context, configs Configs) (map[string]*alertmanagerClient, error)` (builds for every instance) |
| `doRequest`, `amHTTPError`, `isAMStatus`, `amMaxErrorBodyLen` (doRequest constant) | **REMOVED**. `amMaxErrorBodyLen = 512` is **kept** as a package const used by the redacting transport. `amHTTPError`/`isAMStatus`/`doRequest` are gone (the official client produces `*alert.GetAlertsBadRequest` etc.). |
| `buildRedactSecrets`, `redactSecret` | **kept verbatim** (used by the redacting transport) |
| `boolPtr` | **kept** (used to build `*bool` for `GetAlertsParams.Active/Silenced/Inhibited`) |
| `toLabelSet` (map→`model.LabelSet`) | **REMOVED** — `models.LabelSet` IS `map[string]string`; tool layer uses `models.LabelSet(m)` directly |
| `postableAlert`, `gettableAlert`, `amReceiver`, `amAlertStatus`, `amListAlertsParams` (wire types) | **REMOVED** — replaced by official `models.PostableAlert`, `*models.GettableAlert`, `*models.ReceiverReference`, `*models.AlertStatus`, and a small wrapper params struct `listAlertsParams` (see client spec) |
| `ListAlerts(ctx, *amListAlertsParams) ([]gettableAlert, error)` | `ListAlerts(ctx context.Context, p *listAlertsParams) ([]*models.GettableAlert, error)` — wraps `c.api.Alert.GetAlerts` |
| `PostAlerts(ctx, []postableAlert) error` | `PostAlerts(ctx context.Context, alerts models.PostableAlerts) error` — wraps `c.api.Alert.PostAlerts` |
| `authRoundTripper` (duplicated from prometheus) | **NOT duplicated** — replaced by `httpclient.BasicAuth`/`httpclient.BearerToken` via `(*httpclient.Runtime).DefaultAuthentication`. A new `redactingTransport` type implements `runtime.ClientTransport` for secret redaction (see client spec). |
| — (new) | `redactingTransport` struct + `Submit(*runtime.ClientOperation) (any, error)` + `redactAPIErrorPayload` helper |

### Tool-layer symbols (UNCHANGED from previous plan — names kept; internal model types change, see "Tool-layer adaptations")
| Old (package `prometheus`) | New (package `alertmanager`) |
|---|---|
| `alertmanagerBaseTool` | `baseTool` (field `amClients` → `clients`; method `amClient` → `client`) |
| `newAlertmanagerBaseTool` | `newBaseTool` |
| `alertWriteToolName` const | `alertWriteToolName = "alertmanager_alert_write"` + new `alertToolName = "alertmanager_alert"` |
| `AlertTool`, `AlertParams`, `AlertOutput`, `alertDescription`, `NewAlertTool` | same (tool name string changes; `AlertOutput.Labels`/`Annotations` type changes from `model.LabelSet` to `models.LabelSet` — see adaptations) |
| `AlertWriteTool`, `AlertWriteParams`, `AlertWriteOutput`, `alertWriteToolDescription`, `NewAlertWriteTool` | same |
| `buildMatcherFilter`, `validateMatcherLabelKeys`, `coalesceTime`, `validateGeneratorURL`, `postAlert` | same (internal model-type changes — see adaptations) |
| `AlertPaginate`, `alertPaginateToken`, `paginateWindow`, `nextPageToken`, `receiverNames` | same (moved out of prometheus; `receiverNames` adapted to `[]*models.ReceiverReference`) |
| `marshalOutputs`, `marshalString`, `instanceNotFoundError`, `validateParams`, `parseRFC3339` | duplicated (instanceNotFoundError says "Alertmanager instance") |
| `listOutputGuidance` embed | duplicated from `prompts/list_output_guidance.md` |
| `AlertmanagerConfig` (from prometheus `config.go`) | `Config` (top-level) |
| — (new) | `Configs`, `Config`, `GetConfig`, `GetInstanceNames` |
| — (new) | `InstanceListTool`, `InstanceListParams`, `NewInstanceListTool`, `instanceListDescription` |
| — (new) | `Check`, `clientErrorResults`, `probeAlert`, `allComponentNames` |
| — (new) | `readOnlyConstructors`, `writeConstructors`, `buildTools`, `NewAllTools`, `NewReadOnlyTools`, `WriteToolNames`, `ExtractWriteToolNames`, `NewAllToolsWithSafety` |

### Removed from `components/tool/prometheus/`
- Files deleted: `alert.go`, `alert_write.go`, `alertmanager_client.go`,
  `alertmanager_base.go`, `alert_test.go`, `alert_write_test.go`,
  `alertmanager_client_test.go`.
- `config.go`: remove `Alertmanager *AlertmanagerConfig` field and the whole
  `AlertmanagerConfig` type.
- `helper.go`: remove `AlertPaginate`, `alertPaginateToken`, `paginateWindow`,
  `nextPageToken`, `receiverNames`. Keep `listOutputGuidance`, `marshalOutputs`,
  `marshalString`, `instanceNotFoundError`, `validateParams`, `parseRFC3339`,
  `parsePromQLDuration`.
- `check.go`: remove `alertmanagerClientErrorResults`, `probeAlert`, and the
  Alertmanager branch in the per-instance loop.
- `registry.go`: remove `NewAlertTool`/`NewAlertWriteTool` from constructor
  lists; remove `(*AlertTool)(nil)`/`(*AlertWriteTool)(nil)` assertions;
  `WriteToolNames()` returns `[]string{}`.
- `client.go`: `authRoundTripper` STAYS (still used by prometheus `NewClient`).
- `prometheus_test.go`: remove `TestAlertmanagerConfigDecoupling`; rewrite
  `TestValidateParams` to use a prometheus params type instead of `AlertParams`.
- `check_test.go`: update `TestCheckInvalidInstance` (5 → 3 results); remove
  `TestAlertmanagerClientErrorResults`.
- `README.md`: remove the two alert tools, the `Alertmanager` sub-config, and
  the alert-related breaking-change entries; add a new breaking-change note.

---

## New package file-by-file spec: `components/tool/alertmanager/`

All files use `package alertmanager` and start with a package comment in
`config.go`:
```go
// Package alertmanager provides eino tools for managing alerts on
// Alertmanager instances via the Alertmanager v2 HTTP API.
package alertmanager
```
No license banner (CONTRIBUTING.md). All errors wrapped with
`emperror.dev/errors`.

### `components/tool/alertmanager/config.go`
- Package comment (above).
- `type Configs map[string]Config`.
- `type Config struct` with fields copied verbatim from the current
  `prometheus.AlertmanagerConfig` (same names, types, tags):
  - `Address string \`validate:"required" jsonschema:"description=Alertmanager server URL, e.g. http://localhost:9093"\``
  - `Username string \`json:"-"\``
  - `Password string \`json:"-"\``
  - `BearerToken string \`json:"-"\``
  - `TLSSkipVerify bool \`json:"-"\``
  - `Timeout string \`validate:"omitempty" jsonschema:"description=Per-request timeout (Go duration string, e.g. '30s'), defaults to 30s"\``
- `func (c Configs) GetConfig(instanceName string) Config { return c[instanceName] }`.
- `func (c Configs) GetInstanceNames() []string { return toolutil.SortedKeys(c) }`
  (import `github.com/webcenter-fr/eino-ext/libs/toolkit/toolutil`).

### `components/tool/alertmanager/client.go`

This file is **new** (not a copy of the hand-rolled one). It wraps the official
Alertmanager v2 client. Imports:

```go
import (
    "context"
    "crypto/tls"
    "encoding/base64"
    "net/http"
    "net/url"
    "strings"
    "time"

    "emperror.dev/errors"
    "github.com/go-openapi/runtime"
    httpclient "github.com/go-openapi/runtime/client"
    "github.com/go-openapi/strfmt"
    "github.com/prometheus/alertmanager/api/v2/client"
    "github.com/prometheus/alertmanager/api/v2/client/alert"
    "github.com/prometheus/alertmanager/api/v2/models"
    "github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
)
```

#### `alertmanagerClient` wrapper struct

```go
// alertmanagerClient wraps the official Alertmanager v2 client
// (github.com/prometheus/alertmanager/api/v2/client.AlertmanagerAPI) and
// carries the redaction secrets and timeout used by the redacting transport.
type alertmanagerClient struct {
    api           *client.AlertmanagerAPI
    timeout       time.Duration
    redactSecrets []string
}
```

#### `amMaxErrorBodyLen` constant (preserved)

```go
// amMaxErrorBodyLen caps how much of a non-2xx response body is included in
// the redacted error message surfaced to callers. Preserved from the
// hand-rolled client so a server echoing the Authorization header cannot leak
// large or sensitive payloads (CWE-532 / CWE-200).
const amMaxErrorBodyLen = 512
```

#### `buildRedactSecrets` / `redactSecret` — copied verbatim

Copy `buildRedactSecrets(username, password, bearerToken string) []string`
and `redactSecret(s string, secrets []string) string` verbatim from the old
`alertmanager_client.go`. They are used by `redactingTransport`.

#### `NewClient` constructor

Signature: `func NewClient(ctx context.Context, config Config) (*alertmanagerClient, error)`.

Step-by-step logic (error wrap messages preserved verbatim where behavior is
preserved):

1. `if config.Timeout == "" { config.Timeout = "30s" }`.
2. `if err := validate.Struct(&config); err != nil { return nil, errors.Wrap(err, "invalid Alertmanager config") }`.
3. Scheme enforcement (SSRF): `if !strings.HasPrefix(config.Address, "http://") && !strings.HasPrefix(config.Address, "https://") { return nil, errors.Errorf("Alertmanager address must include scheme (http:// or https://): %s", config.Address) }`.
4. Parse timeout: `timeout, err := time.ParseDuration(config.Timeout); if err != nil { return nil, errors.Wrapf(err, "invalid Alertmanager timeout value: %s", config.Timeout) }`.
5. Parse address into host / basePath / scheme:
   ```go
   u, err := url.Parse(config.Address)
   if err != nil {
       return nil, errors.Wrapf(err, "invalid Alertmanager address: %s", config.Address)
   }
   // u.Scheme is already guaranteed http/https by step 3.
   host := u.Host // host:port, no scheme, no path
   // Map any user-supplied path prefix onto the API base path. The official
   // client's DefaultBasePath is "/api/v2/"; we preserve a user prefix such
   // as "http://am:9093/prefix" -> basePath "/prefix/api/v2/".
   prefix := strings.Trim(u.Path, "/")
   basePath := "/api/v2/"
   if prefix != "" {
       basePath = "/" + prefix + "/api/v2/"
   }
   schemes := []string{u.Scheme}
   ```
   Rationale: the official client joins `host + basePath + operation path`
   (`/alerts`). The old client did `TrimRight(Address, "/") + "/api/v2/alerts"`,
   which did NOT support a path prefix. The new mapping supports an optional
   prefix and always ends `basePath` with `/` (the runtime prepends `/` if
   missing and expects a trailing slash before the operation path). An
   address with no path (`http://am:9093`) yields `basePath = "/api/v2/"` ==
   `client.DefaultBasePath`.
6. Build the `*http.Client` with TLS skip + timeout:
   ```go
   httpClient := &http.Client{Timeout: timeout}
   if config.TLSSkipVerify {
       httpClient.Transport = &http.Transport{
           TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
       }
   }
   ```
7. Build the go-openapi runtime with our `*http.Client`:
   ```go
   rt := httpclient.NewWithClient(host, basePath, schemes, httpClient)
   ```
8. Auth — Bearer takes priority over Basic (mirrors old `authRoundTripper`):
   ```go
   var auth runtime.ClientAuthInfoWriter
   switch {
   case config.BearerToken != "":
       auth = httpclient.BearerToken(config.BearerToken)
   case config.Username != "" || config.Password != "":
       auth = httpclient.BasicAuth(config.Username, config.Password)
   }
   if auth != nil {
       rt.DefaultAuthentication = auth
   }
   ```
   Note: `(*httpclient.Runtime).DefaultAuthentication` is applied by the
   runtime when an operation has no `AuthInfo` and the request has no
   `Authorization` header yet (verified in `runtime.go prepareRequest`). The
   Alertmanager v2 spec declares no security scheme, so `operation.AuthInfo`
   is nil and `DefaultAuthentication` is used. Setting Bearer-only (not
   `Compose(Basic, Bearer)`) reproduces the old "Bearer overwrites Basic"
   semantics exactly: when Bearer is set, Basic is never configured.
9. Wrap the runtime with the redacting transport:
   ```go
   secrets := buildRedactSecrets(config.Username, config.Password, config.BearerToken)
   redacting := &redactingTransport{base: rt, secrets: secrets}
   ```
10. Build the official Alertmanager API client:
    ```go
    api := client.New(redacting, strfmt.Default)
    ```
11. Return `&alertmanagerClient{api: api, timeout: timeout, redactSecrets: secrets}, nil`.

`ctx` is accepted for API symmetry with the prometheus `NewClient` and for
future use; the official client does not need it at construction time.

#### `BuildClients`

Signature: `func BuildClients(ctx context.Context, configs Configs) (map[string]*alertmanagerClient, error)`.

- **Remove the `if config.Alertmanager == nil { continue }` skip** — every
  instance is now an Alertmanager instance. Build a client for every entry.
- Error wrap: `"failed to create Alertmanager client for instance %s"` (verbatim).

#### `redactingTransport` — secret redaction decorator

Implements `runtime.ClientTransport`. This is the security control that
replaces the old `doRequest` body redaction. The official client returns the
generated response structs (`*alert.GetAlertsBadRequest`,
`*alert.GetAlertsInternalServerError`, `*alert.PostAlertsBadRequest`,
`*alert.PostAlertsInternalServerError`) as the error for 400/500 (these types
implement `error` and carry `Payload string`), and
`*runtime.APIError` for unknown status codes (default case — its `Error()`
does not include the body because the body is not consumed on that path, so
no redaction is needed there).

```go
// redactingTransport wraps a runtime.ClientTransport and redacts configured
// secrets from the Payload of non-2xx error responses returned by the
// official Alertmanager client, then truncates the payload to
// amMaxErrorBodyLen. This preserves the hand-rolled client's defense against
// a server (or proxy) that echoes the Authorization header in error bodies
// (CWE-532 / CWE-200): credentials sent in the Authorization header cannot
// leak back through error messages surfaced to the LLM.
//
// Redaction is applied ONLY to error payloads (the generated 400/500
// response structs); success bodies are passed through untouched.
type redactingTransport struct {
    base    runtime.ClientTransport
    secrets []string
}

func (t *redactingTransport) Submit(op *runtime.ClientOperation) (any, error) {
    result, err := t.base.Submit(op)
    if err != nil {
        redactAPIErrorPayload(err, t.secrets)
    }
    return result, err
}
```

`redactAPIErrorPayload` type-switches over the four known alert response
structs and redacts+truncates `Payload` in place (preserving the typed error
so callers can still `errors.As`/type-assert):

```go
func redactAPIErrorPayload(err error, secrets []string) {
    switch e := err.(type) {
    case *alert.GetAlertsBadRequest:
        e.Payload = truncateRedacted(redactSecret(e.Payload, secrets))
    case *alert.GetAlertsInternalServerError:
        e.Payload = truncateRedacted(redactSecret(e.Payload, secrets))
    case *alert.PostAlertsBadRequest:
        e.Payload = truncateRedacted(redactSecret(e.Payload, secrets))
    case *alert.PostAlertsInternalServerError:
        e.Payload = truncateRedacted(redactSecret(e.Payload, secrets))
    }
}

func truncateRedacted(s string) string {
    if len(s) > amMaxErrorBodyLen {
        return s[:amMaxErrorBodyLen] + "...(truncated)"
    }
    return s
}
```

(Each generated response struct's `Error()` re-renders `Payload` via
`json.Marshal`, so mutating `Payload` makes the surfaced `err.Error()`
redacted. The typed error is preserved, so `errors.As(err,
&*alert.GetAlertsBadRequest)` still works for callers/tests that branch on
status. The `*runtime.APIError` default-case path does not carry the body in
its `Error()` string, so no redaction is needed there.)

#### `listAlertsParams` wrapper struct

Replaces the old `amListAlertsParams`. Same logical fields, same types as the
official `GetAlertsParams` subset we use:

```go
// listAlertsParams holds the optional query parameters for GET /api/v2/alerts
// that the alert tools use. It maps onto alert.GetAlertsParams.
type listAlertsParams struct {
    Active    *bool
    Silenced  *bool
    Inhibited *bool
    Filter    []string // matchers, e.g. `alertname="HighCPU"`; one matcher per element
    Receiver  string   // empty means unset
}
```

#### `boolPtr` — kept verbatim

```go
// boolPtr returns a pointer to the given bool. Used to distinguish "unset"
// (nil) from an explicit true/false in GetAlertsParams fields.
func boolPtr(b bool) *bool { return &b }
```

#### `ListAlerts` wrapper method

Signature: `func (c *alertmanagerClient) ListAlerts(ctx context.Context, p *listAlertsParams) ([]*models.GettableAlert, error)`.

```go
func (c *alertmanagerClient) ListAlerts(ctx context.Context, p *listAlertsParams) ([]*models.GettableAlert, error) {
    params := alert.NewGetAlertsParamsWithContext(ctx)
    if p != nil {
        if p.Active != nil {
            params.SetActive(p.Active)
        }
        if p.Silenced != nil {
            params.SetSilenced(p.Silenced)
        }
        if p.Inhibited != nil {
            params.SetInhibited(p.Inhibited)
        }
        if len(p.Filter) > 0 {
            params.SetFilter(p.Filter)
        }
        if p.Receiver != "" {
            r := p.Receiver
            params.SetReceiver(&r)
        }
    }
    resp, err := c.api.Alert.GetAlerts(params)
    if err != nil {
        return nil, errors.Wrap(err, "failed to list Alertmanager alerts")
    }
    return resp.GetPayload(), nil
}
```

Notes:
- `GetAlertsParams.SetActive/Silenced/Inhibited` take `*bool` (verified) —
  matches our `listAlertsParams` field types.
- `SetFilter([]string)` (verified) — repeated `filter=` query param.
- `SetReceiver(*string)` (verified) — our `Receiver` is `string`; we take
  its address only when non-empty.
- `resp.GetPayload()` returns `models.GettableAlerts` == `[]*models.GettableAlert`.
- Error wrap message preserved verbatim ("failed to list Alertmanager
  alerts"). The inner error is the redacted generated response struct.

#### `PostAlerts` wrapper method

Signature: `func (c *alertmanagerClient) PostAlerts(ctx context.Context, alerts models.PostableAlerts) error`.

```go
func (c *alertmanagerClient) PostAlerts(ctx context.Context, alerts models.PostableAlerts) error {
    params := alert.NewPostAlertsParamsWithContext(ctx).SetAlerts(alerts)
    if _, err := c.api.Alert.PostAlerts(params); err != nil {
        return errors.Wrap(err, "failed to post alerts to Alertmanager")
    }
    return nil
}
```

Notes:
- `models.PostableAlerts` == `[]*models.PostableAlert` (verified).
- `SetAlerts(models.PostableAlerts)` returns `*PostAlertsParams` (verified).
- Error wrap message preserved verbatim ("failed to post alerts to
  Alertmanager").

### `components/tool/alertmanager/base.go`
- `const` block:
  ```go
  const (
      alertToolName      = "alertmanager_alert"
      alertWriteToolName = "alertmanager_alert_write"
  )
  ```
- `type baseTool struct { clients map[string]*alertmanagerClient; knownInstances []string }`.
- `func (b *baseTool) client(instance string) (*alertmanagerClient, error)` —
  returns `instanceNotFoundError(instance, b.knownInstances)` on miss (note:
  `instanceNotFoundError` now says "Alertmanager instance").
- `func newBaseTool(ctx context.Context, configs Configs) (*baseTool, error)` —
  `clients, err := BuildClients(ctx, configs)`; on err return nil, err; return
  `&baseTool{clients: clients, knownInstances: toolutil.SortedKeys(clients)}`.
  Import `github.com/webcenter-fr/eino-ext/libs/toolkit/toolutil`.

### `components/tool/alertmanager/helper.go`
- `//go:embed prompts/list_output_guidance.md` var `listOutputGuidance string`
  (with the same doc comment as prometheus's).
- `marshalOutputs`, `marshalString`, `validateParams`, `parseRFC3339` —
  verbatim copies from prometheus `helper.go` (same imports: `marshal`,
  `validate`).
- `instanceNotFoundError` — copy but change the label string from
  `"Prometheus instance"` to `"Alertmanager instance"`:
  `return toolutil.NotFoundError("Alertmanager instance", instance, known)`.
- `AlertPaginate`, `alertPaginateToken`, `paginateWindow`, `nextPageToken` —
  verbatim copies from prometheus `helper.go` (these no longer reference
  `amReceiver`; see `receiverNames` below).
- `receiverNames` — adapted to the official model:
  ```go
  // receiverNames flattens the receiver list of an alert into a plain []string.
  func receiverNames(rs []*models.ReceiverReference) []string {
      names := make([]string, 0, len(rs))
      for _, r := range rs {
          if r != nil && r.Name != nil {
              names = append(names, *r.Name)
          }
      }
      return names
  }
  ```
  Import `github.com/prometheus/alertmanager/api/v2/models`.
- Do NOT copy `parsePromQLDuration` (Prometheus-only).

### `components/tool/alertmanager/alert.go`

Copy prometheus `alert.go` with the adaptations below (see "Tool-layer
adaptations" for the model-type changes forced by the official client):

- `package alertmanager`.
- Imports: replace `github.com/prometheus/common/model` with
  `github.com/prometheus/alertmanager/api/v2/models`; add `time` (already
  present). Keep `marshal`, `filter`, `eino` imports.
- `AlertTool` embeds `*baseTool` (not `*alertmanagerBaseTool`).
- `Invoke`:
  - `t.amClient(...)` → `t.client(...)` (two call sites).
  - Build params via `&listAlertsParams{...}` (renamed from
    `amListAlertsParams`); `boolPtr(true)`/`boolPtr(false)` unchanged.
  - `alerts, err := c.ListAlerts(ctx, p)` — `alerts` is now
    `[]*models.GettableAlert`.
  - Fingerprint filter loop: `for _, a := range alerts { if a.Fingerprint !=
    nil && *a.Fingerprint == params.Fingerprint { ... } }` (Fingerprint is
    `*string`).
  - State filter loop: `if a.Status != nil && a.Status.State != nil &&
    *a.Status.State == params.State { ... }`.
  - Output construction:
    ```go
    output := AlertOutput{
        Labels:      models.LabelSet(a.Labels),
        Annotations: models.LabelSet(a.Annotations),
        State:       ptrString(a.Status.State),
        StartsAt:    ptrDateTimeFormat(a.StartsAt),
        EndsAt:      ptrDateTimeFormat(a.EndsAt),
        Fingerprint: ptrString(a.Fingerprint),
        SilencedBy:  a.Status.SilencedBy,
        Receivers:   receiverNames(a.Receivers),
    }
    ```
    where `a.Labels` / `a.Annotations` are `models.LabelSet` (== `map[string]string`),
    `a.Status.SilencedBy` is `[]string` (unchanged), and the helpers are:
    ```go
    // ptrString safely dereferences a *string, returning "" for nil.
    func ptrString(s *string) string {
        if s == nil { return "" }
        return *s
    }
    // ptrDateTimeFormat formats a *strfmt.DateTime as RFC3339, returning ""
    // for nil. Uses time.Time(*dt).Format(time.RFC3339) to preserve the
    // previous second-precision output (a.StartsAt.Format(time.RFC3339)).
    func ptrDateTimeFormat(dt *strfmt.DateTime) string {
        if dt == nil { return "" }
        return time.Time(*dt).Format(time.RFC3339)
    }
    ```
    (Add these two helpers to `helper.go` or `alert.go`; keep them
    package-private. `strfmt` import added.)
  - `paginateWindow`, `nextPageToken`, `marshalOutputs` — unchanged.
- `AlertParams`/`AlertOutput` field tags unchanged EXCEPT:
  - `AlertOutput.Labels` type: `model.LabelSet` → `models.LabelSet`.
  - `AlertOutput.Annotations` type: `model.LabelSet` → `models.LabelSet`.
  - `AlertParams.Instance` jsonschema description: change `"(required) The
    Prometheus instance to query (must have Alertmanager configured)."` →
    `"(required) The Alertmanager instance to query."`. All other tags and
    `validate:"required"` stay.
- `NewAlertTool`: `base, err := newBaseTool(ctx, configs)`; tool name
  `alertToolName` (i.e. `"alertmanager_alert"`); description
  `fmt.Sprintf("%s\n%s", alertDescription, listOutputGuidance)`.
- `alertDescription` text: keep verbatim; the phrase "associated with a
  Prometheus instance" in the first line → "associated with an Alertmanager
  instance" for accuracy.

### `components/tool/alertmanager/alert_write.go`

Copy prometheus `alert_write.go` with the adaptations below:

- `package alertmanager`.
- Imports: replace `github.com/prometheus/common/model` with
  `github.com/prometheus/alertmanager/api/v2/models`; add
  `github.com/go-openapi/strfmt`. Keep `confirm`, `marshal`, `eino` imports.
- `AlertWriteTool` embeds `*baseTool`; `postAlert` uses `t.client(instance)`.
- `Invoke`:
  - `validateParams`, `confirm.RequireConfirmation(dryRun, confirmed)`,
    alertname check — unchanged.
  - `labelSet := toLabelSet(params.Labels)` → `labelSet :=
    models.LabelSet(params.Labels)` (LabelSet IS `map[string]string`).
  - `create` branch:
    - `validateGeneratorURL`, `coalesceTime` — unchanged.
    - Build alert:
      ```go
      alert := models.PostableAlert{
          Labels:       labelSet,
          Annotations:  models.LabelSet(params.Annotations),
          StartsAt:     strfmt.DateTime(startsAt),
          EndsAt:       strfmt.DateTime(endsAt),
          GeneratorURL:  strfmt.URI(params.GeneratorURL),
      }
      ```
      (PostableAlert.StartsAt/EndsAt are `strfmt.DateTime` values, not
      pointers; GeneratorURL is `strfmt.URI` from the embedded `Alert`.)
    - Dry-run: `marshalString(map[string]any{"dryRun": true, "operation":
      "create", "alert": alert})` — unchanged (alert now marshals via its
      generated `MarshalJSON`).
    - `t.postAlert(ctx, params.Instance, alert)` — `postAlert` signature
      changes to `func (t *AlertWriteTool) postAlert(ctx context.Context,
      instance string, alert *models.PostableAlert) error` and calls
      `c.PostAlerts(ctx, models.PostableAlerts{alert})`. Update the create
      call to pass `&alert`.
  - `update` branch:
    - `validateMatcherLabelKeys`, `buildMatcherFilter` — unchanged (operate
      on `map[string]string`).
    - `c, err := t.client(params.Instance)`.
    - `matches, err := c.ListAlerts(ctx, &listAlertsParams{Active: boolPtr(true),
      Silenced: boolPtr(true), Inhibited: boolPtr(true), Filter: filter})`
      (renamed from `amListAlertsParams`).
    - `existing := matches[0]` — `existing` is `*models.GettableAlert`.
    - `mergedAnnotations := existing.Annotations` (type `models.LabelSet`);
      if `params.Annotations != nil` → `mergedAnnotations =
      models.LabelSet(params.Annotations)`.
    - `mergedStartsAt, err := coalesceTime(time.Time(*existing.StartsAt),
      params.StartsAt)` (nil-guard: if `existing.StartsAt == nil` use
      `time.Time{}`).
    - `mergedEndsAt, err := coalesceTime(time.Time(*existing.EndsAt),
      params.EndsAt)` (same nil-guard).
    - `mergedGeneratorURL := existing.GeneratorURL` (type `strfmt.URI`); if
      `params.GeneratorURL != ""` → `mergedGeneratorURL =
      strfmt.URI(params.GeneratorURL)`.
    - `merged := models.PostableAlert{ Labels: existing.Labels, Annotations:
      mergedAnnotations, StartsAt: strfmt.DateTime(mergedStartsAt), EndsAt:
      strfmt.DateTime(mergedEndsAt), GeneratorURL: mergedGeneratorURL }`.
    - Dry-run: `marshalString(map[string]any{"dryRun": true, "operation":
      "update", "existing": existing, "merged": merged})`.
    - `c.PostAlerts(ctx, models.PostableAlerts{&merged})`.
    - Return `AlertWriteOutput{Status: "success", Action: "updated",
      Fingerprint: ptrString(existing.Fingerprint)}` (Fingerprint is
      `*string`).
  - `delete` branch:
    - `resolve := models.PostableAlert{ Labels: labelSet, StartsAt:
      strfmt.DateTime(startsAt), EndsAt: strfmt.DateTime(now) }`.
    - Dry-run: `marshalString(map[string]any{"dryRun": true, "operation":
      "delete", "resolve": resolve})`.
    - `t.postAlert(ctx, params.Instance, &resolve)`.
    - Return `AlertWriteOutput{Status: "success", Action: "deleted", EndsAt:
      now.Format(time.RFC3339)}`.
  - `default` branch — unchanged.
- `AlertWriteParams` `Instance` jsonschema description: change `"(required)
  The Prometheus instance (must have Alertmanager configured)."` → `"(required)
  The Alertmanager instance."`. All other tags verbatim.
- `NewAlertWriteTool`: `newBaseTool`; tool name `alertWriteToolName`.
- `buildMatcherFilter`, `validateMatcherLabelKeys`, `coalesceTime`,
  `validateGeneratorURL` — verbatim (they operate on `map[string]string` and
  `time.Time`, independent of the model package). `postAlert` signature
  changes as above.

### `components/tool/alertmanager/instance_list.go`
Mirror prometheus `instance_list.go` with:
- `package alertmanager`.
- `const instanceListDescription` — text adapted: "It lists all the
  Alertmanager instances where it can connect." and "name: the name of the
  Alertmanager instance.".
- `type InstanceListTool struct { knownInstances []string; tool.InvokableTool }`.
- `type InstanceListParams struct{}`.
- `Invoke` — `json.Marshal(t.knownInstances)`, wrap
  `"failed to marshal known instances"`.
- `NewInstanceListTool(ctx, configs)` — `utils.InferTool("alertmanager_instance_list",
  instanceListDescription, instanceListTool.Invoke,
  utils.WithUnmarshalArguments(toolutil.EmptyJSONUnmarshaler[*InstanceListParams]()))`.

### `components/tool/alertmanager/check.go`
Spec as in decision #7. Imports: `context`, `fmt`, `time`, `emperror.dev/errors`,
`github.com/webcenter-fr/eino-ext/libs/toolkit/checkup`. Const
`alertmanagerCheckTimeout = 10 * time.Second`.
- `Check` — empty/nil configs → single
  `{Component: "alertmanager", Status: StatusError, Error: "no Alertmanager
  instances configured"}`. Loop sorted instances; per instance build
  `baseCtx` with 10s timeout; `client, err := NewClient(baseCtx, cfg)`; on err
  `all = append(all, clientErrorResults(instance, err)...)` and `baseCancel();
  continue`; else `all = append(all, probeInstance(baseCtx, client, instance)...)`
  inside `defer baseCancel()`.
- `clientErrorResults(instance, err)` — 3 results, one per `allComponentNames()`,
  `StatusError`, `errStr`.
- `allComponentNames()` → `["alertmanager_instance_list", "alertmanager_alert",
  "alertmanager_alert_write"]`.
- `probeInstance(ctx, client, instance)` — returns 3 results:
  `{Component: "alertmanager_instance_list", Instance, StatusOK}`,
  `probeAlert(ctx, client, instance)`,
  `{Component: alertWriteToolName, Instance, StatusLimited, Message: "write
  tool, not probed to avoid side effects"}`.
- `probeAlert(ctx, client, instance)` — `alerts, err := client.ListAlerts(ctx,
  &listAlertsParams{Active: boolPtr(true)})`; on err return
  `{Component: alertToolName, Instance, StatusError, Error:
  errors.Wrap(err, "failed to list Alertmanager alerts").Error()}`; else
  `{Component: alertToolName, Instance, StatusOK, Message: fmt.Sprintf("%d
  alerts found, RBAC ok", len(alerts))}`.

### `components/tool/alertmanager/registry.go`
Spec as in decision #8. Mirror grafana `registry.go`:
- `type toolConstructor func(context.Context, Configs) (tool.InvokableTool, error)`.
- `readOnlyConstructors` = `NewInstanceListTool`, `NewAlertTool`.
- `writeConstructors` = `NewAlertWriteTool`.
- `buildTools` error wrap `"failed to create alertmanager tool %d: %w"`.
- `NewAllTools`, `NewReadOnlyTools`, `WriteToolNames` (returns
  `[]string{alertWriteToolName}`), `ExtractWriteToolNames`,
  `NewAllToolsWithSafety` (auto-populate `safetyCfg.WriteToolNames` when empty;
  import `github.com/webcenter-fr/eino-ext/components/middleware/safety`).
- Compile-time assertions:
  `var _ tool.InvokableTool = (*InstanceListTool)(nil)`,
  `(*AlertTool)(nil)`, `(*AlertWriteTool)(nil)`.

### `components/tool/alertmanager/prompts/list_output_guidance.md`
Verbatim copy of `components/tool/prometheus/prompts/list_output_guidance.md`.

### `components/tool/alertmanager/README.md`
New README modeled on grafana's structure:
- Title `# Alertmanager Tools`.
- Design bullets: HTTP API via the **official Alertmanager v2 client**
  (`github.com/prometheus/alertmanager/api/v2/client`, generated by go-swagger
  over the `/api/v2/` OpenAPI spec), multi-instance `Configs map[string]Config`,
  Basic+Bearer auth (Bearer priority) via go-openapi auth writers, TLS skip,
  output limiting (RE2 filter + pagination), read+write split, confirmation
  gate, secret redaction in errors (redacting transport over the official
  client).
- Configuration snippet using `alertmanager.Configs` / `alertmanager.Config`
  (Address, Username, Password, BearerToken, TLSSkipVerify, Timeout).
- Available Tools table:
  | `alertmanager_instance_list` | Read | List configured Alertmanager instances |
  | `alertmanager_alert` | Read | Read alerts (list or get-single via `fingerprint`) |
  | `alertmanager_alert_write` | Write | Create, update, or delete (resolve) an alert |
- Factory Functions: `NewAllTools`, `NewReadOnlyTools`,
  `NewAllToolsWithSafety`; `WriteToolNames()` returns
  `["alertmanager_alert_write"]`.
- Tool Details for `alertmanager_alert` and `alertmanager_alert_write`
  (parameter tables copied from the current prometheus README, with
  "Prometheus instance" → "Alertmanager instance" and the `Alertmanager`
  sub-config references removed). Include operation semantics (create/update/
  delete, idempotent resolve, update-no-match, first-match-on-multiple).
- Output Limiting section.
- Breaking changes section: note that these tools moved from
  `components/tool/prometheus` where they were named `prometheus_alert` and
  `prometheus_alert_write` with config nested under
  `prometheus.Config.Alertmanager`; the config is now top-level
  `alertmanager.Config` and tool names are re-prefixed `alertmanager_`. Note
  also that the underlying client changed from a hand-rolled `net/http`
  client to the official Alertmanager v2 client (no user-facing wire change).

---

## Tool-layer adaptations summary (forced by official model types)

Because the official `models.GettableAlert` / `models.PostableAlert` use
pointer fields and `strfmt.DateTime` / `strfmt.URI` instead of the old
value-type `time.Time` / `string`, the moved tool files (`alert.go`,
`alert_write.go`) need the following mechanical adaptations beyond a plain
copy. These are NOT behavioral changes — the wire format and output JSON
shape are identical.

| Old (hand-rolled) | New (official) |
|---|---|
| `amListAlertsParams` | `listAlertsParams` (same fields) |
| `gettableAlert` | `*models.GettableAlert` |
| `postableAlert` | `models.PostableAlert` (value type; pass `&alert` to PostAlerts) |
| `toLabelSet(map[string]string) model.LabelSet` | `models.LabelSet(map[string]string)` (direct cast; LabelSet IS `map[string]string`) |
| `amReceiver` / `receiverNames([]amReceiver)` | `*models.ReceiverReference` / `receiverNames([]*models.ReceiverReference)` (dereference `*r.Name`) |
| `amAlertStatus` / `a.Status.State` (string) | `*models.AlertStatus` / `*a.Status.State` (ptr; use `ptrString`) |
| `a.StartsAt.Format(time.RFC3339)` (`time.Time`) | `time.Time(*a.StartsAt).Format(time.RFC3339)` (`*strfmt.DateTime`; use `ptrDateTimeFormat` nil-guard) |
| `a.Fingerprint` (string) | `*a.Fingerprint` (ptr; use `ptrString`) |
| `a.Status.SilencedBy` (`[]string`) | `a.Status.SilencedBy` (`[]string`) — unchanged |
| `postableAlert{StartsAt: &t, EndsAt: &t, GeneratorURL: s}` | `models.PostableAlert{StartsAt: strfmt.DateTime(t), EndsAt: strfmt.DateTime(t), GeneratorURL: strfmt.URI(s)}` |
| `c.PostAlerts(ctx, []postableAlert{x})` | `c.PostAlerts(ctx, models.PostableAlerts{&x})` |
| `AlertOutput.Labels model.LabelSet` | `AlertOutput.Labels models.LabelSet` (JSON shape identical: `map[string]string`) |
| `AlertOutput.Annotations model.LabelSet` | `AlertOutput.Annotations models.LabelSet` |

Add two package-private helpers (`ptrString(*string) string`,
`ptrDateTimeFormat(*strfmt.DateTime) string`) to `helper.go`.

---

## Prometheus package changes (file-by-file)

### Delete files
- `components/tool/prometheus/alert.go`
- `components/tool/prometheus/alert_write.go`
- `components/tool/prometheus/alertmanager_client.go`
- `components/tool/prometheus/alertmanager_base.go`
- `components/tool/prometheus/alert_test.go`
- `components/tool/prometheus/alert_write_test.go`
- `components/tool/prometheus/alertmanager_client_test.go`

### `components/tool/prometheus/config.go`
- Remove the `Alertmanager *AlertmanagerConfig` field (and its doc comment)
  from `Config`.
- Remove the entire `AlertmanagerConfig` type.
- Keep `Configs`, `Config` (Address, Username, Password, BearerToken,
  TLSSkipVerify), `GetConfig`, `GetInstanceNames` unchanged.

### `components/tool/prometheus/helper.go`
- Remove `AlertPaginate`, `alertPaginateToken`, `paginateWindow`,
  `nextPageToken`, `receiverNames`.
- Keep `listOutputGuidance` (still used by `metric.go` and `target_list.go`),
  `marshalOutputs`, `marshalString`, `instanceNotFoundError`, `validateParams`,
  `parseRFC3339`, `parsePromQLDuration`.
- After removal, drop now-unused imports if any (the removed functions used
  `emperror.dev/errors`, `github.com/goccy/go-json`, `marshal` — but
  `marshalOutputs` still uses `marshal`; `json` was used by
  `paginateWindow`/`nextPageToken` — check if still needed: `marshalOutputs`
  uses `json.RawMessage` so `json` import stays; `errors` was used by
  `paginateWindow` and `parsePromQLDuration` — `parsePromQLDuration` still
  uses `errors`, so `errors` stays). Verify imports compile.

### `components/tool/prometheus/check.go`
- Delete `alertmanagerClientErrorResults` and `probeAlert`.
- In `Check`'s per-instance loop, delete the whole block:
  ```go
  if cfg.Alertmanager != nil { ... } else { ... }
  ```
  The loop body becomes:
  ```go
  client, err := NewClient(baseCtx, cfg)
  if err != nil {
      all = append(all, clientErrorResults(instance, err)...)
  } else {
      all = append(all, probeInstance(baseCtx, client, instance)...)
  }
  baseCancel()
  ```
- Keep `promCheckTimeout`, `clientErrorResults` (3 results), `probeInstance`,
  `probeInstanceList`, `probeMetric`, `probeTargetList` unchanged.
- Remove now-unused imports if any (`fmt` still used by probeMetric/
  probeTargetList; `errors` still used; `promapi` still used; `time` still
  used). Verify imports compile.

### `components/tool/prometheus/registry.go`
- `readOnlyConstructors`: remove the `NewAlertTool` line (keep
  `NewInstanceListTool`, `NewMetricTool`, `NewTargetListTool`).
- `writeConstructors`: becomes `var writeConstructors = []toolConstructor{}`
  (empty slice).
- `WriteToolNames()`: return `[]string{}`. Add comment: "Prometheus currently
  exposes no write tools; retained for API stability and safety-middleware
  wiring."
- Remove `(*AlertTool)(nil)` and `(*AlertWriteTool)(nil)` from the compile-time
  assertions block.
- `NewAllTools`, `NewReadOnlyTools`, `ExtractWriteToolNames`,
  `NewAllToolsWithSafety` unchanged (they iterate the constructor slices; empty
  `writeConstructors` is fine). `NewAllToolsWithSafety`: when
  `safetyCfg.WriteToolNames` is empty it stays empty — `safety.New` must handle
  an empty list (verify in checklist).

### `components/tool/prometheus/client.go`
- Unchanged. `authRoundTripper` stays here (still used by `NewClient`).

### `components/tool/prometheus/base.go`, `instance_list.go`, `metric.go`, `target_list.go`
- Unchanged.

### `components/tool/prometheus/prometheus_test.go`
- Remove `TestAlertmanagerConfigDecoupling` (references `AlertmanagerConfig` and
  `NewAlertTool`/`NewAlertWriteTool`).
- Rewrite `TestValidateParams` to use a prometheus params type instead of
  `AlertParams`/`AlertPaginate`. Use `TargetListParams` (has `validate:"required"`
  on Instance and `validate:"omitempty,oneof=up down unknown"` on Health):
  - "valid params" → `&TargetListParams{Instance: "prod"}`.
  - "valid params with state" → replace with "valid params with health":
    `&TargetListParams{Instance: "prod", Health: "up"}`.
  - "valid params with pagination" → remove (no pagination on TargetListParams);
    replace with "valid params with filter": `&TargetListParams{Instance:
    "prod", Filter: "node.*"}`.
  - "missing required instance" → `&TargetListParams{}` (expect error
    containing "invalid parameters").
  - "invalid state value" → "invalid health value":
    `&TargetListParams{Instance: "prod", Health: "broken"}` (expect error).
  - "page size below minimum" / "page size above maximum" → remove (no longer
    applicable); keep the test count reasonable.
- `TestInstanceNotFoundError` and `TestConfigs_*` unchanged.

### `components/tool/prometheus/check_test.go`
- `TestCheckInvalidInstance`: change expected result count from 5 to 3
  (`3 prometheus tools`); remove the `switch` case for `prometheus_alert`/
  `prometheus_alert_write` (all 3 results should be `StatusError`); update the
  comment.
- Remove `TestAlertmanagerClientErrorResults` entirely.
- `TestCheckEmptyConfigs`, `TestCheckNilConfigs`, `TestCheckResultStatuses`,
  `TestCheckClientErrorResults` unchanged (the latter already asserts 3).

### `components/tool/prometheus/README.md`
- Design bullets: remove the "Read + write split" mention of alert tools and
  the "Alertmanager" bullet. Replace with a note that Prometheus tools are
  read-only.
- Configuration: remove the `Alertmanager: &prometheus.AlertmanagerConfig{...}`
  block from the snippet; remove the paragraph about `Alertmanager` being
  optional and `AlertmanagerConfig` fields.
- Available Tools table: remove the `prometheus_alert` and
  `prometheus_alert_write` rows.
- Factory Functions: update `prometheus.WriteToolNames()` note to say it
  returns `[]string{}` (no write tools).
- Tool Details: remove the `prometheus_alert` and `prometheus_alert_write`
  subsections.
- Breaking changes: add a new entry at top:
  "**Moved**: `prometheus_alert` and `prometheus_alert_write` moved to the
  dedicated `components/tool/alertmanager` package and were renamed
  `alertmanager_alert` and `alertmanager_alert_write`. The
  `prometheus.Config.Alertmanager` field and `prometheus.AlertmanagerConfig`
  type were removed; configure Alertmanager via `alertmanager.Configs`
  instead. The underlying client changed from a hand-rolled `net/http`
  client to the official Alertmanager v2 client (no wire-format change)."
  Keep the existing historical breaking-change entries.

---

## Test changes (new package)

### `components/tool/alertmanager/alert_test.go`
Copy from `prometheus/alert_test.go` with:
- `package alertmanager`.
- `newAlertTool` helper: build via `NewAlertTool(context.Background(), Configs{
  "t": {Address: server.URL}})` (no nested `Alertmanager` field — `Address` is
  now top-level on `Config`).
- All test logic unchanged (fingerprint precedence, default query params,
  suppressed params, state filter, alertFilter passed to API, regex filter,
  pagination, paginate-window clamping, empty result, missing instance, invalid
  state, API error, constructor).
- Mock server assertions: the official client sends `GET /api/v2/alerts` with
  query params `active=true&silenced=false&inhibited=false&filter=...`. The
  mock handler must assert `r.URL.Path == "/api/v2/alerts"` and check
  `r.URL.Query()` (the official client URL-encodes the same way). Existing
  assertions on `gotQuery` adapt from `r.URL.RawQuery` to `r.URL.Query().Get(...)`.
- `TestAlertConstructor`: assert `info.Name == "alertmanager_alert"`.
- `TestAlertFingerprintPrecedence` / state-filter / suppressed: the mock now
  must return a 200 with body `models.GettableAlerts` JSON (same shape as
  before: `[{labels, annotations, startsAt, endsAt, fingerprint, receivers,
  status:{state, silencedBy, ...}}]`). The official consumer parses
  `*strfmt.DateTime` for startsAt/endsAt — the mock body must use valid
  RFC3339 strings (already does).
- API-error test: mock returns 500 with `{"error":"boom"}`; the wrapper
  returns an error wrapping `*alert.GetAlertsInternalServerError` whose
  `Payload` is the redacted body. Assert `err.Error()` contains `"failed to
  list Alertmanager alerts"` and contains `"internal server error"`-ish text
  from the generated `Error()` (the exact rendered string is
  `[GET /alerts][500] getAlertsInternalServerError "..."` — assert it contains
  `"getAlertsInternalServerError"` and the redacted body, NOT the raw
  `"boom"` if redaction applies; for a non-secret body, `"boom"` survives
  redaction and appears in the JSON-marshaled payload).

### `components/tool/alertmanager/alert_write_test.go`
Copy from `prometheus/alert_write_test.go` with:
- `package alertmanager`.
- `newWriteToolConfig` helper: `NewAlertWriteTool(context.Background(),
  Configs{"t": cfg})` where `cfg` is a `Config` (top-level) with `Address` set
  from `server.URL`. `newWriteTool` calls `newWriteToolConfig(t, Config{}, handler)`.
- All test logic unchanged (create, update, update filter params, keep
  annotations, delete, dry-run, requires confirmation, requires alertname,
  invalid operation, invalid times, update no match, multiple matches uses
  first, generator URL scheme, delete ignores generator URL, missing
  instance, API errors, auth headers, secret redaction, buildMatcherFilter,
  validateMatcherLabelKeys, update rejects invalid label keys, input size
  limits).
- Mock server: POST handler reads body and unmarshals into
  `models.PostableAlerts` (== `[]*models.PostableAlert`). Assert
  `posted[0].Labels["alertname"] == "HighCPU"` etc. (Labels is
  `map[string]string`, so direct key access; no `model.LabelValue` cast).
- Auth-header tests: the official client sets `Authorization: Bearer
  <token>` via `httpclient.BearerToken` and `Authorization: Basic <base64>` via
  `httpclient.BasicAuth`. The mock assertions (`r.Header.Get("Authorization")`
  == `"Bearer token-123"`, `r.BasicAuth()` == user/pass) are unchanged.
  "Bearer wins over basic": when both are configured, only Bearer is set
  (we do not `Compose`), so `r.Header.Get("Authorization")` ==
  `"Bearer token-123"` — unchanged assertion.

### `components/tool/alertmanager/client_test.go` — REWRITTEN for the official client

This file is rewritten (not copied) because the hand-rolled `doRequest`/
`amHTTPError`/`isAMStatus` internals are gone. It tests the official-client
wrapper using the same `httptest` mock approach, adapted to the go-swagger
transport.

- `package alertmanager`.
- `TestNewClient`:
  - "missing address fails validation": `NewClient(ctx, Config{})` → error
    contains `"invalid Alertmanager config"`.
  - "invalid scheme rejected": `NewClient(ctx, Config{Address:
    "ftp://am:9093"})` → error contains `"must include scheme"`.
  - "valid config applies default timeout": `NewClient(ctx,
    Config{Address: "http://localhost:9093"})` → `c.timeout == 30*time.Second`.
  - "valid config with custom timeout": `NewClient(ctx, Config{Address:
    "https://am.example.com", Timeout: "5s"})` → `c.timeout == 5*time.Second`.
  - "invalid timeout rejected": `NewClient(ctx, Config{Address:
    "http://am.example.com", Timeout: "bogus"})` → error.
  - "trailing slash trimmed / path prefix mapped": `NewClient(ctx,
    Config{Address: "http://localhost:9093/"})` succeeds; (path-prefix mapping
    is exercised via a request-shape test below).
- `TestBuildClients`:
  - `Configs{"with-am": {Address: "http://am:9093"}, "no-am": {Address:
    "http://am2:9093"}}` → `BuildClients` returns a map of size 2 with both
    keys (no nil-skip).
  - "empty configs" → empty map, no error.
  - "invalid config wraps instance name": `Configs{"bad": {Address: ""}}` →
    error contains `"instance bad"`.
- `TestListAlerts` (replaces `TestAlertmanagerListAlerts`):
  - Mock `GET /api/v2/alerts` returns 200 with a `models.GettableAlerts` JSON
    body (one alert). Assert `r.URL.Path == "/api/v2/alerts"` and
    `r.URL.Query().Get("active") == "true"` and a `filter` query param equals
    `alertname="HighCPU"`. Assert the returned `[]*models.GettableAlert` has
    len 1 and `*alerts[0].Fingerprint == "abc123"`.
- `TestListAlertsError` (replaces `TestAlertmanagerListAlertsError`):
  - Mock returns 500 with `{"error":"boom"}`. `ListAlerts` returns an error
    whose `Error()` contains `"failed to list Alertmanager alerts"` AND
    contains `"getAlertsInternalServerError"` (the generated response type
    name). Assert `errors.As(err, &*alert.GetAlertsInternalServerError)` is
    true (import the `alert` package in the test).
- `TestPostAlerts` (replaces `TestAlertmanagerPostAlerts`):
  - Mock `POST /api/v2/alerts` returns 200 `{"status":"success"}`. Post a
    `models.PostableAlerts{&models.PostableAlert{Labels:
    models.LabelSet{"alertname":"HighCPU"}, Annotations:
    models.LabelSet{"summary":"high cpu"}}}`. Assert `r.Method == "POST"`;
    unmarshal body into `models.PostableAlerts` and assert
    `posted[0].Labels["alertname"] == "HighCPU"`.
- `TestPostAlertsError` (replaces `TestAlertmanagerPostAlertsError`):
  - Mock returns 400 `{"error":"bad"}`. `PostAlerts` returns an error whose
    `Error()` contains `"failed to post alerts to Alertmanager"` AND
    `"postAlertsBadRequest"`. Assert `errors.As(err,
    &*alert.PostAlertsBadRequest)` is true.
- `TestAuthHeaders` (replaces `TestAlertmanagerAuthHeaders`):
  - "bearer token": `NewClient` with `BearerToken: "token-123"`; mock
    records `r.Header.Get("Authorization")`; `ListAlerts` succeeds; assert
    header == `"Bearer token-123"`.
  - "basic auth": `NewClient` with `Username: "admin", Password: "pw-123"`;
    mock records `r.BasicAuth()`; assert user/pass.
  - "bearer wins over basic": both configured; assert
    `r.Header.Get("Authorization") == "Bearer token-123"` (no Basic header).
- `TestSecretRedactionInErrors` (replaces
  `TestAlertmanagerSecretRedactionInErrors`):
  - Mock returns 500 with `{"error":"internal"}`. `NewClient` with
    `Username/Password/BearerToken` set to `LEAK-*` constants. `ListAlerts`
    and `PostAlerts` errors must NOT contain the bearer, password, or
    username. (The redacting transport mutates `Payload` on the generated
    response struct before the wrapper wraps it; the surfaced `err.Error()`
    re-renders the redacted `Payload`.)
- `TestNewClientRejectsNonHTTPSchemes` (replaces
  `TestNewAlertmanagerClientRejectsNonHTTPSchemes`):
  - Same address list (`ftp://`, `gopher://`, `file:///`, `alertmanager:9093`);
    all rejected with `"must include scheme"`.
- `TestSecretRedactionFromEchoedHeader` (PRESERVED — the key security test):
  - "bearer token echoed in error body is redacted": mock returns 500 with
    body `{"error":"auth header was: <Authorization>"}` (echoes the header).
    `NewClient` with `BearerToken: bearer`. `ListAlerts` error must NOT
    contain `bearer` or `"Bearer "+bearer`, and MUST contain `"[REDACTED]"`.
  - "basic auth echoed in error body is redacted": mock returns 400 with
    body `{"error":"got header: <Authorization>"}`. `NewClient` with
    `Username/Password`. Error must NOT contain password, username, the
    base64-encoded `user:pass`, or `"Basic "+encoded`; MUST contain
    `"[REDACTED]"`.
  - "redaction applies to POST errors too": mock returns 500 echoing the
    Authorization header; `PostAlerts` error must NOT contain the bearer.
  - These tests now exercise the `redactingTransport` over the official
    client. The mock handler is the same `httptest.Server`; the official
    client sends the request through the go-openapi runtime, which hits the
    mock. The redacting transport intercepts the resulting
    `*alert.GetAlertsInternalServerError` / `*alert.PostAlertsBadRequest` and
    redacts `Payload`.
- `TestRedactionTruncation` (NEW — preserves the old "large error body
  truncated" test):
  - Mock returns 500 with a 2048-byte body of repeated `x` (no secrets).
    `ListAlerts` error's `Payload` (extracted via `errors.As` into
    `*alert.GetAlertsInternalServerError`) must be `<= amMaxErrorBodyLen +
    len("...(truncated)")` and end with `"...(truncated)"`.
- `TestBuildRedactSecrets` (PRESERVED verbatim): same as the old test —
  bearer only, basic auth, empty config.

### `components/tool/alertmanager/instance_list_test.go`
Mirror `prometheus/instance_list_test.go` with:
- `package alertmanager`.
- `Configs{"prod": {Address: "http://am:9093"}, "staging": {Address:
  "http://am2:9093"}}`.
- Assert `info.Name == "alertmanager_instance_list"`.
- Same test cases: happy path, empty configs, nil configs, sorted.

### `components/tool/alertmanager/check_test.go`
Mirror `grafana/check_test.go` structure with prometheus-style assertions:
- `TestCheckEmptyConfigs`: 1 result, component `"alertmanager"`, StatusError.
- `TestCheckNilConfigs`: 1 result, StatusError.
- `TestCheckInvalidInstance`: `Configs{"bad": Config{Address: ""}}`; expect 3
  results, all StatusError, all instance `"bad"`, components
  `alertmanager_instance_list`/`alertmanager_alert`/`alertmanager_alert_write`.
- `TestCheckResultStatuses`: statuses in {OK, Error, Limited}.
- `TestCheckClientErrorResults`: `clientErrorResults("test-instance",
  context.DeadlineExceeded)` returns 3 results, all StatusError, instance
  `"test-instance"`.
- `TestAllComponentNames`: `allComponentNames()` returns 3 unique names.

---

## Edge cases / security controls to preserve (verify in tests)

- **Fingerprint precedence**: when `Fingerprint` set, `alertFilter` and `state`
  ignored; all states fetched (`active=true&silenced=true&inhibited=true`); no
  `filter=` query param. (TestAlertFingerprintPrecedence)
- **Suppressed-state fetch**: `state=suppressed` requests both silenced and
  inhibited. (TestAlertSuppressedQueryParams)
- **Default listing**: `active=true&silenced=false&inhibited=false` (do not
  rely on server defaults). (TestAlertDefaultQueryParams)
- **Matcher escaping**: `buildMatcherFilter` escapes `\` and `"` in values;
  one matcher per label (comma-joined rejected by API). Keys validated by
  `validateMatcherLabelKeys` against `model.LabelNameRE` before building.
  (TestBuildMatcherFilter*, TestValidateMatcherLabelKeys,
  TestAlertWriteUpdateRejectsInvalidLabelKeys)
- **`endsAt` future/after-startsAt** for create; **endsAt after startsAt** for
  update. (TestAlertWriteInvalidTimes)
- **Update no-match** errors with `"no existing alert matches labels %v"`.
  (TestAlertWriteUpdateNoMatch)
- **Delete idempotent resolve**: single POST, `endsAt <= now`, `startsAt =
  now-1m`, no pre-existence GET. (TestAlertWriteDelete)
- **Pagination token clamping**: stale token beyond total clamped to total;
  negative token clamped to 0. (TestPaginateWindowClampsStaleToken)
- **Secret redaction**: bearer, password, username, and base64 basic-auth
  pair redacted from non-2xx error bodies (including echoed Authorization
  header) via the `redactingTransport` over the official client.
  (TestSecretRedactionInErrors, TestSecretRedactionFromEchoedHeader,
  TestBuildRedactSecrets)
- **Error body truncation**: non-2xx error payloads truncated to
  `amMaxErrorBodyLen = 512`. (TestRedactionTruncation)
- **SSRF address-scheme enforcement**: `NewClient` rejects non-http/https
  schemes with `"Alertmanager address must include scheme (http:// or
  https://): %s"`. (TestNewClientRejectsNonHTTPSchemes)
- **Timeout**: default `30s`, custom via `Timeout`, invalid rejected; applied
  to the underlying `*http.Client.Timeout`. (TestNewClient)
- **Confirmation gate**: `confirm.RequireConfirmation(dryRun, confirmed)` —
  error `"confirmed must be true to execute"` when neither dryRun nor
  confirmed. (TestAlertWriteRequiresConfirmation)
- **Input size limits**: labels/annotations max 64 entries via
  `validate:"max=64,dive,..."`. (TestAlertWriteInputSizeLimits)
- **`generatorURL` scheme**: http/https enforced by `validateGeneratorURL`.
  (TestAlertWriteGeneratorURLScheme, TestAlertWriteDeleteIgnoresGeneratorURL)
- **Empty configs in Check**: single error result, component `"alertmanager"`.
- **Auth priority**: Bearer token, when set, is used exclusively (Basic never
  configured) — mirrors the old `authRoundTripper` Bearer-over-Basic
  semantics. (TestAuthHeaders "bearer wins over basic")

---

## Verification checklist

Run from repo root:
```bash
go get github.com/prometheus/alertmanager@v0.34.0
go mod tidy
go build ./...
go vet ./...
go test ./components/tool/alertmanager/... ./components/tool/prometheus/...
go test ./...
```

CONTRIBUTING.md PR checklist (per package):
- [ ] `go get github.com/prometheus/alertmanager@v0.34.0` + `go mod tidy`
      run; `go.mod` diff is limited to the go-openapi stack
      (`runtime`, `strfmt`, `errors`, `validate`, `loads`, `spec`,
      `analysis`) + `oklog/ulid/v2` + `go-viper/mapstructure/v2` +
      `asaskevich/govalidator` (and the already-present `swag`/`jsonpointer`/
      `jsonreference` indirects). Note in PR description that
      `go-openapi/swag`, `jsonpointer`, `jsonreference` were ALREADY indirect
      in the repo.
- [ ] `go build ./...`, `go vet ./...`, `go test ./...` pass.
- [ ] `alertmanager.Config` has `validate`+`jsonschema` tags; `NewClient`
      calls `validate.Struct(&cfg)` AFTER applying the `Timeout="30s"` default.
- [ ] `alertmanager` package has: `alert_test.go`, `alert_write_test.go`,
      `client_test.go`, `instance_list_test.go`, `check_test.go`, `README.md`,
      package comment, and compile-time `var _ tool.InvokableTool =
      (*InstanceListTool)(nil)`, `(*AlertTool)(nil)`, `(*AlertWriteTool)(nil)`.
- [ ] `alertmanager` package has `check.go` + `check_test.go` with `Check()`
      returning `checkup.Results`.
- [ ] Every `New...` constructor (`NewClient`, `NewInstanceListTool`,
      `NewAlertTool`, `NewAlertWriteTool`, `newBaseTool`, `BuildClients`)
      accepts `ctx context.Context` as first param and threads it through.
- [ ] Naming: `Alertmanager` (not `Alertmanager` lowercase), `URL`, `HTTP`,
      `API` follow Go casing.
- [ ] Errors wrapped with `emperror.dev/errors` (operation context
      preserved verbatim from the existing code: `"invalid Alertmanager
      config"`, `"Alertmanager address must include scheme (http:// or
      https://): %s"`, `"invalid Alertmanager timeout value: %s"`,
      `"failed to create Alertmanager client for instance %s"`,
      `"failed to list Alertmanager alerts"`, `"failed to post alerts to
      Alertmanager"`).
- [ ] No license banner added.
- [ ] No duplication of `libs/toolkit/` helpers (the duplicated
      `marshalOutputs`/`validateParams`/etc. follow the existing per-component
      precedent). `authRoundTripper` is NOT duplicated — the alertmanager
      package uses the official client's auth writers.
- [ ] **Official-client wrapper redaction tested**: `TestSecretRedactionFromEchoedHeader`
      passes (echoed `Authorization` header in a 500/400 body is redacted by
      `redactingTransport` before the error is surfaced).
- [ ] **Official-client error types asserted**: `TestListAlertsError` and
      `TestPostAlertsError` assert `errors.As(err,
      &*alert.GetAlertsInternalServerError)` / `&*alert.PostAlertsBadRequest`.
- [ ] Prometheus package still compiles after removing `AlertmanagerConfig`:
      no remaining references to `Alertmanager`, `AlertmanagerConfig`,
      `AlertTool`, `AlertWriteTool`, `alertmanagerClient`, `amReceiver`,
      `AlertPaginate`, `paginateWindow`, `nextPageToken`, `receiverNames`,
      `alertmanagerBaseTool`, `alertWriteToolName`. (Grep to confirm zero
      matches in `components/tool/prometheus/`.)
- [ ] `safety.New` accepts an empty `WriteToolNames` (prometheus
      `NewAllToolsWithSafety` now passes `[]string{}`); verify no panic and
      that the middleware still wraps tools (read tools pass through).
- [ ] `prometheus.WriteToolNames()` returns `[]string{}` (empty, non-nil).
- [ ] `prometheus/check_test.go` `TestCheckInvalidInstance` expects 3
      results; `TestAlertmanagerClientErrorResults` removed.
- [ ] `prometheus/prometheus_test.go` `TestAlertmanagerConfigDecoupling`
      removed; `TestValidateParams` rewritten to use `TargetListParams`.
- [ ] `components/tool/alertmanager/prompts/list_output_guidance.md` is a
      verbatim copy of the prometheus prompt file.
- [ ] `components/tool/prometheus/prompts/list_output_guidance.md` still
      present (still embedded by prometheus `helper.go`).
- [ ] `components/tool/prometheus/client.go` `authRoundTripper` unchanged
      (still used by prometheus `NewClient`).

## Open questions / out of scope

- Whether to provide a migration helper that maps old
  `prometheus.Config.Alertmanager` to the new `alertmanager.Configs`: **out of
  scope**; users update their config wiring (documented as a breaking change).
- Whether to keep the historical "renamed from alertmanagerAlertListTool"
  comments on the moved types: **drop** those stale comments in the new
  package (they refer to a pre-prometheus naming that no longer applies).
- Whether to also surface the other Alertmanager v2 sub-services
  (`Alertgroup`, `General`, `Receiver`, `Silence`) as tools: **out of scope**;
  the wrapper exposes only `Alert.GetAlerts`/`PostAlerts` (the two operations
  the existing tools use). The `client.AlertmanagerAPI` struct still
  constructs all sub-services; unused ones are simply not called.
