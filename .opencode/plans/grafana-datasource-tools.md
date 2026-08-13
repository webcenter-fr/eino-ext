# Plan: Grafana Data Source Tools (`grafana_datasource_list` + `grafana_datasource_describe`)

Add two READ-ONLY eino tools to the existing Grafana component so an LLM can
discover and inspect data sources in order to build dashboards.

- `grafana_datasource_list` — list all data sources on an instance.
- `grafana_datasource_describe` — get full details of one data source by UID.

Both are read-only, reuse the existing `baseTool`/`grafanaClient`/`filterMapMarshal`
patterns, and aggressively redact secrets (the GET endpoint returns full config
including `password`, `basicAuthPassword`, and `secureJsonFields`).

All paths below are under `/projects/eino-ext/components/tool/grafana/`.

---

## 1. Files to create

| File | Purpose |
|---|---|
| `datasource_list.go` | `DataSourceListTool` (list tool) |
| `datasource_describe.go` | `DataSourceDescribeTool` (get-by-UID tool) |
| `redact.go` | `redactSensitiveJSON` recursive JSON-key redactor + helpers |
| `prompts/datasource_list_output_guidance.md` | LLM output-guidance prompt (embedded) |
| `prompts/datasource_describe_output_guidance.md` | LLM output-guidance prompt (embedded) |
| `datasource_list_test.go` | Unit tests for the list tool |
| `datasource_describe_test.go` | Unit tests for the describe tool |

## 2. Files to modify

| File | Change |
|---|---|
| `client.go` | Add `ListDataSources`, `GetDataSource` methods + `dataSource` wire type |
| `helper.go` | `//go:embed` the two new prompt strings |
| `registry.go` | Add both constructors to `readOnlyConstructors`; add `var _` assertions |
| `check.go` | Add both names to `allComponentNames()`; add `probeDataSourceList` + `probeDataSourceDescribe` and wire into `probeInstance` |
| `check_test.go` | Update expected counts `4 → 6` (3 places) |
| `security_test.go` | Add `TestGetDataSourcePathEscape` + redaction tests |
| `suite_test.go` | Add mock handlers for `/api/datasources` and `/api/datasources/uid/:uid` |
| `grafana_test.go` | Add `TestDataSourceList` + `TestDataSourceDescribe` suite tests |
| `integration_test.go` | Add `datasource_list` + `datasource_describe` subtests |
| `README.md` | Add two rows to the tools table |

---

## 3. Design decisions (resolved)

1. **Tool names**: `grafana_datasource_list` and `grafana_datasource_describe`
   — `describe` matches the existing `grafana_dashboard_describe` naming.
2. **Lookup key**: UID only (modern, recommended). The describe params require
   `uid`. Numeric-ID lookup is out of scope (the existing `grafana_dashboard_describe`
   is also UID-only, so this stays consistent). The deprecated
   `GET /api/datasources/:id` endpoint is NOT wired.
3. **List endpoint**: `GET /api/datasources` returns a JSON array, no pagination,
   server caps at 5000 results. No pagination params exposed.
4. **`filter` regex on list**: yes — reuse `filter.Compile` + `filterMapMarshal`,
   identical to `dashboard_search`, so the LLM can narrow large lists.
5. **Redaction strategy** (CRITICAL — the GET endpoint returns full config):
   - Top-level `password`, `basicAuthPassword`, `secureJsonData`: **excluded
     entirely** — they are NOT declared on the `dataSource` wire struct, so Go's
     JSON unmarshaler drops them. They never enter memory as named fields.
   - `secureJsonFields` (`map[string]bool`): **excluded from output** (not useful
     for dashboard building; reduces noise). It is on the wire struct only to be
     explicitly ignored on output.
   - `jsonData` (`map[string]any`): **recursively redacted** — sensitive keys are
     replaced with the placeholder `"<redacted>"` (structure preserved so the
     LLM sees what config exists without leaking values).
   - `user` / `basicAuthUser`: kept (usernames, not secrets) in describe output;
     excluded from the lean list output.
6. **Redactor location**: new file `redact.go` in the grafana package (private
   helper). It is Grafana-specific (curated sensitive-key set). If a second
   component later needs the same recursive key redactor, promote it to
   `libs/toolkit/` — noted as a follow-up, not done now (YAGNI).
