package grafana

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/goccy/go-json"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

func TestNewDashboardWriteToolRequiresConfigs(t *testing.T) {
	_, err := NewDashboardWriteTool(context.Background(), Configs{})
	assert.Error(t, err)
}

func TestDashboardWriteToolCreate(t *testing.T) {
	tool := newDashboardWriteTool(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/dashboards/db", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":10,"uid":"new-uid","url":"/d/new-uid/slug","status":"success","version":1,"slug":"slug"}`))
	})

	t.Run("create confirmed", func(t *testing.T) {
		result, err := tool.Invoke(context.Background(), &DashboardWriteParams{
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
		_, err := tool.Invoke(context.Background(), &DashboardWriteParams{
			Instance:  "test",
			Operation: "create",
			Dashboard: `{}`,
			Confirmed: true,
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "must include a title")
	})

	t.Run("missing dashboard", func(t *testing.T) {
		_, err := tool.Invoke(context.Background(), &DashboardWriteParams{
			Instance:  "test",
			Operation: "create",
			Confirmed: true,
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "dashboard is required")
	})

	t.Run("invalid json", func(t *testing.T) {
		_, err := tool.Invoke(context.Background(), &DashboardWriteParams{
			Instance:  "test",
			Operation: "create",
			Dashboard: "not json",
			Confirmed: true,
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid dashboard JSON")
	})

	t.Run("unknown instance", func(t *testing.T) {
		_, err := tool.Invoke(context.Background(), &DashboardWriteParams{
			Instance:  "invalid",
			Operation: "create",
			Dashboard: `{"title":"X"}`,
			Confirmed: true,
		})
		assert.Error(t, err)
	})

	t.Run("invalid operation", func(t *testing.T) {
		_, err := tool.Invoke(context.Background(), &DashboardWriteParams{
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
		result, err := tool.Invoke(context.Background(), &DashboardWriteParams{
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

		_, err = updateTool.Invoke(context.Background(), &DashboardWriteParams{
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
		result, err := tool.Invoke(context.Background(), &DashboardWriteParams{
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
		_, err := tool.Invoke(context.Background(), &DashboardWriteParams{
			Instance:  "test",
			Operation: "delete",
			UID:       "protected-uid",
			Confirmed: true,
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "is protected")
	})

	t.Run("delete nonexistent", func(t *testing.T) {
		_, err := tool.Invoke(context.Background(), &DashboardWriteParams{
			Instance:  "test",
			Operation: "delete",
			UID:       "nonexistent",
			Confirmed: true,
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("delete missing uid", func(t *testing.T) {
		_, err := tool.Invoke(context.Background(), &DashboardWriteParams{
			Instance:  "test",
			Operation: "delete",
			Confirmed: true,
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "uid is required for delete")
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

		_, err = tool.Invoke(context.Background(), &DashboardWriteParams{
			Instance:  "test",
			Operation: "create",
			Dashboard: `{"title":"Kubernetes Evil"}`,
			Confirmed: true,
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "protected blocklist")
	})
}

func TestDashboardWriteToolConstructor(t *testing.T) {
	tool, err := NewDashboardWriteTool(context.Background(), Configs{"t": {URL: "http://localhost"}})
	require.NoError(t, err)
	require.NotNil(t, tool)

	info, err := tool.Info(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "grafana_dashboard_write", info.Name)
}

// TestDashboardWriteToolInputLengthLimits verifies that oversized inputs are
// rejected by validate.Struct before any HTTP request is made (CWE-400).
func TestDashboardWriteToolInputLengthLimits(t *testing.T) {
	tool, err := NewDashboardWriteTool(context.Background(), Configs{"t": {URL: "http://localhost"}})
	require.NoError(t, err)

	t.Run("oversized dashboard JSON rejected", func(t *testing.T) {
		huge := strings.Repeat("x", 1048577) // 1MB + 1
		_, err := tool.Invoke(context.Background(), &DashboardWriteParams{
			Instance:  "t",
			Operation: "create",
			Dashboard: `{"title":"x","fill":"` + huge + `"}`,
			Confirmed: true,
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid parameters")
	})

	t.Run("oversized uid rejected", func(t *testing.T) {
		_, err := tool.Invoke(context.Background(), &DashboardWriteParams{
			Instance:  "t",
			Operation: "delete",
			UID:       strings.Repeat("x", 257),
			Confirmed: true,
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid parameters")
	})

	t.Run("oversized message rejected", func(t *testing.T) {
		_, err := tool.Invoke(context.Background(), &DashboardWriteParams{
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
