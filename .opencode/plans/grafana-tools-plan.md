# Grafana Tools — Implementation Plan

## Overview

Add a `grafana` component under `components/tool/grafana/` providing eino tools
to search, describe, and build Grafana dashboards across multiple named
Grafana instances. Follows the established multi-instance HTTP-API pattern
pioneered by `components/tool/argocd` (HTTP REST API, `Configs map[string]Config`,
Bearer token auth, `net/http` client).

### Tools

| Tool Name | Category | Purpose |
|---|---|---|
| `grafana_instance_list` | Read | List configured Grafana instances |
| `grafana_dashboard_search` | Read | Search/find dashboards, returns URLs |
| `grafana_dashboard_describe` | Read | Get a single dashboard by UID |
| `grafana_dashboard_build` | Write | Create/update a dashboard, returns final URL |

### Security Model

1. **Confirmation gate** — `grafana_dashboard_build` requires
   `confirm.RequireConfirmation(dryRun, confirmed)` before any write.
2. **Dashboard protection blocklist** — per-instance configurable set of
   protected dashboards (by UID, title prefix, folder, tag) that cannot be
   modified by the build tool. Enforced before the API call by fetching the
   existing dashboard and matching against compiled criteria.
3. **Token redaction** — `Token` field uses `json:"-"` so it never appears in
   JSON schema output or logs.
4. **Timeouts** — all HTTP calls use a per-request context timeout derived from
   `Config.DefaultTimeout` (default 30s).

---

## Contradiction Resolved: `osclient.New()` Cannot Be Used

**The requirement states**: "The grafana client should use `osclient.New()`
from `libs/toolkit/osclient/`."

**The code contradicts this**: `osclient.New()` (in
`libs/toolkit/osclient/osclient.go`) returns an `opensearchv4.Client` — the
OpenSearch v4 client from `github.com/disaster37/opensearch/v4`. It is
hardcoded to build an OpenSearch client with OpenSearch-specific config
(URLs, Username, Password, TLSSkipVerify) and an OpenSearch transport. It
cannot make Grafana HTTP API calls (`GET /api/search`,
`POST /api/dashboards/db`, etc.).

**Resolution**: The Grafana client is a custom `*grafanaClient` struct wrapping
`net/http`, following the raw-HTTP pattern already used in
`components/tool/argocd/check.go` (`newArgoCDHTTPClient`, `doArgoCDListGET`).
This is the correct pattern for a REST API with no dedicated Go client library
in `go.mod`. The `osclient` package remains OpenSearch-only per
`CONTRIBUTING.md` ("Do not unify them").

---

## File Layout

```
components/tool/grafana/
  config.go              # Config, Configs, ProtectedDashboardsConfig
  client.go              # grafanaClient, NewClient, BuildClients, HTTP methods
  base.go                # baseTool, newBaseTool, protection helpers
  helper.go              # embedded prompts, instanceNotFoundError, filterMapMarshal
  instance_list.go       # grafana_instance_list tool
  dashboard_search.go    # grafana_dashboard_search tool
  dashboard_describe.go  # grafana_dashboard_describe tool
  dashboard_build.go     # grafana_dashboard_build tool (write, blocklist-enforced)
  registry.go            # constructor lists, NewAllTools, WriteToolNames, compile-time checks
  check.go               # Check() probing each instance
  check_test.go          # Check() unit tests
  suite_test.go          # httptest.Server mock Grafana API, ToolTestSuite
  grafana_test.go        # tool invocation tests
  README.md
  prompts/
    dashboard_search_output_guidance.md
    dashboard_describe_output_guidance.md
```

---

## 1. config.go

Package comment: `// Package grafana provides eino tools for Grafana dashboard management.`

### Struct Definitions

```go
package grafana

import (
    "github.com/webcenter-fr/eino-ext/libs/toolkit/toolutil"
)

// Config represents the configuration for a single Grafana instance.
type Config struct {
    // URL is the Grafana server URL with scheme, e.g. "https://grafana.example.com".
    URL string `validate:"required" jsonschema:"description=Grafana server URL with scheme (https:// or http://)"`

    // Token is the service-account or API token used for Bearer auth.
    // Hidden from JSON schema output via json:"-".
    Token string `json:"-"`

    // TLSSkipVerify skips TLS certificate verification. Hidden from schema.
    TLSSkipVerify bool `json:"-"`

    // DefaultTimeout is a Go duration string for per-request timeouts.
    // Defaults to "30s" when empty.
    DefaultTimeout string `validate:"omitempty" jsonschema:"description=Default timeout for HTTP calls (Go duration string, e.g. '30s')"`

    // ProtectedDashboards defines the blocklist of dashboards that cannot be
    // modified by the build tool. This is a security control to protect
    // base/infrastructure dashboards (e.g. Kubernetes monitoring).
    ProtectedDashboards ProtectedDashboardsConfig `validate:"omitempty"`
}

// ProtectedDashboardsConfig defines criteria for dashboards that are protected
// from modification. A dashboard is protected if it matches ANY one criterion
// (logical OR across categories; within a category, it's a match if the
// dashboard's value matches ANY entry in the list).
type ProtectedDashboardsConfig struct {
    // UIDs lists exact dashboard UIDs that are protected from editing.
    UIDs []string `json:"uids,omitempty" validate:"omitempty"`

    // TitlePrefixes lists title prefixes; a dashboard whose title starts with
    // any of these strings is protected. Example: ["Kubernetes ", "Infra: "].
    TitlePrefixes []string `json:"titlePrefixes,omitempty" validate:"omitempty"`

    // Folders lists folder UIDs; any dashboard residing in one of these folders
    // is protected.
    Folders []string `json:"folders,omitempty" validate:"omitempty"`

    // Tags lists dashboard tags; any dashboard carrying one of these tags is
    // protected. Example: ["protected", "infrastructure"].
    Tags []string `json:"tags,omitempty" validate:"omitempty"`
}

// Configs is a map of Grafana instance configurations keyed by instance name.
type Configs map[string]Config

// GetConfig returns the configuration for the given instance name.
// Returns the zero value if the instance is not present.
func (c Configs) GetConfig(instanceName string) Config {
    return c[instanceName]
}

// GetInstanceNames returns all instance names in sorted order.
func (c Configs) GetInstanceNames() []string {
    return toolutil.SortedKeys(c)
}
```