7. **Single wire type**: one `dataSource` struct serves both list (as
   `[]dataSource`) and get (as `dataSource`). Missing fields in the list
   response are zero-valued and suppressed via `omitempty` on output.

---

## 4. `client.go` additions

### 4.1 Wire type (append to the "Wire types" section)

```go
// dataSource is a single element of GET /api/datasources and the body of
// GET /api/datasources/uid/:uid. Sensitive top-level fields (password,
// basicAuthPassword, secureJsonData) are intentionally NOT declared here so
// they are dropped during unmarshal and never enter our memory as named fields.
type dataSource struct {
	ID               int64           `json:"id"`
	UID              string          `json:"uid"`
	OrgID            int64           `json:"orgId"`
	Name             string          `json:"name"`
	Type             string          `json:"type"`
	TypeName         string          `json:"typeName,omitempty"`
	TypeLogoURL      string          `json:"typeLogoUrl,omitempty"`
	Access           string          `json:"access"`
	URL              string          `json:"url"`
	User             string          `json:"user"`
	Database         string          `json:"database"`
	BasicAuth        bool            `json:"basicAuth"`
	BasicAuthUser    string          `json:"basicAuthUser,omitempty"`
	WithCredentials  bool            `json:"withCredentials,omitempty"`
	IsDefault        bool            `json:"isDefault"`
	JSONData         map[string]any  `json:"jsonData,omitempty"`
	SecureJSONFields map[string]bool `json:"secureJsonFields,omitempty"` // kept on wire, excluded from output
	ReadOnly         bool            `json:"readOnly"`
	Version          int             `json:"version"`
}
```

> **To verify**: the `GET /api/datasources` list example in the Grafana docs does
> not show `typeName` (only `typeLogoUrl`); the task brief lists `typeName`.
> Declared `omitempty` so it is harmless either way. Newer Grafana versions do
> return `typeName`.

### 4.2 API methods (append to the "API Methods" section)

```go
// ListDataSources calls GET /api/datasources and returns the raw JSON array.
// The endpoint does not support pagination; Grafana caps results at 5000.
func (c *grafanaClient) ListDataSources(ctx context.Context) ([]byte, error) {
	body, _, err := c.doRequest(ctx, http.MethodGet, "/api/datasources", nil)
	if err != nil {
		return nil, errors.Wrap(err, "failed to list data sources")
	}
	return body, nil
}

// GetDataSource calls GET /api/datasources/uid/:uid and returns the raw body.
// The uid is path-escaped to prevent path traversal / endpoint injection
// (e.g. a uid containing ".." or "/" must not alter the request path), mirroring
// GetDashboard.
func (c *grafanaClient) GetDataSource(ctx context.Context, uid string) ([]byte, error) {
	path := "/api/datasources/uid/" + url.PathEscape(uid)
	body, _, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get data source")
	}
	return body, nil
}
```

---

## 5. `redact.go` (new file)

```go
package grafana

import "strings"

// redactedPlaceholder replaces sensitive values in redacted output.
const redactedPlaceholder = "<redacted>"

// sensitiveKeyFragments are case-insensitive substrings; any JSON key whose
// lowercased form contains one of these is treated as secret and redacted.
// Curated for Grafana datasource jsonData: catches httpHeaderValue, apiKey,
// clientSecret, privateKey, accessToken, refreshToken, authToken, etc.
var sensitiveKeyFragments = []string{
	"password", "secret", "token", "privatekey", "apikey",
	"httpheadervalue", "credential", "authorization", "bearer",
}

// sensitiveKeyExact are case-insensitive exact key names treated as secret.
// Kept small and precise to avoid over-redaction (e.g. "authMode" is NOT
// matched here because only the exact key "auth" is).
var sensitiveKeyExact = map[string]bool{
	"auth": true,
	"pass": true,
	"pwd":  true,
}

// isSensitiveKey reports whether key names a secret. Matching is
// case-insensitive: exact match against sensitiveKeyExact, or substring match
// against sensitiveKeyFragments. Over-redaction is intentional and safe here
// (a redacted placeholder is preferable to leaking a secret).
func isSensitiveKey(key string) bool {
	lower := strings.ToLower(key)
	if sensitiveKeyExact[lower] {
		return true
	}
	for _, frag := range sensitiveKeyFragments {
		if strings.Contains(lower, frag) {
			return true
		}
	}
	return false
}

// redactSensitiveJSON recursively walks v and replaces the values of
// sensitive keys (case-insensitive) with redactedPlaceholder. Maps and slices
// are copied; scalars are returned unchanged. A nil map stays nil.
func redactSensitiveJSON(v any) any {
	switch val := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, vv := range val {
			if isSensitiveKey(k) {
				out[k] = redactedPlaceholder
			} else {
				out[k] = redactSensitiveJSON(vv)
			}
		}
		return out
	case []any:
		out := make([]any, len(val))
		for i, vv := range val {
			out[i] = redactSensitiveJSON(vv)
		}
		return out
	default:
		return v
	}
}

// redactedJSONData is a typed convenience wrapper for map[string]any inputs
// (the dataSource.JSONData field). Returns nil for a nil input so the output
// struct's omitempty drops it.
func redactedJSONData(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	r, _ := redactSensitiveJSON(m).(map[string]any)
	return r
}
```

