# Implementation Plan: Fix `grafana_dashboard_write` Version Mismatch (HTTP 412)

## Summary

`grafana_dashboard_write` returns spurious HTTP 412 `version-mismatch` errors because the tool never resolves the current dashboard version before saving. Grafana's `POST /api/dashboards/db` performs optimistic-concurrency checking on `dashboard.version`. An absent `version` reads as `0`, which never matches an existing dashboard. The tool must resolve the version at execute time.

This plan also fixes: stale `id` passthrough (can redirect write to wrong dashboard), dry-run preview key mismatch (`folderUID` vs `folderUid`), and unhelpful conflict error messages.

---

## File Changes (all relative to `/projects/eino-ext/components/tool/grafana/`)

| File | Change |
|------|--------|
| `client.go` | Add `ErrVersionMismatch` sentinel, wrap 412 in `SaveDashboard`, add `dashboardVersion()` helper |
| `dashboard_write.go` | Resolve version before save, strip stale `id`, fix dry-run key, actionable conflict error, description/jsonschema updates |
| `dashboard_write_test.go` | 8 new test cases |

No changes to `base.go`, `helper.go`, `config.go`, or `prompts/` files (no dashboard-write-specific prompts exist; the only prompt mentioning dashboard_write is `dashboard_validate_output_guidance.md` which only references it in a workflow recommendation and does not need changes).

---

## 1. `client.go` Changes

### 1.1 Add `ErrVersionMismatch` sentinel error

Add after the existing `maxErrorBodyLen`/`maxResponseBodyLen` constants (around line 98):

```go
// ErrVersionMismatch is returned by SaveDashboard when Grafana rejects the
// save because the submitted dashboard.version is not the current one
// (HTTP 412, status "version-mismatch").
var ErrVersionMismatch = errors.New("dashboard version mismatch")
```

### 1.2 Wrap 412 responses in `SaveDashboard`

Replace the existing `SaveDashboard` method (lines 367-373):

```go
// SaveDashboard calls POST /api/dashboards/db with the given payload.
// Returns the raw response body (contains id, uid, url, version, status).
// Returns ErrVersionMismatch when Grafana responds with HTTP 412
// (version-mismatch), wrapped with the raw response body.
func (c *grafanaClient) SaveDashboard(ctx context.Context, payload []byte) ([]byte, error) {
	body, status, err := c.doRequest(ctx, http.MethodPost, "/api/dashboards/db", strings.NewReader(string(payload)))
	if err != nil {
		if status == http.StatusPreconditionFailed {
			return nil, fmt.Errorf("%w: %s", ErrVersionMismatch, err.Error())
		}
		return nil, errors.Wrap(err, "failed to save dashboard")
	}
	return body, nil
}
```

**Important**: The `doRequest` signature returns `([]byte, int, error)` — the status code is available as the second return value even when `err != nil`. We use `http.StatusPreconditionFailed` (412).

**Import addition**: Add `"fmt"` to the imports block (already imported in `client.go`).

### 1.3 Add `dashboardVersion()` helper method

Add after `SaveDashboard` (around line 373):

```go
// dashboardVersion returns the current version of the dashboard identified
// by uid. Returns (0, false, nil) when the dashboard does not exist (HTTP 404).
func (c *grafanaClient) dashboardVersion(ctx context.Context, uid string) (int, bool, error) {
	body, err := c.GetDashboard(ctx, uid)
	if err != nil {
		if isHTTPStatus(err, http.StatusNotFound) {
			return 0, false, nil
		}
		return 0, false, err
	}

	var dr dashboardResponse
	if err := json.Unmarshal(body, &dr); err != nil {
		return 0, false, errors.Wrap(err, "failed to unmarshal dashboard response")
	}

	return dr.Meta.Version, true, nil
}
```