### Default Application Logic

Defaults are applied in `NewClient` (not in Config itself, following the
argocd pattern where `NewClient` validates and the `baseTool` constructor
applies timeouts):

- `DefaultTimeout`: if empty, set to `"30s"` before `validate.Struct`.

---

## 2. client.go

### grafanaClient Struct

```go
type grafanaClient struct {
    baseURL    string        // scheme + host, no trailing slash
    token       string
    httpClient  *http.Client
    timeout     time.Duration
}
```

### NewClient Signature

```go
// NewClient creates a Grafana HTTP client from the provided configuration.
// It validates the config, applies defaults, and builds an *http.Client with
// TLS settings and timeout.
func NewClient(ctx context.Context, config Config) (*grafanaClient, error)
```

**Logic**:
1. Apply default: if `config.DefaultTimeout == ""`, set `config.DefaultTimeout = "30s"`.
2. Call `validate.Struct(&config)` — returns wrapped error on failure.
3. Validate URL scheme: must start with `https://` or `http://`; else return
   `errors.Errorf("Grafana URL must include scheme (https:// or http://): %s", config.URL)`.
4. Parse timeout: `parseTimeoutOrDefault(config.DefaultTimeout, 30*time.Second)`.
5. Build `*http.Client`:
   - `Transport`: if `config.TLSSkipVerify`, set
     `&http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}` (nolint:gosec);
     else default `http.DefaultTransport`.
   - `Timeout`: the parsed timeout.
6. Return `&grafanaClient{baseURL: strings.TrimRight(config.URL, "/"), token: config.Token, httpClient: hc, timeout: parsedTimeout}`.

### BuildClients Signature

```go
// BuildClients creates grafanaClients for all configurations in the Configs map.
func BuildClients(ctx context.Context, configs Configs) (map[string]*grafanaClient, error)
```

Iterates `configs`, calls `NewClient` per entry, wraps errors with
`errors.Wrapf(err, "failed to create client for instance %s", name)`.

### HTTP Helper Methods

All methods accept `ctx context.Context`, wrap it with `context.WithTimeout(ctx, c.timeout)`,
set `Authorization: Bearer <token>` header (if token non-empty), `Content-Type: application/json`,
and `Accept: application/json`. Errors wrapped with `emperror.dev/errors`.

```go
// doRequest executes an HTTP request and returns the raw body.
// Returns an error for status >= 400, including the response body in the message.
func (c *grafanaClient) doRequest(ctx context.Context, method, path string, body io.Reader) ([]byte, int, error)
```

### API Methods

```go
// SearchDashboards calls GET /api/search with the given query parameters.
// Returns the raw JSON array body (caller unmarshals into []searchHit).
func (c *grafanaClient) SearchDashboards(ctx context.Context, params *searchParams) ([]byte, error)

// GetDashboard calls GET /api/dashboards/uid/:uid and returns the raw body.
func (c *grafanaClient) GetDashboard(ctx context.Context, uid string) ([]byte, error)

// SaveDashboard calls POST /api/dashboards/db with the given payload.
// Returns the raw response body (contains id, uid, url, version, status).
func (c *grafanaClient) SaveDashboard(ctx context.Context, payload []byte) ([]byte, error)
```

### Wire Types (unexported, in client.go)

```go
// searchParams maps to GET /api/search query parameters.
type searchParams struct {
    Query       string   // ?query=
    Type        string   // ?type=  (dash-db, dash-folder)
    Tags        []string // ?tag= (repeatable)
    FolderUIDs  []string // ?folderUIDs=
    Sort        string   // ?sort= (e.g. "alpha_asc", "created_desc")
    Limit       int      // ?limit= (max 5000, default 100)
    Page        int      // ?page= (1-based)
}

// searchHit is a single element of the GET /api/search response array.
type searchHit struct {
    ID          int64    `json:"id"`
    UID         string   `json:"uid"`
    Title       string   `json:"title"`
    URL         string   `json:"url"`          // relative: /d/<uid>/<slug>
    Type        string   `json:"type"`         // "dash-db" or "dash-folder"
    Tags        []string `json:"tags"`
    FolderTitle  string   `json:"folderTitle"`
    FolderUID   string   `json:"folderUid"`
    Starred     bool     `json:"starred"`
}

// dashboardResponse is the GET /api/dashboards/uid/:uid response.
type dashboardResponse struct {
    Dashboard map[string]any `json:"dashboard"`
    Meta      dashboardMeta  `json:"meta"`
}

type dashboardMeta struct {
    FolderTitle string `json:"folderTitle"`
    FolderUID   string `json:"folderUid"`
    FolderID    int64  `json:"folderId"`
    Version     int    `json:"version"`
    CreatedBy   string `json:"createdBy"`
    UpdatedBy   string `json:"updatedBy"`
}

// saveDashboardRequest is the POST /api/dashboards/db request body.
type saveDashboardRequest struct {
    Dashboard map[string]any `json:"dashboard"`
    FolderUID string         `json:"folderUid,omitempty"`
    FolderID  int64          `json:"folderId,omitempty"`
    Message   string         `json:"message,omitempty"`
    Overwrite bool           `json:"overwrite"`
}

// saveDashboardResponse is the POST /api/dashboards/db response.
type saveDashboardResponse struct {
    ID      int64  `json:"id"`
    UID     string `json:"uid"`
    URL     string `json:"url"`     // relative: /d/<uid>/<slug>
    Status  string `json:"status"`  // "success" or "version-mismatch"
    Version int    `json:"version"`
    Slug    string `json:"slug"`
}
```