---

## 6. `datasource_list.go` (new file)

```go
package grafana

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/filter"
)

const dataSourceListDescription = `
** General Purpose **
It lists all configured data sources on a Grafana instance.
Use this to discover which data sources (Prometheus, Loki, Elasticsearch, etc.)
are available before building dashboards that reference them.

** Output **
It returns a JSON array of objects, where each object represents a data source with:
- uid: the data source UID (use this to reference the data source in dashboards).
- name: the data source name.
- type: the plugin type (e.g. 'prometheus', 'loki', 'elasticsearch').
- typeName: the human-readable type name (when available).
- url: the data source URL.
- access: the access mode ('proxy' or 'direct').
- isDefault: whether this is the default data source.
- readOnly: whether the data source is read-only.
- version: the data source version.
- jsonData: plugin-specific configuration with sensitive fields redacted.
`

// DataSourceListParams defines the parameters for listing Grafana data sources.
type DataSourceListParams struct {
	Instance string `json:"instance" validate:"required" jsonschema:"(required) The Grafana instance to connect to."`
	Filter   string `json:"filter,omitempty" jsonschema:"(optional) A Go RE2 regex applied on each data source JSON output. Keep only data sources that match the pattern. RE2 does NOT support lookahead (?=...)/(?!...), lookbehind (?<=...)/(?<!...), or backreferences — such patterns return an error. Example: 'prometheus|loki'."`
}

// DataSourceListOutput is the structured output for a single data source in a list.
// Sensitive fields (password, basicAuthPassword, secureJsonFields) are excluded;
// jsonData is recursively redacted.
type DataSourceListOutput struct {
	ID        int64          `json:"id"`
	UID       string         `json:"uid"`
	Name      string         `json:"name"`
	Type      string         `json:"type"`
	TypeName  string         `json:"typeName,omitempty"`
	URL       string         `json:"url"`
	Access    string         `json:"access"`
	IsDefault bool           `json:"isDefault"`
	ReadOnly  bool           `json:"readOnly"`
	Version   int            `json:"version"`
	JSONData  map[string]any `json:"jsonData,omitempty"`
}

// DataSourceListTool is an eino tool for listing Grafana data sources.
type DataSourceListTool struct {
	*baseTool
	tool.InvokableTool
}

// Invoke lists all data sources on the given Grafana instance.
func (t *DataSourceListTool) Invoke(ctx context.Context, params *DataSourceListParams) (result string, err error) {
	if err := validateParams(params); err != nil {
		return "", err
	}

	re, err := filter.Compile(params.Filter)
	if err != nil {
		return "", errors.Wrap(err, "error when compile regex")
	}

	c, err := t.client(params.Instance)
	if err != nil {
		return "", err
	}

	body, err := c.ListDataSources(ctx)
	if err != nil {
		return "", errors.Wrap(err, "failed to list data sources")
	}

	var sources []dataSource
	if err := json.Unmarshal(body, &sources); err != nil {
		return "", errors.Wrap(err, "failed to unmarshal data sources")
	}

	return filterMapMarshal(sources, re, func(item dataSource) DataSourceListOutput {
		return DataSourceListOutput{
			ID:        item.ID,
			UID:       item.UID,
			Name:      item.Name,
			Type:      item.Type,
			TypeName:  item.TypeName,
			URL:       item.URL,
			Access:    item.Access,
			IsDefault: item.IsDefault,
			ReadOnly:  item.ReadOnly,
			Version:   item.Version,
			JSONData:  redactedJSONData(item.JSONData),
		}
	})
}

// NewDataSourceListTool creates a new DataSourceListTool.
func NewDataSourceListTool(ctx context.Context, configs Configs) (*DataSourceListTool, error) {
	base, err := newBaseTool(ctx, configs)
	if err != nil {
		return nil, err
	}

	listTool := &DataSourceListTool{baseTool: base}
	t, err := utils.InferTool("grafana_datasource_list", fmt.Sprintf("%s\n%s", dataSourceListDescription, dataSourceListOutputGuidance), listTool.Invoke)
	if err != nil {
		return nil, err
	}
	listTool.InvokableTool = t

	return listTool, nil
}
```