**Note**: This reuses the existing `GetDashboard` method. `GetDashboard` already wraps errors from `doRequest` with "failed to get dashboard". The `isHTTPStatus` check unwraps through that to find the underlying 404 `*httpError`.

---

## 2. `dashboard_write.go` Changes

### 2.1 Resolve version before save (create/update branch)

Insert the version-resolution block **after** the `checkProtectedModel` call and **before** the `DryRun` short-circuit, so that:
- The blocklist check still sees the original caller-submitted model.
- Dry-run performs no extra GET requests (no version read).
- The version is resolved at execute time, not at preview time.

The insertion point is between the closing `}` of the `checkProtectedModel` block (line 130) and the `if params.DryRun` block (line 132).

Insert this block:

```go
		// Resolve the current dashboard version at execute time.
		// Grafana's save endpoint enforces optimistic concurrency on
		// dashboard.version: a stale — or missing, which reads as 0 —
		// version is rejected with 412 version-mismatch. Callers (and
		// LLMs) cannot be relied on to carry the right version, and
		// under a dry-run/approve flow the model is frozen before
		// approval, so resolve the version here, at execute time.
		//
		// This runs ONLY at execute time (after the dry-run
		// short-circuit below) and only when the caller has not opted
		// out with overwrite=true.
		if uid != "" && !params.Overwrite {
			current, exists, err := c.dashboardVersion(ctx, uid)
			if err != nil {
				return "", errors.Wrapf(err, "failed to read current version of dashboard %q", uid)
			}
			if !exists {
				// Dashboard was deleted between the protection
				// check and now. Treat as a fresh create:
				// strip any inherited version/id.
				delete(dashboardModel, "version")
				delete(dashboardModel, "id")
			} else {
				// Inject the current version so Grafana's
				// optimistic-concurrency check passes.
				dashboardModel["version"] = current
				// Strip stale numeric id: Grafana upserts on
				// uid; a stale id inherited by copying another
				// dashboard's JSON can retarget the write.
				delete(dashboardModel, "id")
			}
		}
```

**Important**: This block MUST be placed **after** the `if params.DryRun` block. The current code structure is:

```
checkProtectedModel(...)     // line 128-130
if params.DryRun { ... }     // line 132-140
req := saveDashboardRequest{ // line 142
```

The version resolution goes **between** the dry-run return and the `req` construction, so it only executes at execute time. Correct insertion: after line 140 (`}` closing the dry-run block) and before line 142 (`req := saveDashboardRequest{`).

### 2.2 Actionable conflict error message

Replace the `SaveDashboard` error handling block (lines 154-157):

```go
		body, err := c.SaveDashboard(ctx, payload)
		if err != nil {
			if errors.Is(err, ErrVersionMismatch) {
				submittedVersion := dashboardModel["version"]
				return "", errors.Wrapf(err,
					"dashboard %q was modified concurrently: the tool submitted version %v, "+
						"which Grafana rejected. Re-read the dashboard, re-apply your change on "+
						"top of the newer model, and retry. Set overwrite=true only to "+
						"deliberately discard the concurrent change",
					uid, submittedVersion)
			}
			return "", errors.Wrap(err, "failed to save dashboard")
		}
```

### 2.3 Fix dry-run preview key: `folderUID` → `folderUid`

In the dry-run preview map (line 137), change the key:

```
				"folderUID": params.FolderUID,
```
to:
```
				"folderUid": params.FolderUID,
```

Also add a `versionResolvedAtExecute` indicator to the dry-run preview when version resolution applies. Update the dry-run return block (lines 132-140) to:

```go
		if params.DryRun {
			preview := map[string]any{
				"dryRun":    true,
				"operation": params.Operation,
				"dashboard": dashboardModel,
				"folderUid": params.FolderUID,
				"overwrite": params.Overwrite,
			}
			if uid != "" && !params.Overwrite {
				preview["versionResolvedAtExecute"] = true
			}
			return marshalJSON(preview, "failed to marshal dry-run preview")
		}
```

