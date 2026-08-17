package grafana

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/goccy/go-json"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewDashboardToolRequiresConfigs(t *testing.T) {
	_, err := NewDashboardTool(context.Background(), Configs{})
	assert.Error(t, err)
}

func newDashboardTestTool(t *testing.T, handler http.HandlerFunc) *DashboardTool {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	tool, err := NewDashboardTool(context.Background(), Configs{"test": {URL: server.URL}})
	require.NoError(t, err)
	return tool
}

func TestDashboardToolSearch(t *testing.T) {
	const searchJSON = `[
		{"id":1,"uid":"abc123","title":"Production Overview","url":"/d/abc123/prod","type":"dash-db","tags":["prod"],"folderTitle":"Infra","folderUid":"folder-1","starred":true},
		{"id":2,"uid":"def456","title":"Staging Dashboard","url":"/d/def456/staging","type":"dash-db","tags":["staging"],"folderTitle":"","folderUid":"folder-2","starred":false}
	]`
	tool := newDashboardTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/search" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("limit") == "1" {
			_, _ = w.Write([]byte(`[{"id":1,"uid":"abc123","title":"Production Overview","url":"/d/abc123/prod","type":"dash-db","tags":["prod"],"folderTitle":"Infra","folderUid":"folder-1","starred":true}]`))
			return
		}
		_, _ = w.Write([]byte(searchJSON))
	})

	t.Run("search all", func(t *testing.T) {
		result, err := tool.Invoke(context.Background(), &DashboardParams{Instance: "test"})
		require.NoError(t, err)

		var outputs []DashboardSearchOutput
		require.NoError(t, json.Unmarshal([]byte(result), &outputs))
		assert.Len(t, outputs, 2)
	})

	t.Run("search with filter", func(t *testing.T) {
		result, err := tool.Invoke(context.Background(), &DashboardParams{Instance: "test", Filter: "Staging"})
		require.NoError(t, err)

		var outputs []DashboardSearchOutput
		require.NoError(t, json.Unmarshal([]byte(result), &outputs))
		assert.Len(t, outputs, 1)
		assert.Equal(t, "def456", outputs[0].UID)
	})

	t.Run("search with pagination", func(t *testing.T) {
		result, err := tool.Invoke(context.Background(), &DashboardParams{
			Instance: "test",
			Paginate: &DashboardPaginate{PageSize: 1, Page: 1},
		})
		require.NoError(t, err)

		var outputs []DashboardSearchOutput
		require.NoError(t, json.Unmarshal([]byte(result), &outputs))
		assert.Len(t, outputs, 1)
	})

	t.Run("search unknown instance", func(t *testing.T) {
		_, err := tool.Invoke(context.Background(), &DashboardParams{Instance: "invalid"})
		assert.Error(t, err)
	})

	t.Run("search invalid filter regex", func(t *testing.T) {
		_, err := tool.Invoke(context.Background(), &DashboardParams{Instance: "test", Filter: "(?=...)"})
		assert.Error(t, err)
	})
}

func TestDashboardToolDescribe(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/dashboards/uid/abc123", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"dashboard":{"uid":"abc123","title":"Production Overview","panels":[{"id":1}]},"meta":{"folderUid":"folder-1","version":3}}`))
	})
	mux.HandleFunc("/api/dashboards/uid/nonexistent", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Dashboard not found"}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	tool, err := NewDashboardTool(context.Background(), Configs{"test": {URL: server.URL}})
	require.NoError(t, err)

	t.Run("describe existing", func(t *testing.T) {
		result, err := tool.Invoke(context.Background(), &DashboardParams{Instance: "test", UID: "abc123"})
		require.NoError(t, err)
		assert.Contains(t, result, `"Production Overview"`)
		assert.Contains(t, result, `"panels"`)
	})

	t.Run("describe with excludes", func(t *testing.T) {
		result, err := tool.Invoke(context.Background(), &DashboardParams{
			Instance:            "test",
			UID:                 "abc123",
			ExcludeFieldsOutput: []string{"panels", "meta"},
		})
		require.NoError(t, err)
		assert.NotContains(t, result, `"panels"`)
		assert.NotContains(t, result, `"meta"`)
	})

	t.Run("describe nonexistent uid", func(t *testing.T) {
		_, err := tool.Invoke(context.Background(), &DashboardParams{Instance: "test", UID: "nonexistent"})
		assert.Error(t, err)
	})

	t.Run("describe invalid exclude field", func(t *testing.T) {
		_, err := tool.Invoke(context.Background(), &DashboardParams{
			Instance:            "test",
			UID:                 "abc123",
			ExcludeFieldsOutput: []string{"foo"},
		})
		assert.Error(t, err)
	})
}

func TestDashboardToolConstructor(t *testing.T) {
	tool, err := NewDashboardTool(context.Background(), Configs{"t": {URL: "http://localhost"}})
	require.NoError(t, err)
	require.NotNil(t, tool)

	info, err := tool.Info(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "grafana_dashboard", info.Name)
}