> Imports actually needed: `context`, `fmt`, `emperror.dev/errors`, `tool`,
> `utils`, `github.com/goccy/go-json`, `filter`. (Same import block as
> `dashboard_search.go`.)

---

## 7. `datasource_describe.go` (new file)

```go
package grafana

import (
	"context"
	"fmt"
	"net/http"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/goccy/go-json"
)

const dataSourceDescribeDescription = `
** General Purpose **
It gets the full details of a specific Grafana data source by its UID.
Use this to inspect a data source's configuration (e.g. default database, region,
time field) before building dashboards that query it.

** Output **
It returns a JSON object with the data source's full configuration. Sensitive
fields (passwords, tokens, secrets) are excluded or redacted.
`

// DataSourceDescribeParams defines the parameters for describing a Grafana data source.
type DataSourceDescribeParams struct {
	Instance string `json:"instance" validate:"required" jsonschema:"(required) The Grafana instance to connect to."`
	UID      string `json:"uid" validate:"required" jsonschema:"(required) The data source UID."`
}

// DataSourceDescribeOutput is the structured output for a data source describe.
// Sensitive top-level fields (password, basicAuthPassword, secureJsonFields,
// secureJsonData) are excluded; jsonData is recursively redacted.
type DataSourceDescribeOutput struct {
	ID              int64          `json:"id"`
	UID             string         `json:"uid"`
	OrgID           int64          `json:"orgId"`
	Name            string         `json:"name"`
	Type            string         `json:"type"`
	TypeName        string         `json:"typeName,omitempty"`
	TypeLogoURL     string         `json:"typeLogoUrl,omitempty"`
	Access          string         `json:"access"`
	URL             string         `json:"url"`
	User            string         `json:"user"`
	Database        string         `json:"database"`
	BasicAuth       bool           `json:"basicAuth"`
	BasicAuthUser   string         `json:"basicAuthUser,omitempty"`
	WithCredentials bool           `json:"withCredentials,omitempty"`
	IsDefault       bool           `json:"isDefault"`
	JSONData        map[string]any `json:"jsonData,omitempty"`
	ReadOnly        bool           `json:"readOnly"`
	Version         int            `json:"version"`
}

// DataSourceDescribeTool is an eino tool for describing a Grafana data source.
type DataSourceDescribeTool struct {
	*baseTool
	tool.InvokableTool
}

// Invoke returns the full details of a Grafana data source by UID.
func (t *DataSourceDescribeTool) Invoke(ctx context.Context, params *DataSourceDescribeParams) (result string, err error) {
	if err := validateParams(params); err != nil {
		return "", err
	}

	c, err := t.client(params.Instance)
	if err != nil {
		return "", err
	}

	body, err := c.GetDataSource(ctx, params.UID)
	if err != nil {
		// Surface a clear not-found error for 404, propagate everything else.
		if isHTTPStatus(err, http.StatusNotFound) {
			return "", errors.Wrapf(err, "data source with UID %q not found", params.UID)
		}
		return "", errors.Wrap(err, "failed to get data source")
	}

	var ds dataSource
	if err := json.Unmarshal(body, &ds); err != nil {
		return "", errors.Wrap(err, "failed to unmarshal data source")
	}

	output := DataSourceDescribeOutput{
		ID:              ds.ID,
		UID:             ds.UID,
		OrgID:           ds.OrgID,
		Name:            ds.Name,
		Type:            ds.Type,
		TypeName:        ds.TypeName,
		TypeLogoURL:     ds.TypeLogoURL,
		Access:          ds.Access,
		URL:             ds.URL,
		User:            ds.User,
		Database:        ds.Database,
		BasicAuth:       ds.BasicAuth,
		BasicAuthUser:   ds.BasicAuthUser,
		WithCredentials: ds.WithCredentials,
		IsDefault:       ds.IsDefault,
		JSONData:        redactedJSONData(ds.JSONData),
		ReadOnly:        ds.ReadOnly,
		Version:         ds.Version,
	}

	data, err := json.Marshal(output)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal output")
	}

	return string(data), nil
}

// NewDataSourceDescribeTool creates a new DataSourceDescribeTool.
func NewDataSourceDescribeTool(ctx context.Context, configs Configs) (*DataSourceDescribeTool, error) {
	base, err := newBaseTool(ctx, configs)
	if err != nil {
		return nil, err
	}

	describeTool := &DataSourceDescribeTool{baseTool: base}
	t, err := utils.InferTool("grafana_datasource_describe", fmt.Sprintf("%s\n%s", dataSourceDescribeDescription, dataSourceDescribeOutputGuidance), describeTool.Invoke)
	if err != nil {
		return nil, err
	}
	describeTool.InvokableTool = t

	return describeTool, nil
}
```