### 2.4 Update tool description (`dashboardWriteDescription`)

Replace the existing `dashboardWriteDescription` constant (lines 14-36). Under `** Safety **`, add guidance about version management:

```go
const dashboardWriteDescription = `
** General Purpose **
A single tool that creates, updates, or deletes a Grafana dashboard. The
required 'operation' param selects the action:

- create: POST a new dashboard model to /api/dashboards/db.
- update: POST an updated dashboard model to /api/dashboards/db (Grafana upsert
  semantics; include a 'uid' to target an existing dashboard).
- delete: DELETE /api/dashboards/uid/:uid.

** Safety **
This is a write tool. Always use dryRun=true first to preview the resolved
payload before saving/deleting. After reviewing, set confirmed=true to execute.

Do NOT include 'version' or 'id' in the dashboard model — the tool resolves the
current version automatically at execute time. Set overwrite=true only when the
user explicitly asks to force the save and discard any concurrent modifications;
otherwise leave it false.

** Dashboard Protection **
Dashboards matching the instance's protected blocklist (by UID, title prefix,
folder, or tag) cannot be modified or deleted.

** Output **
create/update returns a JSON object with the saved dashboard's UID, URL,
version, and status. delete returns a JSON object with the deleted dashboard's
title and message.
`
```

### 2.5 Update `DashboardWriteParams.Overwrite` jsonschema

Change line 47 from:

```go
	Overwrite bool   `json:"overwrite,omitempty" jsonschema:"(optional, create/update) Overwrite without version checking."`
```

to:

```go
	Overwrite bool   `json:"overwrite,omitempty" jsonschema:"(optional, create/update) Force the save, discarding any concurrent modification. Leave false unless the user explicitly asked to force it; the tool resolves the current version automatically."`
```

### 2.6 Update `DashboardWriteParams.Dashboard` jsonschema

Change line 43 from:

```go
	Dashboard string `json:"dashboard,omitempty" validate:"omitempty,max=1048576" jsonschema:"(optional, create/update) The full Grafana dashboard model as a JSON string. Must include 'title'. Include 'uid' to target an existing dashboard (update). Ignored for delete."`
```

to:

```go
	Dashboard string `json:"dashboard,omitempty" validate:"omitempty,max=1048576" jsonschema:"(optional, create/update) The full Grafana dashboard model as a JSON string. Must include 'title'. Include 'uid' to target an existing dashboard (update). Do NOT include 'version' or 'id' — they are resolved by the tool. Ignored for delete."`
```

---

## 3. `dashboard_write_test.go` Changes

Add the following 8 test cases. Place them after `TestDashboardWriteToolProtection` (after line 328) and before `TestDashboardWriteToolConstructor` (before line 330).

### 3.1 Test helper: request body capture

The existing tests use `io.ReadAll(r.Body)` in some places. For the new tests, we'll use a consistent pattern. Add this helper function after the existing `newDashboardWriteTool` (around line 29):

```go
// captureBody returns an http.HandlerFunc that captures the POST request body
// into *captured (when non-nil) and responds with the given status and body.
func captureBody(captured *[]byte, status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if captured != nil && r.Body != nil {
			b, _ := io.ReadAll(r.Body)
			*captured = b
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}
```

### 3.2 Test 1: Version is injected on update

