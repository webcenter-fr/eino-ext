package grafana

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"emperror.dev/errors"
	"github.com/goccy/go-json"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/safety"
)

// authorizedCtx marks grafana_dashboard_write as execution-authorized so
// confirmed:true invocations pass the per-tool authorization layer.
var authorizedCtx = safety.WithExecutionAuthorized(context.Background(), "grafana_dashboard_write")

func newDashboardWriteTestTool(t *testing.T, cfg Config, handler http.HandlerFunc) *DashboardWriteTool {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	cfg.URL = server.URL
	tool, err := NewDashboardWriteTool(context.Background(), Configs{"test": cfg})
	require.NoError(t, err)
	return tool
}

func newDashboardWriteTool(t *testing.T, handler http.HandlerFunc) *DashboardWriteTool {
	t.Helper()
	return newDashboardWriteTestTool(t, Config{}, handler)
}

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

// newDashboardWriteToolWithMux creates a DashboardWriteTool that routes to
// handlers registered on the provided mux. Used for tests that need multiple
// endpoints (e.g. GET dashboard + POST save).
func newDashboardWriteToolWithMux(t *testing.T, mux *http.ServeMux) *DashboardWriteTool {
	t.Helper()
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	tool, err := NewDashboardWriteTool(context.Background(), Configs{"test": {URL: server.URL}})
	require.NoError(t, err)
	return tool
}

// dashboardGetHandler returns a handler for GET /api/dashboards/uid/:uid that
// returns a dashboard response with the given uid, title, and version.
func dashboardGetHandler(uid, title string, version int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fmt.Sprintf(
			`{"dashboard":{"uid":%q,"title":%q},"meta":{"version":%d}}`,
			uid, title, version)))
	}
}

// dashboardGetNotFoundHandler returns a handler that returns HTTP 404.
func dashboardGetNotFoundHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Dashboard not found"}`))
	}
}

// dashboardSaveHandler returns a handler for POST /api/dashboards/db that
// captures the request body (if captured is non-nil) and returns a success
// response with the given uid and version.
func dashboardSaveHandler(captured *[]byte, uid string, version int) http.HandlerFunc {
	return captureBody(captured, http.StatusOK, fmt.Sprintf(
		`{"id":10,"uid":%q,"url":"/d/%s/slug","status":"success","version":%d,"slug":"slug"}`,
		uid, uid, version))
}

// dashboardSaveConflictHandler returns a handler that returns HTTP 412
// (PreconditionFailed) with a version-mismatch status.
func dashboardSaveConflictHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPreconditionFailed)
		_, _ = w.Write([]byte(`{"message":"The dashboard has been changed by someone else","status":"version-mismatch"}`))
	}
}

func TestNewDashboardWriteToolRequiresConfigs(t *testing.T) {
	_, err := NewDashboardWriteTool(context.Background(), Configs{})
	assert.Error(t, err)
}

// TestDashboardWriteUnauthorizedContext asserts the per-tool second layer:
// calling the tool directly (no middleware) with confirmed:true and an
// unauthorized context is refused with ErrExecutionNotAuthorized. The
// authorization check fires before any HTTP request, so the handler is not hit.
func TestDashboardWriteUnauthorizedContext(t *testing.T) {
	handlerHit := false
	tool := newDashboardWriteTool(t, func(w http.ResponseWriter, r *http.Request) {
		handlerHit = true
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":10,"uid":"new-uid","url":"/d/new-uid/slug","status":"success","version":1,"slug":"slug"}`))
	})

	_, err := tool.Invoke(context.Background(), &DashboardWriteParams{
		Instance:  "test",
		Operation: "create",
		Dashboard: `{"title":"My Dashboard"}`,
		Confirmed: true,
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, safety.ErrExecutionNotAuthorized), "expected ErrExecutionNotAuthorized, got %v", err)
	assert.False(t, handlerHit, "handler must not be reached when execution is not authorized")
}