---

## 8. `helper.go` changes

Add two `//go:embed` lines next to the existing ones:

```go
//go:embed prompts/datasource_list_output_guidance.md
var dataSourceListOutputGuidance string

//go:embed prompts/datasource_describe_output_guidance.md
var dataSourceDescribeOutputGuidance string
```

### `prompts/datasource_list_output_guidance.md` (new)

```markdown
** How to limit output (IMPORTANT) **
The list endpoint returns all data sources (no pagination, max 5000). Narrow it:
- Use `filter` (Go RE2 regex, applied on each data source JSON output) to keep
  only matches, e.g. 'prometheus|loki' to restrict to those plugin types. RE2
  does NOT support lookahead (?=...)/(?!...), lookbehind (?<=...)/(?<!...), or
  backreferences — such patterns return an error. Prefer simple alternations.
- If you only need a data source's UID/type/name, prefer this list tool over
  describe. Call describe only when you need the full jsonData configuration.
```

### `prompts/datasource_describe_output_guidance.md` (new)

```markdown
** How to use the output (IMPORTANT) **
- The `uid` and `type` fields are what you need to reference this data source in
  a dashboard panel's `datasource` object (e.g. `{"type":"prometheus","uid":"..."}`).
- `jsonData` holds plugin-specific config (e.g. `timeField`, `timeInterval`,
  `region`, `database`) useful for writing queries. Sensitive sub-fields are
  redacted to "<redacted>" and must not be sent back in any request.
- Sensitive top-level fields (passwords, tokens) are excluded entirely.
```

---

## 9. `registry.go` changes

Add to `readOnlyConstructors` (after the dashboard_describe entry):

```go
func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewDataSourceListTool(ctx, c) },
func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewDataSourceDescribeTool(ctx, c) },
```

Add to the `var (` assertion block:

```go
_ tool.InvokableTool = (*DataSourceListTool)(nil)
_ tool.InvokableTool = (*DataSourceDescribeTool)(nil)
```

`WriteToolNames()` is unchanged — both new tools are read-only.

---

## 10. `check.go` changes

### `allComponentNames()` — add the two names (6 total, order matches probe order):

```go
func allComponentNames() []string {
	return []string{
		"grafana_instance_list",
		"grafana_dashboard_search",
		"grafana_dashboard_describe",
		"grafana_dashboard_build",
		"grafana_datasource_list",
		"grafana_datasource_describe",
	}
}
```

### `probeInstance` — append datasource probes after the dashboard_build block:

```go
// grafana_datasource_list
listResult, firstDSUID, err := probeDataSourceList(ctx, client, instance)
results = append(results, listResult)

if err == nil && firstDSUID != "" {
	results = append(results, probeDataSourceDescribe(ctx, client, instance, firstDSUID))
} else if err == nil {
	results = append(results, checkup.Result{
		Component: "grafana_datasource_describe",
		Instance:  instance,
		Status:    checkup.StatusLimited,
		Message:   "no data sources to test describe",
	})
} else {
	results = append(results, checkup.Result{
		Component: "grafana_datasource_describe",
		Instance:  instance,
		Status:    checkup.StatusError,
		Error:     "dependency failed",
	})
}
```