### URL Construction

Full dashboard URL = `c.baseURL + hit.URL` (or `c.baseURL + saveResp.URL`).
The `URL` field from the API is relative (e.g. `/d/abc123/my-dashboard`).

---

## 3. base.go

### baseTool Struct

```go
const (
    defaultGrafanaTimeout = 30 * time.Second
)

type baseTool struct {
    clients        map[string]*grafanaClient
    configs        Configs
    knownInstances []string
    // protected holds compiled per-instance protection matchers.
    protected      map[string]*dashboardProtection
}
```

### dashboardProtection (compiled blocklist)

```go
// dashboardProtection is the compiled, lookup-optimized form of
// ProtectedDashboardsConfig for a single instance.
type dashboardProtection struct {
    uidSet        map[string]bool   // exact UID matches
    titlePrefixes []string           // title starts-with
    folderSet     map[string]bool   // folder UID matches
    tagSet        map[string]bool   // tag matches
}

// isProtected reports whether the given dashboard attributes match any
// protection criterion. Returns true if ANY criterion matches.
func (p *dashboardProtection) isProtected(uid, title, folderUID string, tags []string) bool

// buildProtection compiles a ProtectedDashboardsConfig into a dashboardProtection.
func buildProtection(cfg ProtectedDashboardsConfig) *dashboardProtection
```

`isProtected` logic:
1. If `uid != ""` and `p.uidSet[uid]` → true.
2. For each prefix in `p.titlePrefixes`: if `strings.HasPrefix(title, prefix)` → true.
3. If `folderUID != ""` and `p.folderSet[folderUID]` → true.
4. For each tag in `tags`: if `p.tagSet[tag]` → true.
5. Otherwise false.

If all four lists are empty, `buildProtection` returns `nil` (no protection).
`isProtected` on a nil receiver returns false.

### newBaseTool

```go
func newBaseTool(ctx context.Context, configs Configs) (*baseTool, error)
```

**Logic**:
1. If `len(configs) == 0`, return
   `errors.Errorf("at least one Grafana instance configuration is required")`.
2. `clients, err := BuildClients(ctx, configs)`.
3. Build `protected` map: for each instance, `buildProtection(cfg.ProtectedDashboards)`.
4. Return `&baseTool{clients, configs, configs.GetInstanceNames(), protected}`.

### Utility Methods

```go
// client returns the grafanaClient for the given instance, or a NotFoundError.
func (b *baseTool) client(instance string) (*grafanaClient, error)

// protection returns the compiled dashboardProtection for the instance (may be nil).
func (b *baseTool) protection(instance string) *dashboardProtection

// checkProtected fetches the existing dashboard by UID (if non-empty) and
// returns an error if it is protected. Used by the build tool before saving.
// If uid is empty or the dashboard does not exist (404), returns nil (no protection).
func (b *baseTool) checkProtected(ctx context.Context, instance, uid string) error
```

`checkProtected` logic:
1. If `uid == ""` → return nil (new dashboard, no protection).
2. Fetch existing dashboard via `client.GetDashboard(ctx, uid)`.
3. If HTTP 404 → return nil (dashboard doesn't exist, it's a create).
4. On other errors → return wrapped error.
5. Unmarshal into `dashboardResponse`.
6. Extract `title` from `dashboard.title`, `folderUID` from `meta.folderUid`,
   `tags` from `dashboard.tags`.
7. If `protection.isProtected(uid, title, folderUID, tags)` → return
   `errors.Errorf("dashboard %q (UID %s) is protected and cannot be modified", title, uid)`.
8. Else return nil.

### instanceNotFoundError

```go
func instanceNotFoundError(instance string, known []string) error {
    return toolutil.NotFoundError("Grafana instance", instance, known)
}
```

(Placed in `helper.go` following the argocd pattern.)

---

## 4. helper.go

```go
package grafana

import (
    _ "embed"
    "regexp"

    "emperror.dev/errors"
    "github.com/goccy/go-json"
    "github.com/webcenter-fr/eino-ext/libs/toolkit/filter"
    "github.com/webcenter-fr/eino-ext/libs/toolkit/marshal"
    "github.com/webcenter-fr/eino-ext/libs/toolkit/toolutil"
)

//go:embed prompts/dashboard_search_output_guidance.md
var dashboardSearchOutputGuidance string

//go:embed prompts/dashboard_describe_output_guidance.md
var dashboardDescribeOutputGuidance string

func instanceNotFoundError(instance string, known []string) error {
    return toolutil.NotFoundError("Grafana instance", instance, known)
}

// filterMapMarshal maps each source item to an output value, marshals it,
// keeps only items whose JSON matches re, and returns the JSON array.
// (Same generic helper as argocd's filterMapMarshal.)
func filterMapMarshal[T, O any](items []T, re *regexp.Regexp, toOutput func(T) O) (string, error) {
    outputs := make([]json.RawMessage, 0, len(items))
    for _, item := range items {
        outputJSON := json.RawMessage(marshal.MustMarshal(toOutput(item)))
        if !filter.Match(outputJSON, re) {
            continue
        }
        outputs = append(outputs, outputJSON)
    }
    return marshal.Outputs(outputs)
}

// validateParams validates a struct using the shared validator.
func validateParams(v any) error {
    return validate.Struct(v)
}
```

---

## 5. instance_list.go

Follows the exact `argocd/instance_list.go` pattern.

```go
const instanceListDescription = `
** General Purpose **
It lists all the Grafana instances where it can connect.

** Output **
It returns a JSON array of objects, where each object represents an instance with the following fields:
- name: the name of the Grafana instance.
`

type InstanceListTool struct {
    knownInstances []string
    tool.InvokableTool
}

type InstanceListParams struct{}

func (t *InstanceListTool) Invoke(ctx context.Context, params *InstanceListParams) (string, error)

func NewInstanceListTool(ctx context.Context, configs Configs) (*InstanceListTool, error)
```