// TestDryRunNoMutation asserts the WriteToolNames contract: invoking the write
// tool with dryRun:true returns a preview and issues no mutating request.
func TestDryRunNoMutation(t *testing.T) {
	mutating := []string{}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/dashboards/db", func(w http.ResponseWriter, r *http.Request) {
		mutating = append(mutating, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":10,"uid":"abc123","url":"/d/abc123/slug","status":"success","version":2,"slug":"slug"}`))
	})
	mux.HandleFunc("/api/dashboards/uid/abc123", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			mutating = append(mutating, r.Method+" "+r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"dashboard":{"uid":"abc123","title":"Production Overview"},"meta":{"version":1}}`))
	})

	tool := newDashboardWriteToolWithMux(t, mux)

	t.Run("create dry-run", func(t *testing.T) {
		result, err := tool.Invoke(context.Background(), &DashboardWriteParams{
			Instance:  "test",
			Operation: "create",
			Dashboard: `{"title":"My Dashboard"}`,
			DryRun:    true,
		})
		require.NoError(t, err)
		assert.Contains(t, result, `"dryRun":true`)
	})

	t.Run("update dry-run", func(t *testing.T) {
		result, err := tool.Invoke(context.Background(), &DashboardWriteParams{
			Instance:  "test",
			Operation: "update",
			UID:       "abc123",
			Dashboard: `{"uid":"abc123","title":"My Dashboard"}`,
			DryRun:    true,
		})
		require.NoError(t, err)
		assert.Contains(t, result, `"dryRun":true`)
	})

	t.Run("delete dry-run", func(t *testing.T) {
		result, err := tool.Invoke(context.Background(), &DashboardWriteParams{
			Instance:  "test",
			Operation: "delete",
			UID:       "abc123",
			DryRun:    true,
		})
		require.NoError(t, err)
		assert.Contains(t, result, `"dryRun":true`)
	})

	assert.Empty(t, mutating, "dry-run must issue no POST /api/dashboards/db and no DELETE request, got %v", mutating)
}

func TestDashboardWriteToolCreate(t *testing.T) {
	tool := newDashboardWriteTool(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/dashboards/db", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":10,"uid":"new-uid","url":"/d/new-uid/slug","status":"success","version":1,"slug":"slug"}`))
	})

	t.Run("create confirmed", func(t *testing.T) {
		result, err := tool.Invoke(authorizedCtx, &DashboardWriteParams{
			Instance:  "test",
			Operation: "create",
			Dashboard: `{"title":"My Dashboard"}`,
			Confirmed: true,
		})
		require.NoError(t, err)

		var out DashboardSaveOutput
		require.NoError(t, json.Unmarshal([]byte(result), &out))
		assert.Equal(t, "new-uid", out.UID)
		assert.Equal(t, "success", out.Status)
	})

	t.Run("create dry run", func(t *testing.T) {
		result, err := tool.Invoke(context.Background(), &DashboardWriteParams{
			Instance:  "test",
			Operation: "create",
			Dashboard: `{"title":"My Dashboard"}`,
			DryRun:    true,
		})
		require.NoError(t, err)
		assert.Contains(t, result, `"dryRun":true`)
		assert.Contains(t, result, `"operation":"create"`)
	})

	t.Run("missing title", func(t *testing.T) {
		_, err := tool.Invoke(authorizedCtx, &DashboardWriteParams{
			Instance:  "test",
			Operation: "create",
			Dashboard: `{}`,
			Confirmed: true,
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "missing a 'title'")
	})

	t.Run("missing dashboard", func(t *testing.T) {
		_, err := tool.Invoke(authorizedCtx, &DashboardWriteParams{
			Instance:  "test",
			Operation: "create",
			Confirmed: true,
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "dashboard' is required")
	})

	t.Run("invalid json", func(t *testing.T) {
		_, err := tool.Invoke(authorizedCtx, &DashboardWriteParams{
			Instance:  "test",
			Operation: "create",
			Dashboard: "not json",
			Confirmed: true,
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not valid JSON")
	})

	t.Run("unknown instance", func(t *testing.T) {
		_, err := tool.Invoke(authorizedCtx, &DashboardWriteParams{
			Instance:  "invalid",
			Operation: "create",
			Dashboard: `{"title":"X"}`,
			Confirmed: true,
		})
		assert.Error(t, err)
	})

	t.Run("invalid operation", func(t *testing.T) {
		_, err := tool.Invoke(authorizedCtx, &DashboardWriteParams{
			Instance:  "test",
			Operation: "bogus",
			Dashboard: `{"title":"X"}`,
			Confirmed: true,
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid parameters")
	})
}

func TestDashboardWriteToolUpdate(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/dashboards/uid/abc123", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"dashboard":{"uid":"abc123","title":"Production Overview","tags":["prod"]},"meta":{"folderUid":"folder-1","version":3}}`))
	})
	mux.HandleFunc("/api/dashboards/db", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":10,"uid":"abc123","url":"/d/abc123/slug","status":"success","version":4,"slug":"slug"}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	tool, err := NewDashboardWriteTool(context.Background(), Configs{"test": {URL: server.URL}})
	require.NoError(t, err)

	t.Run("update existing", func(t *testing.T) {
		result, err := tool.Invoke(authorizedCtx, &DashboardWriteParams{
			Instance:  "test",
			Operation: "update",
			Dashboard: `{"uid":"abc123","title":"Production Overview"}`,
			Overwrite: true,
			Confirmed: true,
		})
		require.NoError(t, err)
		assert.Contains(t, result, `"uid":"abc123"`)
		assert.Contains(t, result, `"status":"success"`)
	})

	t.Run("no confirmation", func(t *testing.T) {
		_, err := tool.Invoke(context.Background(), &DashboardWriteParams{
			Instance:  "test",
			Operation: "update",
			Dashboard: `{"uid":"abc123","title":"Production Overview"}`,
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "confirmed must be true to execute")
	})

	t.Run("update by params.UID targets that UID", func(t *testing.T) {
		var gotBody []byte
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet {
				// protection-check fetch for abc123
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"dashboard":{"uid":"abc123","title":"Production Overview"},"meta":{}}`))
				return
			}
			gotBody, _ = io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":10,"uid":"abc123","url":"/d/abc123/slug","status":"success","version":4,"slug":"slug"}`))
		}))
		defer server.Close()

		updateTool, err := NewDashboardWriteTool(context.Background(), Configs{"test": {URL: server.URL}})
		require.NoError(t, err)

		_, err = updateTool.Invoke(authorizedCtx, &DashboardWriteParams{
			Instance:  "test",
			Operation: "update",
			UID:       "abc123",
			Dashboard: `{"title":"Renamed via UID"}`,
			Confirmed: true,
		})
		require.NoError(t, err)

		var req saveDashboardRequest
		require.NoError(t, json.Unmarshal(gotBody, &req))
		uid, _ := req.Dashboard["uid"].(string)
		assert.Equal(t, "abc123", uid, "params.UID must be injected into the dashboard model so the update targets the right dashboard")
	})
}