### New probe helpers (mirror `probeSearch`/`probeDescribe`):

```go
func probeDataSourceList(ctx context.Context, client *grafanaClient, instance string) (checkup.Result, string, error) {
	body, err := client.ListDataSources(ctx)
	if err != nil {
		return checkup.Result{
			Component: "grafana_datasource_list",
			Instance:  instance,
			Status:    checkup.StatusError,
			Error:     errors.Wrap(err, "failed to list data sources").Error(),
		}, "", err
	}

	var sources []dataSource
	if err := json.Unmarshal(body, &sources); err != nil {
		return checkup.Result{
			Component: "grafana_datasource_list",
			Instance:  instance,
			Status:    checkup.StatusError,
			Error:     errors.Wrap(err, "failed to unmarshal data sources").Error(),
		}, "", err
	}

	firstUID := ""
	if len(sources) > 0 {
		firstUID = sources[0].UID
	}

	return checkup.Result{
		Component: "grafana_datasource_list",
		Instance:  instance,
		Status:    checkup.StatusOK,
		Message:   fmt.Sprintf("%d data sources found, RBAC ok", len(sources)),
	}, firstUID, nil
}

func probeDataSourceDescribe(ctx context.Context, client *grafanaClient, instance, uid string) checkup.Result {
	body, err := client.GetDataSource(ctx, uid)
	if err != nil {
		return checkup.Result{
			Component: "grafana_datasource_describe",
			Instance:  instance,
			Status:    checkup.StatusError,
			Error:     errors.Wrap(err, "failed to get data source").Error(),
		}
	}

	var ds dataSource
	if err := json.Unmarshal(body, &ds); err != nil {
		return checkup.Result{
			Component: "grafana_datasource_describe",
			Instance:  instance,
			Status:    checkup.StatusError,
			Error:     errors.Wrap(err, "failed to unmarshal data source").Error(),
		}
	}

	return checkup.Result{
		Component: "grafana_datasource_describe",
		Instance:  instance,
		Status:    checkup.StatusOK,
		Message:   fmt.Sprintf("described data source %q (type %s), RBAC ok", ds.Name, ds.Type),
	}
}
```

---

## 11. `check_test.go` changes

Update the three hardcoded `4` counts to `6`:

- `TestCheckInvalidInstance`: `if len(results) != 4` → `!= 6`.
- `TestCheckClientErrorResults`: `if len(r) != 4` → `!= 6` (and the
  `clientErrorResults` make uses `len(allComponentNames())` so it auto-adapts,
  but the assertion must be updated).
- `TestAllComponentNames`: `if len(names) != 4` → `!= 6`.

---

## 12. `security_test.go` additions

### `TestGetDataSourcePathEscape` (mirror `TestGetDashboardPathEscape`)

- Hit a test server, call `c.GetDataSource(ctx, "../ds")`, assert the captured
  `r.RequestURI` equals `/api/datasources/uid/..%2Fds`.
- Call with `"foo/bar"` → assert `/api/datasources/uid/foo%2Fbar`.
- Call with `"foo?bar=baz"` → assert `/api/datasources/uid/foo%3Fbar=baz`.

### `TestRedactSensitiveJSON` (table-driven)

- `{"password":"x","token":"y","apiKey":"z","httpHeaderValue":"h"}` → all four
  values become `"<redacted>"`.
- `{"auth":"x","name":"prometheus"}` → `auth` redacted, `name` kept.
- Nested: `{"jsonData":{"clientSecret":"s","timeField":"@timestamp"}}` →
  `clientSecret` redacted, `timeField` kept.
- Slice of maps: `[{"accessToken":"a"},{"name":"n"}]` → first redacted, second kept.
- Case-insensitivity: `{"APIKey":"a","PassWord":"p"}` → both redacted.
- Substring safety: `{"authMode":"oidc"}` → NOT redacted (only exact `auth`
  matches; `authMode` contains no fragment from `sensitiveKeyFragments`).
  Verify this expectation explicitly to guard against over-redaction regressions.