`NewInstanceListTool` uses `toolutil.EmptyJSONUnmarshaler[*InstanceListParams]()`
and `utils.InferTool("grafana_instance_list", instanceListDescription, ...)`.

---

## 6. dashboard_search.go (Read Tool)

### Description

```go
const dashboardSearchDescription = `
** General Purpose **
It searches for Grafana dashboards by title query, tags, folder, and type.
Returns matching dashboards with their URLs for direct access.

** Output **
It returns a JSON array of objects, where each object represents a dashboard with the following fields:
- uid: the dashboard UID.
- title: the dashboard title.
- url: the full URL to access the dashboard in Grafana.
- type: the resource type ("dash-db" for dashboards, "dash-folder" for folders).
- tags: the dashboard tags.
- folderTitle: the title of the folder containing the dashboard.
- folderUid: the UID of the folder containing the dashboard.
`
```

### Params Struct

```go
type DashboardSearchParams struct {
    Instance   string   `json:"instance" validate:"required" jsonschema:"(required) The Grafana instance to connect to."`
    Query      string   `json:"query,omitempty" jsonschema:"(optional) Title search query. Matches dashboard titles containing this string."`
    Type       string   `json:"type,omitempty" validate:"omitempty,oneof=dash-db dash-folder" jsonschema:"(optional) Filter by type: 'dash-db' for dashboards or 'dash-folder' for folders."`
    Tags       []string `json:"tags,omitempty" validate:"omitempty" jsonschema:"(optional) Filter by tags. A dashboard must have ALL specified tags to match."`
    FolderUIDs []string `json:"folderUIDs,omitempty" validate:"omitempty" jsonschema:"(optional) Filter by folder UIDs. Only dashboards in the specified folders are returned."`
    Sort       string   `json:"sort,omitempty" validate:"omitempty,oneof=alpha_asc alpha_desc created_asc created_desc updated_asc updated_desc" jsonschema:"(optional) Sort order for results."`
    Filter     string   `json:"filter,omitempty" jsonschema:"(optional) A Go RE2 regex applied on each dashboard JSON output. Keep only dashboards that match the pattern. RE2 does NOT support lookahead (?=...)/(?!...), lookbehind (?<=...)/(?<!...), or backreferences — such patterns return an error. Example: 'prod|staging'. Invalid regex returns an error."`
    Paginate   *DashboardSearchPaginate `json:"paginate,omitempty" jsonschema:"(optional) Pagination parameters."`
}

type DashboardSearchPaginate struct {
    PageSize int `json:"pageSize,omitempty" validate:"omitempty,min=1,max=5000" jsonschema:"(optional) Number of results per page. Default is 100, max 5000."`
    Page     int `json:"page,omitempty" validate:"omitempty,min=1" jsonschema:"(optional) Page number (1-based). Default is 1."`
}
```

### Output Struct

```go
type DashboardSearchOutput struct {
    UID         string   `json:"uid"`
    Title       string   `json:"title"`
    URL         string   `json:"url"`         // full URL: baseURL + relative URL
    Type        string   `json:"type"`
    Tags        []string `json:"tags"`
    FolderTitle string   `json:"folderTitle"`
    FolderUID   string   `json:"folderUid"`
}
```

### Tool Struct & Invoke

```go
type DashboardSearchTool struct {
    *baseTool
    tool.InvokableTool
}

func (t *DashboardSearchTool) Invoke(ctx context.Context, params *DashboardSearchParams) (string, error)
```

### Invoke Pseudocode

```
1. Apply defaults: if params.Paginate != nil && params.Paginate.PageSize == 0 → set PageSize = 100;
   if params.Paginate != nil && params.Paginate.Page == 0 → set Page = 1.
2. validateParams(params) → return error on failure.
3. re, err := filter.Compile(params.Filter) → wrap error.
4. client, err := t.client(params.Instance) → return error (NotFoundError) if unknown.
5. Build searchParams from DashboardSearchParams fields.
6. body, err := client.SearchDashboards(ctx, &searchParams) → wrap "failed to search dashboards".
7. Unmarshal body into []searchHit.
8. For each hit: build DashboardSearchOutput{URL: client.baseURL + hit.URL, ...}.
   Use filterMapMarshal with re to filter and marshal.
9. Return JSON array string.
```

### Error Handling & Edge Cases

- Unknown instance → `toolutil.NotFoundError("Grafana instance", instance, known)`.
- Invalid regex → wrapped `filter.Compile` error.
- HTTP 401/403 → wrapped error from `doRequest` (includes status + body).
- Empty result set → return `[]` (valid JSON empty array).
- `Paginate` nil → no limit/page params sent; Grafana defaults apply (100 results).

### Constructor

```go
func NewDashboardSearchTool(ctx context.Context, configs Configs) (*DashboardSearchTool, error)
```

Uses `utils.InferTool("grafana_dashboard_search", fmt.Sprintf("%s\n%s", dashboardSearchDescription, dashboardSearchOutputGuidance), ...)`.

---

## 7. dashboard_describe.go (Read Tool)

### Description

```go
const dashboardDescribeDescription = `
** General Purpose **
It gets the full details of a specific Grafana dashboard by its UID.

** Output **
It returns a JSON object with the dashboard model and metadata.
`
```

### Params Struct

```go
type DashboardDescribeParams struct {
    Instance            string   `json:"instance" validate:"required" jsonschema:"(required) The Grafana instance to connect to."`
    UID                 string   `json:"uid" validate:"required" jsonschema:"(required) The dashboard UID."`
    ExcludeFieldsOutput []string `json:"excludeFieldsOutput,omitempty" validate:"omitempty,dive,oneof=meta panels templating time annotations schemaVersion version" jsonschema:"(optional) Fields to exclude from the dashboard output: 'meta', 'panels', 'templating', 'time', 'annotations', 'schemaVersion', 'version'."`
}
```

### Output Struct