```go
func TestDashboardWriteToolVersionInjection(t *testing.T) {
	// Stub GET returning version 7, then capture the POST body.
	var gotPOST []byte

	mux := http.NewServeMux()
	mux.HandleFunc("/api/dashboards/uid/abc123", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"dashboard":{"uid":"abc123","title":"T"},"meta":{"version":7}}`))
	})
	mux.HandleFunc("/api/dashboards/db", captureBody(&gotPOST, http.StatusOK,
		`{"id":10,"uid":"abc123","url":"/d/abc123/slug","status":"success","version":8,"slug":"slug"}`))

	server := httptest.NewServer(mux)
	defer server.Close()

	tool, err := NewDashboardWriteTool(context.Background(), Configs{"test": {URL: server.URL}})
	require.NoError(t, err)

	// Model with no version — tool must inject version 7 from GET.
	_, err = tool.Invoke(context.Background(), &DashboardWriteParams{
		Instance:  "test",
		Operation: "update",
		Dashboard: `{"uid":"abc123","title":"T"}`,
		Confirmed: true,
	})
	require.NoError(t, err)

	var req saveDashboardRequest
	require.NoError(t, json.Unmarshal(gotPOST, &req))
	v, _ := req.Dashboard["version"].(float64)
	assert.Equal(t, float64(7), v, "version must be injected from GET response")
	assert.False(t, req.Overwrite, "overwrite must remain false")
}
```

### 3.3 Test 2: Stale version is replaced

```go
func TestDashboardWriteToolStaleVersionReplaced(t *testing.T) {
	var gotPOST []byte

	mux := http.NewServeMux()
	mux.HandleFunc("/api/dashboards/uid/abc123", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"dashboard":{"uid":"abc123","title":"T"},"meta":{"version":7}}`))
	})
	mux.HandleFunc("/api/dashboards/db", captureBody(&gotPOST, http.StatusOK,
		`{"id":10,"uid":"abc123","url":"/d/abc123/slug","status":"success","version":8,"slug":"slug"}`))

	server := httptest.NewServer(mux)
	defer server.Close()

	tool, err := NewDashboardWriteTool(context.Background(), Configs{"test": {URL: server.URL}})
	require.NoError(t, err)

	// Model carries stale version 3 — tool must replace with 7.
	_, err = tool.Invoke(context.Background(), &DashboardWriteParams{
		Instance:  "test",
		Operation: "update",
		Dashboard: `{"uid":"abc123","title":"T","version":3}`,
		Confirmed: true,
	})
	require.NoError(t, err)

	var req saveDashboardRequest
	require.NoError(t, json.Unmarshal(gotPOST, &req))
	v, _ := req.Dashboard["version"].(float64)
	assert.Equal(t, float64(7), v, "stale version 3 must be replaced with current version 7")
}
```

### 3.4 Test 3: Stale id is stripped

```go
func TestDashboardWriteToolStaleIDStripped(t *testing.T) {
	var gotPOST []byte

	mux := http.NewServeMux()
	mux.HandleFunc("/api/dashboards/uid/abc123", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"dashboard":{"uid":"abc123","title":"T"},"meta":{"version":5}}`))
	})
	mux.HandleFunc("/api/dashboards/db", captureBody(&gotPOST, http.StatusOK,
		`{"id":10,"uid":"abc123","url":"/d/abc123/slug","status":"success","version":6,"slug":"slug"}`))

	server := httptest.NewServer(mux)
	defer server.Close()

	tool, err := NewDashboardWriteTool(context.Background(), Configs{"test": {URL: server.URL}})
	require.NoError(t, err)

	// Model carries stale id 1471 — tool must strip it.
	_, err = tool.Invoke(context.Background(), &DashboardWriteParams{
		Instance:  "test",
		Operation: "update",
		Dashboard: `{"uid":"abc123","title":"T","id":1471}`,
		Confirmed: true,
	})
	require.NoError(t, err)

	var req saveDashboardRequest
	require.NoError(t, json.Unmarshal(gotPOST, &req))
	_, hasID := req.Dashboard["id"]
	assert.False(t, hasID, "stale id must be stripped from the POST body")
	v, _ := req.Dashboard["version"].(float64)
	assert.Equal(t, float64(5), v, "version must still be injected")
}
```

### 3.5 Test 4: overwrite=true passes through untouched

```go
func TestDashboardWriteToolOverwritePassthrough(t *testing.T) {
	var gotPOST []byte
	getCalled := false

	mux := http.NewServeMux()
	mux.HandleFunc("/api/dashboards/uid/abc123", func(w http.ResponseWriter, r *http.Request) {
		getCalled = true
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"dashboard":{"uid":"abc123","title":"T"},"meta":{"version":7}}`))
	})
	mux.HandleFunc("/api/dashboards/db", captureBody(&gotPOST, http.StatusOK,
		`{"id":10,"uid":"abc123","url":"/d/abc123/slug","status":"success","version":8,"slug":"slug"}`))

	server := httptest.NewServer(mux)
	defer server.Close()

	tool, err := NewDashboardWriteTool(context.Background(), Configs{"test": {URL: server.URL}})
	require.NoError(t, err)

	// overwrite=true: no version resolution, model sent verbatim.
	_, err = tool.Invoke(context.Background(), &DashboardWriteParams{
		Instance:  "test",
		Operation: "update",
		Dashboard: `{"uid":"abc123","title":"T","version":99,"id":999}`,
		Overwrite: true,
		Confirmed: true,
	})
	require.NoError(t, err)

	assert.False(t, getCalled, "GET /api/dashboards/uid/:uid must NOT be called when overwrite=true")

	var req saveDashboardRequest
	require.NoError(t, json.Unmarshal(gotPOST, &req))
	assert.True(t, req.Overwrite, "overwrite must be true in request")
	v, _ := req.Dashboard["version"].(float64)
	assert.Equal(t, float64(99), v, "caller-supplied version must pass through untouched")
	id, _ := req.Dashboard["id"].(float64)
	assert.Equal(t, float64(999), id, "caller-supplied id must pass through untouched")
}
```

### 3.6 Test 5: Unknown uid (dashboard does not exist)

```go
func TestDashboardWriteToolUnknownUID(t *testing.T) {
	var gotPOST []byte

	mux := http.NewServeMux()
	mux.HandleFunc("/api/dashboards/uid/notfound", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Dashboard not found"}`))
	})
	mux.HandleFunc("/api/dashboards/db", captureBody(&gotPOST, http.StatusOK,
		`{"id":11,"uid":"notfound","url":"/d/notfound/slug","status":"success","version":1,"slug":"slug"}`))

	server := httptest.NewServer(mux)
	defer server.Close()

	tool, err := NewDashboardWriteTool(context.Background(), Configs{"test": {URL: server.URL}})
	require.NoError(t, err)

	// Model for a uid that doesn't exist — should proceed as create.
	_, err = tool.Invoke(context.Background(), &DashboardWriteParams{
		Instance:  "test",
		Operation: "create",
		Dashboard: `{"uid":"notfound","title":"New Dashboard","version":5,"id":99}`,
		Confirmed: true,
	})
	require.NoError(t, err)

	var req saveDashboardRequest
	require.NoError(t, json.Unmarshal(gotPOST, &req))
	_, hasVersion := req.Dashboard["version"]
	assert.False(t, hasVersion, "version must be stripped when dashboard does not exist")
	_, hasID := req.Dashboard["id"]
	assert.False(t, hasID, "id must be stripped when dashboard does not exist")
	assert.Equal(t, "New Dashboard", req.Dashboard["title"])
}
```

### 3.7 Test 6: Genuine conflict (race condition)

```go
func TestDashboardWriteToolGenuineConflict(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/dashboards/uid/abc123", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"dashboard":{"uid":"abc123","title":"T"},"meta":{"version":7}}`))
	})
	mux.HandleFunc("/api/dashboards/db", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPreconditionFailed)
		_, _ = w.Write([]byte(`{"message":"The dashboard has been changed by someone else","status":"version-mismatch"}`))
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	tool, err := NewDashboardWriteTool(context.Background(), Configs{"test": {URL: server.URL}})
	require.NoError(t, err)

	_, err = tool.Invoke(context.Background(), &DashboardWriteParams{
		Instance:  "test",
		Operation: "update",
		Dashboard: `{"uid":"abc123","title":"T"}`,
		Confirmed: true,
	})

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrVersionMismatch), "error must wrap ErrVersionMismatch")
	errStr := err.Error()
	assert.Contains(t, errStr, "abc123", "error must name the dashboard uid")
	assert.Contains(t, errStr, "version 7", "error must mention the submitted version (7)")
	assert.Contains(t, errStr, "modified concurrently", "error must explain the cause")
}
```

### 3.8 Test 7: Dry-run performs no version read and uses correct key

```go
func TestDashboardWriteToolDryRunNoVersionRead(t *testing.T) {
	getCalled := false

	mux := http.NewServeMux()
	mux.HandleFunc("/api/dashboards/uid/abc123", func(w http.ResponseWriter, r *http.Request) {
		getCalled = true
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"dashboard":{"uid":"abc123","title":"T"},"meta":{"version":7}}`))
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	tool, err := NewDashboardWriteTool(context.Background(), Configs{"test": {URL: server.URL}})
	require.NoError(t, err)

	result, err := tool.Invoke(context.Background(), &DashboardWriteParams{
		Instance:  "test",
		Operation: "update",
		Dashboard: `{"uid":"abc123","title":"T"}`,
		DryRun:    true,
	})
	require.NoError(t, err)

	// The protection check (checkProtected) may call GET, but the version
	// resolution MUST NOT. Since checkProtected already calls fetchDashboard
	// for blocklist evaluation, we can only assert the dry-run preview
	// structure. The key assertion is that the preview uses "folderUid" not
	// "folderUID", and that versionResolvedAtExecute is present.

	assert.Contains(t, result, `"dryRun":true`)
	assert.Contains(t, result, `"operation":"update"`)
	assert.Contains(t, result, `"folderUid":`)
	assert.NotContains(t, result, `"folderUID":`)
	assert.Contains(t, result, `"versionResolvedAtExecute":true`)
}
```

**Note on Test 7**: The `checkProtected` call (which happens before the dry-run check) already fetches the dashboard via `fetchDashboard` → `GetDashboard` for blocklist evaluation. This means `getCalled` will be `true` even for dry-run. The test primarily validates the preview structure keys and the presence of `versionResolvedAtExecute`. The version-resolution-specific GET (the one added by this fix) is not called during dry-run because it's placed after the dry-run return.

### 3.9 Test 8: Regression — reported incident

```go
func TestDashboardWriteToolRegressionReportedIncident(t *testing.T) {
	// Reproduce the exact scenario from the reported bug:
	// Model with id:1471, no version, overwrite unset, operation=update.
	// The save must succeed (no 412).

	var gotPOST []byte

	mux := http.NewServeMux()
	mux.HandleFunc("/api/dashboards/uid/3ce913db-abcd-1234-5678-abcdef123456", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"dashboard":{"uid":"3ce913db-abcd-1234-5678-abcdef123456","title":"My Dashboard","id":1471},"meta":{"version":6}}`))
	})
	mux.HandleFunc("/api/dashboards/db", captureBody(&gotPOST, http.StatusOK,
		`{"id":1471,"uid":"3ce913db-abcd-1234-5678-abcdef123456","url":"/d/3ce913db-abcd-1234-5678-abcdef123456/slug","status":"success","version":7,"slug":"slug"}`))

	server := httptest.NewServer(mux)
	defer server.Close()

	tool, err := NewDashboardWriteTool(context.Background(), Configs{"test": {URL: server.URL}})
	require.NoError(t, err)

	result, err := tool.Invoke(context.Background(), &DashboardWriteParams{
		Instance:  "test",
		Operation: "update",
		Dashboard: `{"uid":"3ce913db-abcd-1234-5678-abcdef123456","title":"My Dashboard","id":1471}`,
		Confirmed: true,
	})
	require.NoError(t, err)

	assert.Contains(t, result, `"status":"success"`)

	var req saveDashboardRequest
	require.NoError(t, json.Unmarshal(gotPOST, &req))
	v, _ := req.Dashboard["version"].(float64)
	assert.Equal(t, float64(6), v, "version must be 6 (injected from GET)")
	_, hasID := req.Dashboard["id"]
	assert.False(t, hasID, "stale id 1471 must be stripped")
}
```

---

## 4. Import Changes

### `client.go`

No new imports needed. `"fmt"` is already imported. `"emperror.dev/errors"` and `"github.com/goccy/go-json"` are already imported. `"net/http"` is already imported (needed for `http.StatusPreconditionFailed` and `http.StatusNotFound`).

### `dashboard_write_test.go`

The existing test file already imports `"io"`, `"net/http"`, `"net/http/httptest"`, `"testing"`, `"github.com/goccy/go-json"`, `"github.com/stretchr/testify/assert"`, `"github.com/stretchr/testify/require"`. Add:

```go
	"emperror.dev/errors"