- nil map input to `redactedJSONData(nil)` → returns nil.

### `TestDataSourceDescribeExcludesSecrets` (end-to-end via httptest)

- Serve a `GET /api/datasources/uid/sec-1` response that includes top-level
  `password`, `basicAuthPassword`, `secureJsonFields`, and a `jsonData` with
  `httpHeaderValue` + `timeField`.
- Invoke `NewDataSourceDescribeTool`, run with `{"instance":"t","uid":"sec-1"}`.
- Assert the result string does NOT contain `"password"`, `"basicAuthPassword"`,
  `"secureJsonFields"`, or the raw secret values; assert it contains
  `"<redacted>"` and `"timeField"` with its real value.

---

## 13. `suite_test.go` additions (mock handlers)

Add to the `SetupSuite` mux:

```go
// GET /api/datasources — list
mux.HandleFunc("/api/datasources", func(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`[
		{
			"id": 1, "uid": "ds-prom", "orgId": 1, "name": "Prometheus",
			"type": "prometheus", "typeName": "Prometheus", "access": "proxy",
			"url": "http://prom:9090", "isDefault": true, "readOnly": false,
			"version": 3, "password": "should-not-leak",
			"jsonData": {"timeInterval":"15s","httpHeaderValue":"secret-bearer"}
		},
		{
			"id": 2, "uid": "ds-loki", "orgId": 1, "name": "Loki",
			"type": "loki", "typeName": "Loki", "access": "proxy",
			"url": "http://loki:3100", "isDefault": false, "readOnly": false,
			"version": 1,
			"jsonData": {"maxLines": 1000}
		}
	]`))
})

// GET /api/datasources/uid/ds-prom
mux.HandleFunc("/api/datasources/uid/ds-prom", func(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{
		"id": 1, "uid": "ds-prom", "orgId": 1, "name": "Prometheus",
		"type": "prometheus", "typeName": "Prometheus", "typeLogoUrl": "public/app/plugins/datasource/prometheus/img/prom.svg",
		"access": "proxy", "url": "http://prom:9090", "user": "", "database": "",
		"basicAuth": false, "withCredentials": false, "isDefault": true,
		"jsonData": {"timeInterval":"15s","httpHeaderValue":"secret-bearer"},
		"secureJsonFields": {"httpHeaderValue": true},
		"readOnly": false, "version": 3, "password": "should-not-leak", "basicAuthPassword": ""
	}`))
})