```go
type DashboardDescribeOutput struct {
    Dashboard map[string]any `json:"dashboard,omitempty"`
    Meta      *dashboardMeta `json:"meta,omitempty"`
}
```

### Tool Struct & Invoke

```go
type DashboardDescribeTool struct {
    *baseTool
    tool.InvokableTool
}

func (t *DashboardDescribeTool) Invoke(ctx context.Context, params *DashboardDescribeParams) (string, error)
```

### Invoke Pseudocode

```
1. validateParams(params) → return error.
2. client, err := t.client(params.Instance) → return error if unknown.
3. body, err := client.GetDashboard(ctx, params.UID) → wrap "failed to get dashboard".
4. Unmarshal into DashboardDescribeOutput.
5. Apply excludes via applyExcludes helper (same pattern as argocd):
   - "meta" → output.Meta = nil
   - "panels" → delete output.Dashboard["panels"]
   - "templating" → delete output.Dashboard["templating"]
   - "time" → delete output.Dashboard["time"]
   - "annotations" → delete output.Dashboard["annotations"]
   - "schemaVersion" → delete output.Dashboard["schemaVersion"]
   - "version" → delete output.Dashboard["version"]
   Invalid field → error "invalid exclude field: %s".
6. Marshal output → return string.
```

### Error Handling & Edge Cases

- Unknown instance → NotFoundError.
- Dashboard not found (HTTP 404) → wrapped error "failed to get dashboard".
- `ExcludeFieldsOutput` with invalid field → error.

### Constructor

```go
func NewDashboardDescribeTool(ctx context.Context, configs Configs) (*DashboardDescribeTool, error)
```

Uses `utils.InferTool("grafana_dashboard_describe", fmt.Sprintf("%s\n%s", dashboardDescribeDescription, dashboardDescribeOutputGuidance), ...)`.

---

## 8. dashboard_build.go (Write Tool — Blocklist-Enforced)

### Description

```go
const dashboardBuildDescription = `
** General Purpose **
It creates or updates a Grafana dashboard from a JSON dashboard model.
Returns the final URL of the dashboard.

** Safety **
Always use dryRun=true first to validate the dashboard model before saving.
After reviewing the dry-run result, set confirmed=true to actually save.

** Dashboard Protection **
Dashboards matching the instance's protected blocklist (by UID, title prefix,
folder, or tag) cannot be modified. If you attempt to update a protected
dashboard, the tool returns an error.

** Output **
It returns a JSON object with the saved dashboard's UID, URL, version, and status.
`
```

### Params Struct

```go
type DashboardBuildParams struct {
    Instance   string         `json:"instance" validate:"required" jsonschema:"(required) The Grafana instance to connect to."`
    Dashboard  string         `json:"dashboard" validate:"required" jsonschema:"(required) The full Grafana dashboard model as a JSON string. Must include 'title'. Include 'uid' to update an existing dashboard; omit 'uid' to create a new one."`
    FolderUID  string         `json:"folderUID,omitempty" jsonschema:"(optional) The UID of the folder to place the dashboard in. Omit for the root folder."`
    Message    string         `json:"message,omitempty" jsonschema:"(optional) Commit message for the dashboard version."`
    Overwrite  bool           `json:"overwrite,omitempty" jsonschema:"(optional) If true, overwrite an existing dashboard with the same UID without version checking."`
    DryRun     bool           `json:"dryRun,omitempty" jsonschema:"(optional) If true, validate the dashboard model without saving. Show the result to the user and ask for confirmation."`
    Confirmed  bool           `json:"confirmed,omitempty" jsonschema:"(optional) Must be true to actually save. Set this after the user has approved the dry-run result."`
}
```

### Output Struct

```go
type DashboardBuildOutput struct {
    UID     string `json:"uid"`
    URL     string `json:"url"`      // full URL: baseURL + relative URL
    Status  string `json:"status"`
    Version int    `json:"version"`
    Slug    string `json:"slug"`
}
```

### Tool Struct & Invoke

```go
type DashboardBuildTool struct {
    *baseTool
    tool.InvokableTool
}

func (t *DashboardBuildTool) Invoke(ctx context.Context, params *DashboardBuildParams) (string, error)
```

### Invoke Pseudocode

```
1. validateParams(params) → return error.
2. Enforce confirmation gate:
   confirm.RequireConfirmation(params.DryRun, params.Confirmed) → return error.
3. client, err := t.client(params.Instance) → return error if unknown.
4. Parse params.Dashboard JSON into map[string]any (dashboardModel).
   On parse error → wrap "invalid dashboard JSON".
5. Extract uid from dashboardModel["uid"] (string; may be "").
6. Extract title from dashboardModel["title"] (string; required).
   If title == "" → error "dashboard model must include a title".
7. PROTECTION CHECK (only for updates, i.e. uid != ""):
   err := t.checkProtected(ctx, params.Instance, uid)
   If err → return err (dashboard is protected).
   (checkProtected fetches the existing dashboard by UID; if 404, it's a
   create and no protection applies. If the existing dashboard matches any
   blocklist criterion, returns an error.)
8. If params.DryRun:
   a. Build a dry-run preview: marshal {dryRun: true, dashboard: dashboardModel,
      folderUID: params.FolderUID, overwrite: params.Overwrite}.
   b. Return the JSON string (no API call).
9. Build saveDashboardRequest{Dashboard: dashboardModel, FolderUID: params.FolderUID,
   Message: params.Message, Overwrite: params.Overwrite}.
10. Marshal request, call client.SaveDashboard(ctx, payload).
11. Unmarshal response into saveDashboardResponse.
12. Build DashboardBuildOutput{UID: resp.UID, URL: client.baseURL + resp.URL,
    Status: resp.Status, Version: resp.Version, Slug: resp.Slug}.
13. Marshal output → return string.
```

### Error Handling & Edge Cases

- Unknown instance → NotFoundError.
- Invalid dashboard JSON → wrapped parse error.
- Dashboard missing title → error.
- Protected dashboard → error from `checkProtected` (includes title + UID).
- HTTP 412 (version mismatch) when `overwrite=false` → wrapped error from
  `SaveDashboard` (includes status + body). The LLM should retry with
  `overwrite=true` if the user approves.