func TestDashboardWriteToolDelete(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/dashboards/uid/abc123", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"title":"Production Overview","message":"Dashboard deleted","id":1}`))
			return
		}
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"dashboard":{"uid":"abc123","title":"Production Overview","tags":["prod"]},"meta":{"folderUid":"folder-1","version":3}}`))
	})
	mux.HandleFunc("/api/dashboards/uid/protected-uid", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"dashboard":{"uid":"protected-uid","title":"Kubernetes Monitoring","tags":["infrastructure"]},"meta":{"folderUid":"infra-folder","version":1}}`))
	})
	mux.HandleFunc("/api/dashboards/uid/nonexistent", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Dashboard not found"}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	tool, err := NewDashboardWriteTool(context.Background(), Configs{
		"test": {
			URL: server.URL,
			ProtectedDashboards: ProtectedDashboardsConfig{
				UIDs: []string{"protected-uid"},
			},
		},
	})
	require.NoError(t, err)

	t.Run("delete dry run", func(t *testing.T) {
		result, err := tool.Invoke(context.Background(), &DashboardWriteParams{
			Instance:  "test",
			Operation: "delete",
			UID:       "abc123",
			DryRun:    true,
		})
		require.NoError(t, err)
		assert.Contains(t, result, `"dryRun":true`)
		assert.Contains(t, result, `"delete"`)
	})

	t.Run("delete confirmed", func(t *testing.T) {
		result, err := tool.Invoke(authorizedCtx, &DashboardWriteParams{
			Instance:  "test",
			Operation: "delete",
			UID:       "abc123",
			Confirmed: true,
		})
		require.NoError(t, err)

		var out DashboardDeleteOutput
		require.NoError(t, json.Unmarshal([]byte(result), &out))
		assert.Equal(t, "abc123", out.UID)
		assert.Equal(t, "success", out.Status)
		assert.Equal(t, "Production Overview", out.Title)
	})

	t.Run("delete protected", func(t *testing.T) {
		_, err := tool.Invoke(authorizedCtx, &DashboardWriteParams{
			Instance:  "test",
			Operation: "delete",
			UID:       "protected-uid",
			Confirmed: true,
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "is protected")
	})

	t.Run("delete nonexistent", func(t *testing.T) {
		_, err := tool.Invoke(authorizedCtx, &DashboardWriteParams{
			Instance:  "test",
			Operation: "delete",
			UID:       "nonexistent",
			Confirmed: true,
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("delete missing uid", func(t *testing.T) {
		_, err := tool.Invoke(authorizedCtx, &DashboardWriteParams{
			Instance:  "test",
			Operation: "delete",
			Confirmed: true,
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "uid' is required for delete")
	})
}

func TestDashboardWriteToolProtection(t *testing.T) {
	t.Run("create blocked by protected title prefix", func(t *testing.T) {
		tool, err := NewDashboardWriteTool(context.Background(), Configs{
			"test": {
				URL: "http://localhost",
				ProtectedDashboards: ProtectedDashboardsConfig{
					TitlePrefixes: []string{"Kubernetes "},
				},
			},
		})
		require.NoError(t, err)

		_, err = tool.Invoke(authorizedCtx, &DashboardWriteParams{
			Instance:  "test",
			Operation: "create",
			Dashboard: `{"title":"Kubernetes Evil"}`,
			Confirmed: true,
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "protected blocklist")
	})
}

// TestDashboardWriteToolVersionResolution tests various version and ID handling
// scenarios when updating existing dashboards.
func TestDashboardWriteToolVersionResolution(t *testing.T) {
	t.Parallel()

	// assertPOSTVersionUnmarshaled is a test assertion helper that unmarshals
	// the captured POST body and asserts the dashboard version.
	assertPOSTVersion := func(t *testing.T, gotPOST []byte, wantVersion float64) {
		t.Helper()
		var req saveDashboardRequest
		require.NoError(t, json.Unmarshal(gotPOST, &req))
		v, _ := req.Dashboard["version"].(float64)
		assert.Equal(t, wantVersion, v)
	}

	// assertPOSTHasNoField is a test assertion helper that asserts a field is
	// NOT present in the dashboard of the captured POST body.
	assertPOSTHasNoField := func(t *testing.T, gotPOST []byte, field string) {
		t.Helper()
		var req saveDashboardRequest
		require.NoError(t, json.Unmarshal(gotPOST, &req))
		_, exists := req.Dashboard[field]
		assert.False(t, exists, "field %q must not be present", field)
	}

	t.Run("version is injected when model has no version", func(t *testing.T) {
		t.Parallel()
		var gotPOST []byte

		mux := http.NewServeMux()
		mux.HandleFunc("/api/dashboards/uid/abc123", dashboardGetHandler("abc123", "T", 7))
		mux.HandleFunc("/api/dashboards/db", dashboardSaveHandler(&gotPOST, "abc123", 8))

		tool := newDashboardWriteToolWithMux(t, mux)
		_, err := tool.Invoke(authorizedCtx, &DashboardWriteParams{
			Instance:  "test",
			Operation: "update",
			Dashboard: `{"uid":"abc123","title":"T"}`,
			Confirmed: true,
		})
		require.NoError(t, err)

		assertPOSTVersion(t, gotPOST, 7)
	})

	t.Run("stale version is replaced with current version", func(t *testing.T) {
		t.Parallel()
		var gotPOST []byte

		mux := http.NewServeMux()
		mux.HandleFunc("/api/dashboards/uid/abc123", dashboardGetHandler("abc123", "T", 7))
		mux.HandleFunc("/api/dashboards/db", dashboardSaveHandler(&gotPOST, "abc123", 8))

		tool := newDashboardWriteToolWithMux(t, mux)
		_, err := tool.Invoke(authorizedCtx, &DashboardWriteParams{
			Instance:  "test",
			Operation: "update",
			Dashboard: `{"uid":"abc123","title":"T","version":3}`,
			Confirmed: true,
		})
		require.NoError(t, err)

		assertPOSTVersion(t, gotPOST, 7)
	})

	t.Run("stale id is stripped from request", func(t *testing.T) {
		t.Parallel()
		var gotPOST []byte

		mux := http.NewServeMux()
		mux.HandleFunc("/api/dashboards/uid/abc123", dashboardGetHandler("abc123", "T", 5))
		mux.HandleFunc("/api/dashboards/db", dashboardSaveHandler(&gotPOST, "abc123", 6))

		tool := newDashboardWriteToolWithMux(t, mux)
		_, err := tool.Invoke(authorizedCtx, &DashboardWriteParams{
			Instance:  "test",
			Operation: "update",
			Dashboard: `{"uid":"abc123","title":"T","id":1471}`,
			Confirmed: true,
		})
		require.NoError(t, err)

		assertPOSTHasNoField(t, gotPOST, "id")
		assertPOSTVersion(t, gotPOST, 5)
	})
}

func TestDashboardWriteToolOverwritePassthrough(t *testing.T) {
	t.Parallel()
	var gotPOST []byte

	mux := http.NewServeMux()
	// Note: GET /api/dashboards/uid/abc123 is still called by checkProtected
	// before reaching the version-resolution code (which skips when overwrite=true)
	mux.HandleFunc("/api/dashboards/uid/abc123", dashboardGetHandler("abc123", "T", 7))
	mux.HandleFunc("/api/dashboards/db", dashboardSaveHandler(&gotPOST, "abc123", 8))

	tool := newDashboardWriteToolWithMux(t, mux)
	_, err := tool.Invoke(authorizedCtx, &DashboardWriteParams{
		Instance:  "test",
		Operation: "update",
		Dashboard: `{"uid":"abc123","title":"T","version":99,"id":999}`,
		Overwrite: true,
		Confirmed: true,
	})
	require.NoError(t, err)

	var req saveDashboardRequest
	require.NoError(t, json.Unmarshal(gotPOST, &req))
	assert.True(t, req.Overwrite, "overwrite must be true in request")
	v, _ := req.Dashboard["version"].(float64)
	assert.Equal(t, float64(99), v, "caller-supplied version must pass through untouched when overwrite=true")
	_, hasID := req.Dashboard["id"]
	assert.False(t, hasID, "id must be stripped even when overwrite=true")
}

func TestDashboardWriteToolUnknownUID(t *testing.T) {
	t.Parallel()
	var gotPOST []byte

	mux := http.NewServeMux()
	mux.HandleFunc("/api/dashboards/uid/notfound", dashboardGetNotFoundHandler())
	mux.HandleFunc("/api/dashboards/db", dashboardSaveHandler(&gotPOST, "notfound", 1))

	tool := newDashboardWriteToolWithMux(t, mux)
	_, err := tool.Invoke(authorizedCtx, &DashboardWriteParams{
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

func TestDashboardWriteToolGenuineConflict(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/dashboards/uid/abc123", dashboardGetHandler("abc123", "T", 7))
	mux.HandleFunc("/api/dashboards/db", dashboardSaveConflictHandler())

	tool := newDashboardWriteToolWithMux(t, mux)
	_, err := tool.Invoke(authorizedCtx, &DashboardWriteParams{
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

func TestDashboardWriteToolDryRunNoVersionRead(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	// GET is called by checkProtected, but not for version resolution
	mux.HandleFunc("/api/dashboards/uid/abc123", dashboardGetHandler("abc123", "T", 7))

	tool := newDashboardWriteToolWithMux(t, mux)
	result, err := tool.Invoke(context.Background(), &DashboardWriteParams{
		Instance:  "test",
		Operation: "update",
		Dashboard: `{"uid":"abc123","title":"T"}`,
		DryRun:    true,
	})
	require.NoError(t, err)

	assert.Contains(t, result, `"dryRun":true`)
	assert.Contains(t, result, `"operation":"update"`)
	assert.Contains(t, result, `"folderUid":`)
	assert.NotContains(t, result, `"folderUID":`)
	assert.Contains(t, result, `"versionResolvedAtExecute":true`)
}

// TestDashboardWriteToolRegressionReportedIncident reproduces the exact scenario
// from the reported bug: Model with id:1471, no version, overwrite unset.
// The save must succeed (no 412 version mismatch).
func TestDashboardWriteToolRegressionReportedIncident(t *testing.T) {
	t.Parallel()
	var gotPOST []byte

	const uid = "3ce913db-abcd-1234-5678-abcdef123456"

	mux := http.NewServeMux()
	// GET returns the existing dashboard with version 6 and id 1471
	mux.HandleFunc("/api/dashboards/uid/"+uid, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fmt.Sprintf(
			`{"dashboard":{"uid":%q,"title":"My Dashboard","id":1471},"meta":{"version":6}}`, uid)))
	})
	// POST succeeds with new version 7
	mux.HandleFunc("/api/dashboards/db", captureBody(&gotPOST, http.StatusOK, fmt.Sprintf(
		`{"id":1471,"uid":%q,"url":"/d/%s/slug","status":"success","version":7,"slug":"slug"}`, uid, uid)))

	tool := newDashboardWriteToolWithMux(t, mux)
	result, err := tool.Invoke(authorizedCtx, &DashboardWriteParams{
		Instance:  "test",
		Operation: "update",
		Dashboard: fmt.Sprintf(`{"uid":%q,"title":"My Dashboard","id":1471}`, uid),
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

func TestDashboardWriteToolConstructor(t *testing.T) {
	tool, err := NewDashboardWriteTool(context.Background(), Configs{"t": {URL: "http://localhost"}})
	require.NoError(t, err)
	require.NotNil(t, tool)

	info, err := tool.Info(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "grafana_dashboard_write", info.Name)
}

// TestDashboardWriteToolUIDValidation verifies that invalid UIDs are rejected
// before any HTTP request is made (CWE-20, CWE-22, CWE-400).
func TestDashboardWriteToolUIDValidation(t *testing.T) {
	tool, err := NewDashboardWriteTool(context.Background(), Configs{"t": {URL: "http://localhost"}})
	require.NoError(t, err)

	t.Run("path traversal uid rejected in delete", func(t *testing.T) {
		_, err := tool.Invoke(authorizedCtx, &DashboardWriteParams{
			Instance:  "t",
			Operation: "delete",
			UID:       "../../etc/passwd",
			Confirmed: true,
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid dashboard UID")
	})

	t.Run("path traversal uid rejected in update via params", func(t *testing.T) {
		_, err := tool.Invoke(authorizedCtx, &DashboardWriteParams{
			Instance:  "t",
			Operation: "update",
			UID:       "../../etc/passwd",
			Dashboard: `{"title":"Test"}`,
			Confirmed: true,
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid dashboard UID")
	})

	t.Run("path traversal uid rejected in update via model", func(t *testing.T) {
		_, err := tool.Invoke(authorizedCtx, &DashboardWriteParams{
			Instance:  "t",
			Operation: "update",
			Dashboard: `{"uid":"../../etc/passwd","title":"Test"}`,
			Confirmed: true,
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid dashboard UID")
	})

	t.Run("overly long uid rejected (41 chars)", func(t *testing.T) {
		uid := strings.Repeat("a", 41)
		_, err := tool.Invoke(authorizedCtx, &DashboardWriteParams{
			Instance:  "t",
			Operation: "delete",
			UID:       uid,
			Confirmed: true,
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid dashboard UID")
	})

	t.Run("uid with invalid characters rejected", func(t *testing.T) {
		_, err := tool.Invoke(authorizedCtx, &DashboardWriteParams{
			Instance:  "t",
			Operation: "delete",
			UID:       "uid with spaces",
			Confirmed: true,
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid dashboard UID")
	})

	t.Run("valid uid formats accepted", func(t *testing.T) {
		validUIDs := []string{
			"abc123",
			"my-dashboard_UID-123",
			strings.Repeat("a", 40), // max length
		}
		for _, uid := range validUIDs {
			// Just validate that we get past the UID validation and either succeed
			// or fail for a different reason (like network)
			_, err := tool.Invoke(authorizedCtx, &DashboardWriteParams{
				Instance:  "t",
				Operation: "delete",
				UID:       uid,
				Confirmed: true,
			})
			// Error should NOT be "invalid dashboard UID"
			if err != nil {
				assert.NotContains(t, err.Error(), "invalid dashboard UID", "uid %q should be valid", uid)
			}
		}
	})

	t.Run("empty uid is valid (for create)", func(t *testing.T) {
		// Create with no UID should be fine (no HTTP call needed for validation)
		// But will fail because no confirmation/dryrun - but not because of UID
		_, err := tool.Invoke(context.Background(), &DashboardWriteParams{
			Instance:  "t",
			Operation: "create",
			Dashboard: `{"title":"New Dashboard"}`,
		})
		if err != nil {
			assert.NotContains(t, err.Error(), "invalid dashboard UID")
		}
	})
}

// TestDashboardWriteToolInputLengthLimits verifies that oversized inputs are
// rejected by validate.Struct before any HTTP request is made (CWE-400).
func TestDashboardWriteToolInputLengthLimits(t *testing.T) {
	tool, err := NewDashboardWriteTool(context.Background(), Configs{"t": {URL: "http://localhost"}})
	require.NoError(t, err)

	t.Run("oversized dashboard JSON rejected", func(t *testing.T) {
		huge := strings.Repeat("x", 1048577) // 1MB + 1
		_, err := tool.Invoke(authorizedCtx, &DashboardWriteParams{
			Instance:  "t",
			Operation: "create",
			Dashboard: `{"title":"x","fill":"` + huge + `"}`,
			Confirmed: true,
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid parameters")
	})

	t.Run("oversized uid rejected", func(t *testing.T) {
		_, err := tool.Invoke(authorizedCtx, &DashboardWriteParams{
			Instance:  "t",
			Operation: "delete",
			UID:       strings.Repeat("x", 257),
			Confirmed: true,
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid parameters")
	})

	t.Run("oversized message rejected", func(t *testing.T) {
		_, err := tool.Invoke(authorizedCtx, &DashboardWriteParams{
			Instance:  "t",
			Operation: "create",
			Dashboard: `{"title":"x"}`,
			Message:   strings.Repeat("x", 1025),
			Confirmed: true,
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid parameters")
	})
}

func TestDashboardWriteToolChangesAutoFetch(t *testing.T) {
	var gotPOST []byte

	mux := http.NewServeMux()
	mux.HandleFunc("/api/dashboards/uid/abc123", dashboardGetHandler("abc123", "Production Overview", 3))
	mux.HandleFunc("/api/dashboards/db", captureBody(&gotPOST, http.StatusOK,
		`{"id":10,"uid":"abc123","url":"/d/abc123/slug","status":"success","version":4,"slug":"slug"}`))

	tool := newDashboardWriteToolWithMux(t, mux)

	t.Run("auto-fetch and merge changes", func(t *testing.T) {
		result, err := tool.Invoke(authorizedCtx, &DashboardWriteParams{
			Instance:  "test",
			Operation: "update",
			UID:       "abc123",
			Changes:   `{"templating":{"list":[{"current":{"value":"new-cluster"}}]}}`,
			Confirmed: true,
		})
		require.NoError(t, err)
		assert.Contains(t, result, `"uid":"abc123"`)
		assert.Contains(t, result, `"status":"success"`)

		var req saveDashboardRequest
		require.NoError(t, json.Unmarshal(gotPOST, &req))
		assert.Equal(t, "Production Overview", req.Dashboard["title"],
			"title must come from auto-fetched dashboard")
		assert.Equal(t, float64(3), req.Dashboard["version"],
			"version must be injected from fetched dashboard")
		assert.NotContains(t, string(gotPOST), `"id":`)
	})
}

func TestDashboardWriteToolChangesWithDashboard(t *testing.T) {
	var gotPOST []byte

	mux := http.NewServeMux()
	mux.HandleFunc("/api/dashboards/uid/abc123", dashboardGetHandler("abc123", "Existing Title", 5))
	mux.HandleFunc("/api/dashboards/db", captureBody(&gotPOST, http.StatusOK,
		`{"id":10,"uid":"abc123","url":"/d/abc123/slug","status":"success","version":6,"slug":"slug"}`))

	tool := newDashboardWriteToolWithMux(t, mux)

	t.Run("changes merged into provided dashboard", func(t *testing.T) {
		result, err := tool.Invoke(authorizedCtx, &DashboardWriteParams{
			Instance:  "test",
			Operation: "update",
			Dashboard: `{"uid":"abc123","title":"Custom Title","panels":[{"id":1}]}`,
			Changes:   `{"tags":["env:prod"],"panels":[{"id":2,"title":"New Panel"}]}`,
			Confirmed: true,
		})
		require.NoError(t, err)
		assert.Contains(t, result, `"status":"success"`)

		var req saveDashboardRequest
		require.NoError(t, json.Unmarshal(gotPOST, &req))
		assert.Equal(t, "Custom Title", req.Dashboard["title"],
			"title from dashboard must be preserved")
		assert.Contains(t, string(gotPOST), `"env:prod"`,
			"tags from changes must be merged")
		panels, _ := req.Dashboard["panels"].([]any)
		assert.Len(t, panels, 1,
			"panels from changes must replace panels from dashboard (array replacement)")
	})
}

func TestDashboardWriteToolChangesErrors(t *testing.T) {
	tool, err := NewDashboardWriteTool(context.Background(), Configs{"t": {URL: "http://localhost"}})
	require.NoError(t, err)

	t.Run("changes without dashboard and without uid", func(t *testing.T) {
		_, err := tool.Invoke(authorizedCtx, &DashboardWriteParams{
			Instance:  "t",
			Operation: "update",
			Changes:   `{"title":"Test"}`,
			Confirmed: true,
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "uid' is required when 'changes' is provided without 'dashboard'")
	})

	t.Run("changes on create rejected", func(t *testing.T) {
		_, err := tool.Invoke(authorizedCtx, &DashboardWriteParams{
			Instance:  "t",
			Operation: "create",
			Dashboard: `{"title":"Test"}`,
			Changes:   `{"tags":["prod"]}`,
			Confirmed: true,
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "changes' is not supported for create")
	})

	t.Run("invalid changes JSON", func(t *testing.T) {
		_, err := tool.Invoke(authorizedCtx, &DashboardWriteParams{
			Instance:  "t",
			Operation: "update",
			UID:       "abc123",
			Changes:   "not json",
			Confirmed: true,
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not valid JSON")
	})
}

func TestDashboardWriteToolChangesAutoFetchNotFound(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/dashboards/uid/nonexistent", dashboardGetNotFoundHandler())

	tool := newDashboardWriteToolWithMux(t, mux)

	t.Run("changes on nonexistent dashboard", func(t *testing.T) {
		_, err := tool.Invoke(authorizedCtx, &DashboardWriteParams{
			Instance:  "test",
			Operation: "update",
			UID:       "nonexistent",
			Changes:   `{"title":"New Title"}`,
			Confirmed: true,
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestDashboardWriteToolChangesProtected(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/dashboards/uid/protected-uid", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"dashboard":{"uid":"protected-uid","title":"Kubernetes Monitoring","tags":["infrastructure"]},"meta":{"folderUid":"infra-folder","version":1}}`))
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	tool, err := NewDashboardWriteTool(context.Background(), Configs{
		"test": {
			URL: server.URL,
			ProtectedDashboards: ProtectedDashboardsConfig{
				UIDs: []string{"protected-uid"},
			},
		},
	})
	require.NoError(t, err)

	t.Run("changes on protected dashboard blocked", func(t *testing.T) {
		_, err := tool.Invoke(authorizedCtx, &DashboardWriteParams{
			Instance:  "test",
			Operation: "update",
			UID:       "protected-uid",
			Changes:   `{"templating":{"list":[{"current":{"value":"x"}}]}}`,
			Confirmed: true,
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "is protected")
	})
}

func TestDashboardWriteToolChangesDryRun(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/dashboards/uid/abc123", dashboardGetHandler("abc123", "Dry Run Dashboard", 2))

	tool := newDashboardWriteToolWithMux(t, mux)

	t.Run("dry run with changes shows merged preview", func(t *testing.T) {
		result, err := tool.Invoke(context.Background(), &DashboardWriteParams{
			Instance:  "test",
			Operation: "update",
			UID:       "abc123",
			Changes:   `{"tags":["env:staging"]}`,
			DryRun:    true,
		})
		require.NoError(t, err)
		assert.Contains(t, result, `"dryRun":true`)
		assert.Contains(t, result, `"operation":"update"`)
		assert.Contains(t, result, `"Dry Run Dashboard"`,
			"title from fetched dashboard must be in preview")
		assert.Contains(t, result, `"env:staging"`,
			"changes must be reflected in preview")
		assert.Contains(t, result, `"versionResolvedAtExecute":true`)
	})
}

func TestDashboardWriteToolChangesDeepMerge(t *testing.T) {
	var gotPOST []byte

	mux := http.NewServeMux()
	mux.HandleFunc("/api/dashboards/uid/abc123", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"dashboard": {
				"uid": "abc123",
				"title": "Deep Merge Test",
				"templating": {
					"list": [
						{"name": "cluster", "current": {"value": "old-cluster"}, "includeAll": false},
						{"name": "namespace", "current": {"value": "default"}}
					]
				},
				"time": {"from": "now-6h", "to": "now"}
			},
			"meta": {"version": 1}
		}`))
	})
	mux.HandleFunc("/api/dashboards/db", captureBody(&gotPOST, http.StatusOK,
		`{"id":10,"uid":"abc123","url":"/d/abc123/slug","status":"success","version":2,"slug":"slug"}`))

	tool := newDashboardWriteToolWithMux(t, mux)

	t.Run("deep merge nested changes", func(t *testing.T) {
		_, err := tool.Invoke(authorizedCtx, &DashboardWriteParams{
			Instance:  "test",
			Operation: "update",
			UID:       "abc123",
			Changes: `{
				"templating": {
					"list": [
						{"name": "cluster", "current": {"value": "new-cluster"}, "includeAll": true, "allValue": ".+"}
					]
				},
				"time": {"from": "now-1h"}
			}`,
			Confirmed: true,
		})
		require.NoError(t, err)

		var req saveDashboardRequest
		require.NoError(t, json.Unmarshal(gotPOST, &req))

		d := req.Dashboard
		assert.Equal(t, "Deep Merge Test", d["title"])

		templating, _ := d["templating"].(map[string]any)
		list, _ := templating["list"].([]any)
		assert.Len(t, list, 1, "templating.list from changes replaces existing list (array replacement)")

		timeMap, _ := d["time"].(map[string]any)
		assert.Equal(t, "now-1h", timeMap["from"], "time.from from changes overrides existing")
		assert.Equal(t, "now", timeMap["to"], "time.to from existing dashboard is preserved (deep merge)")
	})
}

func TestDashboardWriteToolChangesTitleMerged(t *testing.T) {
	var gotPOST []byte

	mux := http.NewServeMux()
	mux.HandleFunc("/api/dashboards/uid/abc123", dashboardGetHandler("abc123", "Original Title", 1))
	mux.HandleFunc("/api/dashboards/db", captureBody(&gotPOST, http.StatusOK,
		`{"id":10,"uid":"abc123","url":"/d/abc123/slug","status":"success","version":2,"slug":"slug"}`))

	tool := newDashboardWriteToolWithMux(t, mux)

	t.Run("changes with title override", func(t *testing.T) {
		_, err := tool.Invoke(authorizedCtx, &DashboardWriteParams{
			Instance:  "test",
			Operation: "update",
			UID:       "abc123",
			Changes:   `{"title":"Updated Title"}`,
			Confirmed: true,
		})
		require.NoError(t, err)

		var req saveDashboardRequest
		require.NoError(t, json.Unmarshal(gotPOST, &req))
		assert.Equal(t, "Updated Title", req.Dashboard["title"])
	})
}