```

(Needed for `errors.Is` in Test 6.)

---

## 5. Edge Cases and Error Handling Summary

| Scenario | Behavior |
|----------|----------|
| Dashboard exists, `overwrite=false` | GET version → inject into model → strip `id` → save |
| Dashboard exists, `overwrite=true` | No GET, model sent verbatim (including caller's `version`/`id`) |
| Dashboard deleted between protection check and save | GET returns 404 → strip `version` and `id` → save as create |
| Dashboard modified between version read and save | POST returns 412 → return `ErrVersionMismatch` with actionable message |
| `uid` is empty (fresh create, no uid) | `uid != ""` is false → skip version resolution entirely |
| `operation=create` with existing uid | Same as update — `uid != ""` triggers resolution (Grafana upserts) |
| `operation=delete` | No changes; delete path is unaffected |
| Dry-run | No version GET beyond protection check; preview uses `folderUid` key; includes `versionResolvedAtExecute` indicator |
| Protected dashboard | Blocklist check runs first; 412 is never reached for protected dashboards |

---

## 6. Validation Plan

1. **Unit tests**: Run `go test ./components/tool/grafana/... -run "VersionInjection|StaleVersion|StaleID|OverwritePassthrough|UnknownUID|GenuineConflict|DryRunNoVersionRead|RegressionReported" -v`
2. **Full test suite**: Run `go test ./components/tool/grafana/...` to ensure no regressions in existing tests.
3. **Manual acceptance** (out of scope for this plan but noted): Test against a real Grafana instance with `operation=update`, no `version`/`id`, no `overwrite`, repeated saves — all must succeed.

---

## 7. Out of Scope

- Retry logic on genuine `ErrVersionMismatch` (explicitly excluded per design decision).
- Changes to the consumer's dry-run/approve replay semantics.
- Changes to `grafana_dashboard_validate`, `grafana_query`, `grafana_dashboard`, or `grafana_datasource`.
- Changes to `prompts/` files (no dashboard-write-specific prompt files exist; the only prompt mentioning dashboard_write is `dashboard_validate_output_guidance.md` which only references it in a workflow recommendation and needs no changes).

---

## 8. Implementation Order

1. Add `ErrVersionMismatch` sentinel and `dashboardVersion()` helper to `client.go`
2. Wrap 412 in `SaveDashboard()` in `client.go`
3. Add version resolution block after dry-run check in `dashboard_write.go`
4. Add actionable conflict error wrapping in `dashboard_write.go`
5. Fix dry-run preview key and add `versionResolvedAtExecute` indicator in `dashboard_write.go`
6. Update `dashboardWriteDescription` constant in `dashboard_write.go`
7. Update `Overwrite` and `Dashboard` jsonschema tags in `dashboard_write.go`
8. Add `captureBody` helper and 8 test cases to `dashboard_write_test.go`
9. Run full test suite and fix any issues