- HTTP 400 (validation error from Grafana) → wrapped error with body.
- DryRun path never makes a POST; it only fetches the existing dashboard (for
  protection check) and returns the preview.

### Constructor

```go
func NewDashboardBuildTool(ctx context.Context, configs Configs) (*DashboardBuildTool, error)
```

Uses `utils.InferTool("grafana_dashboard_build", dashboardBuildDescription, ...)`.

---

## 9. registry.go

```go
package grafana

import (
    "context"
    "fmt"

    "github.com/cloudwego/eino/components/tool"
    "github.com/webcenter-fr/eino-ext/components/middleware/safety"
)

type toolConstructor func(context.Context, Configs) (tool.InvokableTool, error)

var readOnlyConstructors = []toolConstructor{
    func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewInstanceListTool(ctx, c) },
    func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewDashboardSearchTool(ctx, c) },
    func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewDashboardDescribeTool(ctx, c) },
}

var writeConstructors = []toolConstructor{
    func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewDashboardBuildTool(ctx, c) },
}

func buildTools(ctx context.Context, configs Configs, constructors []toolConstructor) ([]tool.InvokableTool, error)

func NewAllTools(ctx context.Context, configs Configs) ([]tool.InvokableTool, error)
func NewReadOnlyTools(ctx context.Context, configs Configs) ([]tool.InvokableTool, error)

func WriteToolNames() []string {
    return []string{"grafana_dashboard_build"}
}

func ExtractWriteToolNames(ctx context.Context, configs Configs) ([]string, error)

func NewAllToolsWithSafety(ctx context.Context, configs Configs, safetyCfg *safety.Config) ([]tool.InvokableTool, *safety.Middleware, error)

// Compile-time interface checks
var (
    _ tool.InvokableTool = (*InstanceListTool)(nil)
    _ tool.InvokableTool = (*DashboardSearchTool)(nil)
    _ tool.InvokableTool = (*DashboardDescribeTool)(nil)
    _ tool.InvokableTool = (*DashboardBuildTool)(nil)
)
```

`NewAllToolsWithSafety` auto-populates `safetyCfg.WriteToolNames` from
`WriteToolNames()` if empty, then creates the safety middleware. Identical
structure to `argocd/registry.go`.

---

## 10. check.go

### Check Function

```go
const grafanaCheckTimeout = 10 * time.Second

func Check(ctx context.Context, configs Configs) checkup.Results
```

### Logic

1. If `len(configs) == 0` → return single error result:
   `{Component: "grafana", Status: "error", Error: "no Grafana instances configured"}`.
2. For each instance (sorted):
   a. `client, err := NewClient(ctx, cfg)` — on error, append
      `clientErrorResults(instance, err)` (one error result per tool component)
      and continue.
   b. `probeInstance(ctx, client, instance)`:
      - `grafana_instance_list`: always `ok` (no API call needed).
      - `grafana_dashboard_search`: call `GET /api/search?limit=1`. On success
        → `ok` with message "N dashboards found, RBAC ok". On error → `error`.
      - `grafana_dashboard_describe`: if search returned ≥1 hit, extract the
        first hit's UID and call `GET /api/dashboards/uid/:uid`. On success →
        `ok` with "described dashboard %q, RBAC ok". If search returned 0 hits
        → `limited` with "no dashboards to test describe". If search failed →
        `error` with "dependency failed".
      - `grafana_dashboard_build`: `limited` with "write tool, not probed to
        avoid side effects".

### clientErrorResults

```go
func clientErrorResults(instance string, err error) checkup.Results
```

Returns 4 error results (one per tool component name), all with the given
instance and error string.

### allComponentNames

```go
func allComponentNames() []string {
    return []string{
        "grafana_instance_list",
        "grafana_dashboard_search",
        "grafana_dashboard_describe",
        "grafana_dashboard_build",
    }
}
```

---

## 11. prompts/

### prompts/dashboard_search_output_guidance.md

```markdown
** How to limit output (IMPORTANT) **
Always narrow the query to avoid large responses:
- Use `query` to search by title (e.g. 'production', 'kubernetes').
- Use `tags` to filter by dashboard tags (e.g. ['prod', 'infra']).
- Use `type` to restrict to 'dash-db' (dashboards only) or 'dash-folder'.
- Use `folderUIDs` to search within specific folders.
- Use `filter` (Go RE2 regex, applied on each dashboard JSON output) to keep
  only matches. RE2 does NOT support lookahead (?=...)/(?!...), lookbehind
  (?<=...)/(?<!...), or backreferences — such patterns return an error. Prefer
  simple alternations (e.g. 'prod|staging').
- Use `paginate.pageSize` (default 100, max 5000) and `paginate.page` to page
  through large result sets.
```

### prompts/dashboard_describe_output_guidance.md

```markdown
** How to limit output (IMPORTANT) **
Use `excludeFieldsOutput` to drop large sections you do not need. Available
fields: 'meta', 'panels', 'templating', 'time', 'annotations',
'schemaVersion', 'version'. Excluding 'panels' dramatically reduces output
size for dashboards with many panels.
```

---

## 12. Testing Plan

### suite_test.go — Mock Grafana API

`ToolTestSuite` with `httptest.Server` and `Configs`. Mock endpoints:

| Endpoint | Method | Response |
|---|---|---|
| `/api/search` | GET | Array of 2 dashboards: `{uid: "abc123", title: "Production Overview", url: "/d/abc123/production-overview", type: "dash-db", tags: ["prod"], folderTitle: "Infrastructure", folderUid: "folder-1"}` and `{uid: "def456", title: "Staging Dashboard", url: "/d/def456/staging-dashboard", type: "dash-db", tags: ["staging"], folderUid: "folder-2"}` |
| `/api/search?query=Production` | GET | Array with 1 dashboard (Production Overview) |
| `/api/dashboards/uid/abc123` | GET | `{dashboard: {uid: "abc123", title: "Production Overview", tags: ["prod"], panels: [...]}, meta: {folderTitle: "Infrastructure", folderUid: "folder-1", version: 3}}` |
| `/api/dashboards/uid/nonexistent` | GET | 404 |
| `/api/dashboards/uid/protected-uid` | GET | `{dashboard: {uid: "protected-uid", title: "Kubernetes Monitoring", tags: ["infrastructure"]}, meta: {folderUid: "infra-folder"}}` |
| `/api/dashboards/db` | POST | `{id: 10, uid: "new-uid", url: "/d/new-uid/my-new-dashboard", status: "success", version: 1, slug: "my-new-dashboard"}` |

Configs:
```go
t.configs = Configs{
    "test": {
        URL: t.server.URL,
        ProtectedDashboards: ProtectedDashboardsConfig{
            UIDs:          []string{"protected-uid"},
            TitlePrefixes: []string{"Kubernetes "},
            Tags:          []string{"infrastructure"},
        },
    },
}
```

### grafana_test.go — Tool Tests

Table-driven tests using `t.configs` and the mock server.

**TestInstanceList**:
- Invoke with `""` and `"{}"` → returns `["test"]`.
- `Info(ctx)` succeeds.

**TestDashboardSearch** (table-driven):

| Case | Params JSON | Expected |
|---|---|---|
| Search all | `{"instance":"test"}` | 2 results, URLs contain server URL |
| Search by query | `{"instance":"test","query":"Production"}` | 1 result, title "Production Overview" |
| Search with filter | `{"instance":"test","filter":"prod"}` | 1 result (Production Overview has "prod" in JSON) |
| Search with type filter | `{"instance":"test","type":"dash-db"}` | 2 results |
| Search with pagination | `{"instance":"test","paginate":{"pageSize":1,"page":1}}` | 1 result |
| Unknown instance | `{"instance":"invalid"}` | Error |
| Invalid filter regex | `{"instance":"test","filter":"(?=...)"}` | Error |

**TestDashboardDescribe** (table-driven):

| Case | Params JSON | Expected |
|---|---|---|
| Describe existing | `{"instance":"test","uid":"abc123"}` | Contains title, panels, meta |
| Describe with excludes | `{"instance":"test","uid":"abc123","excludeFieldsOutput":["panels","meta"]}` | No "panels", no "meta" |
| Nonexistent UID | `{"instance":"test","uid":"nonexistent"}` | Error |
| Unknown instance | `{"instance":"invalid","uid":"abc123"}` | Error |
| Invalid exclude field | `{"instance":"test","uid":"abc123","excludeFieldsOutput":["foo"]}` | Error |

**TestDashboardBuild** (table-driven):

| Case | Params JSON | Expected |
|---|---|---|
| DryRun | `{"instance":"test","dashboard":"{\"title\":\"My New Dashboard\"}","dryRun":true}` | Returns `{"dryRun":true,...}`, no POST made |
| Create confirmed | `{"instance":"test","dashboard":"{\"title\":\"My New Dashboard\"}","confirmed":true}` | Returns uid, url, status "success" |
| Update existing | `{"instance":"test","dashboard":"{\"uid\":\"abc123\",\"title\":\"Production Overview\"}","overwrite":true,"confirmed":true}` | Returns uid "abc123", url |
| Protected by UID | `{"instance":"test","dashboard":"{\"uid\":\"protected-uid\",\"title\":\"Kubernetes Monitoring\"}","confirmed":true}` | Error "is protected" |
| Protected by title prefix | `{"instance":"test","dashboard":"{\"uid\":\"protected-uid\",\"title\":\"Kubernetes Monitoring\"}","confirmed":true}` | Error (title starts with "Kubernetes ") |
| Protected by tag | `{"instance":"test","dashboard":"{\"uid\":\"protected-uid\",\"title\":\"Kubernetes Monitoring\"}","confirmed":true}` | Error (tag "infrastructure") |
| No confirmation | `{"instance":"test","dashboard":"{\"title\":\"X\"}"}` | Error "confirmed must be true" |
| Missing title | `{"instance":"test","dashboard":"{}","confirmed":true}` | Error "must include a title" |
| Invalid JSON | `{"instance":"test","dashboard":"not json","confirmed":true}` | Error "invalid dashboard JSON" |
| Unknown instance | `{"instance":"invalid","dashboard":"{\"title\":\"X\"}","confirmed":true}` | Error |

**TestDashboardProtection** (unit tests for `dashboardProtection`):

| Case | Inputs | Expected |
|---|---|---|
| Nil protection | uid="abc", title="X" | false (not protected) |
| UID match | uid="protected-uid" | true |
| Title prefix match | title="Kubernetes Monitoring" | true |
| No title prefix match | title="My App" | false |
| Folder match | folderUID="infra-folder" | true |
| Tag match | tags=["infrastructure"] | true |
| No tag match | tags=["app"] | false |
| Empty config | all empty | false |

### check_test.go — Check Function Tests

| Test | Configs | Expected |
|---|---|---|
| TestCheckEmptyConfigs | `Configs{}` | 1 result, error, component "grafana" |
| TestCheckNilConfigs | `nil` | 1 result, error |
| TestCheckInvalidInstance | `{"bad": Config{URL: ""}}` | 4 results, all error, instance "bad" |
| TestCheckResultStatuses | `{"bad": Config{URL: ""}}` | All statuses are ok/error/limited |
| TestCheckClientErrorResults | call `clientErrorResults("test", err)` | 4 results, all instance "test", all error |
| TestAllComponentNames | call `allComponentNames()` | 4 names, no duplicates |

---

## 13. README.md Outline