// GET /api/datasources/uid/nonexistent → 404
mux.HandleFunc("/api/datasources/uid/nonexistent", func(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte(`{"message":"data source not found"}`))
})
```

> Note: `httptest` mux uses exact path matching for `/api/datasources` and
> `/api/datasources/uid/ds-prom`; the `/api/datasources/uid/nonexistent`
> handler is distinct from `ds-prom`. No conflict with `/api/dashboards/...`.

---

## 14. `grafana_test.go` additions (suite tests)

### `TestDataSourceList`

- `list all` → 2 outputs; first has `UID == "ds-prom"`, `Type == "prometheus"`,
  `IsDefault == true`; result does NOT contain `"should-not-leak"` or
  `"secret-bearer"`; result contains `"<redacted>"` (from `httpHeaderValue`).
- `list with filter` → `{"instance":"test","filter":"loki"}` → 1 output, name
  `Loki`.
- `unknown instance` → error.
- `invalid filter regex` → `{"instance":"test","filter":"(?=...)"}` → error.

### `TestDataSourceDescribe`

- `describe existing` → contains `"ds-prom"`, `"prometheus"`, `"timeInterval"`,
  `"15s"`; does NOT contain `"should-not-leak"`, `"secret-bearer"`,
  `"secureJsonFields"`, `"password"`; contains `"<redacted>"`.
- `nonexistent uid` → `{"instance":"test","uid":"nonexistent"}` → error
  containing `not found`.
- `unknown instance` → error.

---

## 15. `integration_test.go` additions

Add a `datasource_list` and `datasource_describe` section (READ, before the
write tests), mirroring the dashboard sections:

- `datasource_list`: `InvokableRun(ctx, '{"instance":"test"}')` → unmarshal to
  `[]DataSourceListOutput`, assert `len >= 1`; assert no output field equals
  the raw token/password env values.
- `datasource_describe`: pick the first UID from the list result, describe it,
  assert the response contains `uid` and `type`; assert the redacted placeholder
  appears for any sensitive jsonData key the real instance returns (best-effort:
  assert no field literally named `password`/`basicAuthPassword` appears).

---

## 16. `README.md` changes

Add two rows to the "Available Tools" table:

```markdown
| `grafana_datasource_list` | Read | List all data sources on an instance (secrets redacted) |
| `grafana_datasource_describe` | Read | Get full data source details by UID (secrets redacted) |
```

No factory-function changes needed (the new tools are auto-included by
`NewAllTools` / `NewReadOnlyTools` via `readOnlyConstructors`). Optionally add a
one-line note under "Design" that data source tools redact sensitive fields.

---

## 17. Edge cases & error handling

| Case | Behavior |
|---|---|
| Unknown instance | `t.client(instance)` → `instanceNotFoundError` (via `toolutil.NotFoundError`). |
| Unknown UID (404) | `GetDataSource` returns `*httpError` with status 404; describe wraps as `data source with UID %q not found`. |
| Empty data source list | List returns `[]` (valid JSON empty array); describe probe reports `StatusLimited` ("no data sources to test describe"). |
| Malformed JSON | `json.Unmarshal` error wrapped with operation context. |
| Timeout | `doRequest` already applies `c.timeout` via `context.WithTimeout`; surfaces as a wrapped request error. |
| UID with path-traversal chars | `url.PathEscape(uid)` in `GetDataSource` (same defense as `GetDashboard`); covered by `TestGetDataSourcePathEscape`. |
| Sensitive fields in response | Excluded (top-level) or redacted (jsonData) — see §5. |
| Large list (>5000) | Grafana caps at 5000; no client-side pagination available. Documented in the list output guidance prompt. |

---

## 18. Validation plan

1. `go build ./...` — compiles, no unused imports.
2. `go vet ./...` — clean.
3. `go test ./components/tool/grafana/...` — all unit tests pass, including:
   - new `datasource_*_test.go`,
   - updated `check_test.go` (counts = 6),
   - updated `security_test.go` (path escape + redaction + e2e exclusion),
   - updated `grafana_test.go` suite tests.
4. `go test -tags integration ./components/tool/grafana/...` (with `GRAFANA_TOKEN`
   set against a live instance) — datasource list + describe subtests pass and
   no secret leaks in output.
5. Manual redaction spot-check: a describe response containing
   `password`/`basicAuthPassword`/`secureJsonFields`/`jsonData.httpHeaderValue`
   must produce output with none of those literal secret values and must
   contain `"<redacted>"` for `httpHeaderValue`.

---

## 19. Implementation order

1. `client.go` — add `dataSource` wire type + `ListDataSources` + `GetDataSource`.
2. `redact.go` — add `redactSensitiveJSON` + `redactedJSONData` + `isSensitiveKey`.
3. `prompts/datasource_list_output_guidance.md` +
   `prompts/datasource_describe_output_guidance.md`.
4. `helper.go` — add the two `//go:embed` vars.
5. `datasource_list.go` — tool + constructor.
6. `datasource_describe.go` — tool + constructor.
7. `registry.go` — wire constructors + `var _` assertions.
8. `check.go` — `allComponentNames()` + `probeDataSourceList` +
   `probeDataSourceDescribe` + wire into `probeInstance`.
9. `check_test.go` — update counts to 6.
10. `security_test.go` — path-escape + redaction + e2e exclusion tests.
11. `suite_test.go` — mock handlers.
12. `datasource_list_test.go` + `datasource_describe_test.go` — unit tests.
13. `grafana_test.go` — suite tests.
14. `integration_test.go` — integration subtests.
15. `README.md` — tools table.
16. Run `go build ./... && go vet ./... && go test ./...`.

---

## 20. Out of scope / future

- Numeric-ID lookup (`GET /api/datasources/:id`) — deprecated; not wired.
- Data source health check (`GET /api/datasources/uid/:uid/health`) — could be a
  future read tool.
- Promoting `redactSensitiveJSON` to `libs/toolkit/` — defer until a second
  component needs it.
- Per-instance data source allow/deny list (analogous to
  `ProtectedDashboards`) — not needed for read-only tools.