```markdown
# Grafana Tools

eino tools for interacting with Grafana instances via the HTTP API (v9+).

## Design
- HTTP API — uses net/http with Bearer token auth (no dedicated Go client library).
- Multi-instance — Configs map[string]Config, matching the argocd/kubernetes pattern.
- Dashboard protection — per-instance blocklist (UID, title prefix, folder, tag).
- Safety — build tool enforces dry-run/confirmed gate.

## Configuration
<code snippet showing Configs with 2 instances, token, protected dashboards>

## Available Tools
<table: instance_list, dashboard_search, dashboard_describe, dashboard_build>

## Factory Functions
<NewAllTools, NewReadOnlyTools, NewAllToolsWithSafety, WriteToolNames snippets>

## Dashboard Protection
<explanation of blocklist criteria, example config>

## Usage Example
<dashboard_search and dashboard_build examples>
```

---

## 14. File-by-File Summary

| File | Contents |
|---|---|
| `config.go` | `Config` struct (URL, Token, TLSSkipVerify, DefaultTimeout, ProtectedDashboards), `ProtectedDashboardsConfig` (UIDs, TitlePrefixes, Folders, Tags), `Configs` map, `GetConfig`, `GetInstanceNames`. |
| `client.go` | `grafanaClient` struct, `NewClient`, `BuildClients`, `doRequest` helper, `SearchDashboards`, `GetDashboard`, `SaveDashboard` methods. Wire types: `searchParams`, `searchHit`, `dashboardResponse`, `dashboardMeta`, `saveDashboardRequest`, `saveDashboardResponse`. |
| `base.go` | `baseTool` struct, `dashboardProtection` struct + `isProtected` + `buildProtection`, `newBaseTool`, `client`, `protection`, `checkProtected` methods. `defaultGrafanaTimeout` const. |
| `helper.go` | Embedded prompt vars (`dashboardSearchOutputGuidance`, `dashboardDescribeOutputGuidance`), `instanceNotFoundError`, `filterMapMarshal` generic, `validateParams`. |
| `instance_list.go` | `InstanceListTool`, `InstanceListParams`, `Invoke`, `NewInstanceListTool`. |
| `dashboard_search.go` | `DashboardSearchParams`, `DashboardSearchPaginate`, `DashboardSearchOutput`, `DashboardSearchTool`, `Invoke`, `NewDashboardSearchTool`. |
| `dashboard_describe.go` | `DashboardDescribeParams`, `DashboardDescribeOutput`, `DashboardDescribeTool`, `Invoke`, `NewDashboardDescribeTool`. |
| `dashboard_build.go` | `DashboardBuildParams`, `DashboardBuildOutput`, `DashboardBuildTool`, `Invoke` (with protection check + confirmation gate), `NewDashboardBuildTool`. |
| `registry.go` | `readOnlyConstructors`, `writeConstructors`, `buildTools`, `NewAllTools`, `NewReadOnlyTools`, `WriteToolNames`, `ExtractWriteToolNames`, `NewAllToolsWithSafety`, compile-time `var _` checks. |
| `check.go` | `Check`, `clientErrorResults`, `allComponentNames`, `probeInstance`, probe helpers for search/describe. |
| `check_test.go` | `TestCheckEmptyConfigs`, `TestCheckNilConfigs`, `TestCheckInvalidInstance`, `TestCheckResultStatuses`, `TestCheckClientErrorResults`, `TestAllComponentNames`. |
| `suite_test.go` | `ToolTestSuite` with `httptest.Server` mock Grafana API, `SetupSuite`, `TearDownSuite`, `configs`. |
| `grafana_test.go` | `TestInstanceList`, `TestDashboardSearch`, `TestDashboardDescribe`, `TestDashboardBuild`, `TestDashboardProtection`. |
| `README.md` | Component overview, config, tools table, factory functions, protection, usage. |
| `prompts/dashboard_search_output_guidance.md` | LLM guidance for narrowing search output. |
| `prompts/dashboard_describe_output_guidance.md` | LLM guidance for excluding large fields. |

---

## 15. Implementation Order

1. `config.go` — no dependencies.
2. `client.go` — depends on config.go.
3. `base.go` — depends on client.go, config.go.
4. `helper.go` — depends on filter, marshal, toolutil, validate.
5. `prompts/*.md` — embedded by helper.go.
6. `instance_list.go` — depends on base.go, helper.go.
7. `dashboard_search.go` — depends on base.go, helper.go, client.go.
8. `dashboard_describe.go` — depends on base.go, helper.go, client.go.
9. `dashboard_build.go` — depends on base.go, helper.go, client.go, confirm.
10. `registry.go` — depends on all tool constructors + safety middleware.
11. `check.go` — depends on client.go, checkup.
12. `suite_test.go` — mock server.
13. `grafana_test.go` — tool tests.
14. `check_test.go` — check tests.
15. `README.md`.

---

## 16. Validation Steps

- `go build ./components/tool/grafana/...`
- `go vet ./components/tool/grafana/...`
- `go test ./components/tool/grafana/...` — all tests pass against mock server.
- Confirm compile-time interface checks present in `registry.go`.
- Confirm every `New...` constructor calls `validate.Struct` after defaults.
- Confirm `Token` field has `json:"-"` (not exposed in schema).
- Confirm `checkProtected` is called in `dashboard_build.go` before the POST.
- Confirm `confirm.RequireConfirmation` is called in `dashboard_build.go`.
- No `osclient` import in any grafana file (it is OpenSearch-only).

---

## 17. Open Questions / Out of Scope

- **Dashboard deletion**: Not required by the spec. A future
  `grafana_dashboard_delete` tool could follow the same write-tool pattern with
  blocklist enforcement.
- **Folder management**: Out of scope. Folders are referenced by UID only.
- **Datasource management**: Out of scope.
- **Grafana API v2 (k8s-style)**: The plan targets the legacy HTTP API
  (`/api/dashboards/db`, `/api/search`) which is stable across Grafana v9–v12.
  The v2 k8s-style API (`/apis/dashboard.grafana.app/`) is marked as the future
  path but the legacy endpoints remain supported.
