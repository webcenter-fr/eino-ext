//go:build integration

package grafana

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/goccy/go-json"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func grafanaURL() string {
	u := os.Getenv("GRAFANA_URL")
	if u == "" {
		u = "http://localhost:32768"
	}
	return u
}

func grafanaToken() string {
	return os.Getenv("GRAFANA_TOKEN")
}

func setupTestDashboards(t *testing.T, baseURL, token string) {
	t.Helper()
	client := &http.Client{}
	authHdr := "Bearer " + token

	// Re-create the known test dashboard in a known state
	dashboards := []struct {
		payload string
	}{
		{`{"dashboard":{"uid":"test-dashboard-001","title":"Test Production Overview","tags":["test","production"],"panels":[{"id":1,"title":"CPU","type":"graph","gridPos":{"h":9,"w":12,"x":0,"y":0}}],"schemaVersion":36},"folderUid":"test-folder","message":"Setup for integration test","overwrite":true}`},
		{`{"dashboard":{"uid":"test-staging-002","title":"Staging Environment","tags":["test","staging"],"panels":[],"schemaVersion":36},"message":"Setup for integration test","overwrite":true}`},
		{`{"dashboard":{"uid":"kube-protected","title":"Kubernetes Monitoring","tags":["infrastructure"],"panels":[],"schemaVersion":36},"message":"Setup for integration test","overwrite":true}`},
	}

	for _, d := range dashboards {
		req, err := http.NewRequest("POST", baseURL+"/api/dashboards/db", strings.NewReader(d.payload))
		require.NoError(t, err, "failed to build setup request")
		req.Header.Set("Authorization", authHdr)
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		require.NoError(t, err, "failed to create test dashboard")
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("unexpected status %d during dashboard setup", resp.StatusCode)
		}
	}
}

func TestIntegrationAllTools(t *testing.T) {
	baseURL := grafanaURL()
	token := grafanaToken()
	if token == "" {
		t.Skip("GRAFANA_TOKEN not set, skipping integration test")
	}

	setupTestDashboards(t, baseURL, token)

	configs := Configs{
		"test": {
			URL:   baseURL,
			Token: token,
			ProtectedDashboards: ProtectedDashboardsConfig{
				UIDs:          []string{"kube-protected"},
				TitlePrefixes: []string{"Kubernetes "},
				Tags:          []string{"infrastructure"},
			},
		},
	}

	ctx := context.Background()

	// ─── 1. Instance List ────────────────────────────────────────────────

	t.Run("instance_list", func(t *testing.T) {
		tool, err := NewInstanceListTool(ctx, configs)
		require.NoError(t, err)

		result, err := tool.InvokableRun(ctx, "{}")
		require.NoError(t, err)
		assert.Contains(t, result, "test")
	})

	// ─── 2. Dashboard Search (READ — must run before any write tests) ────

	t.Run("dashboard_search", func(t *testing.T) {
		tool, err := NewDashboardSearchTool(ctx, configs)
		require.NoError(t, err)

		t.Run("search all", func(t *testing.T) {
			result, err := tool.InvokableRun(ctx, `{"instance":"test"}`)
			require.NoError(t, err)

			var outputs []DashboardSearchOutput
			err = json.Unmarshal([]byte(result), &outputs)
			require.NoError(t, err)
			assert.GreaterOrEqual(t, len(outputs), 3, "should find at least our 3 test dashboards")
		})

		t.Run("search by query", func(t *testing.T) {
			result, err := tool.InvokableRun(ctx, `{"instance":"test","query":"Production"}`)
			require.NoError(t, err)

			var outputs []DashboardSearchOutput
			err = json.Unmarshal([]byte(result), &outputs)
			require.NoError(t, err)
			assert.Len(t, outputs, 1)
			assert.Equal(t, "Test Production Overview", outputs[0].Title)
			assert.Equal(t, "test-dashboard-001", outputs[0].UID)
			assert.True(t, strings.HasPrefix(outputs[0].URL, baseURL), "URL should be full: %s", outputs[0].URL)
		})

		t.Run("search by type", func(t *testing.T) {
			result, err := tool.InvokableRun(ctx, `{"instance":"test","type":"dash-db"}`)
			require.NoError(t, err)

			var outputs []DashboardSearchOutput
			err = json.Unmarshal([]byte(result), &outputs)
			require.NoError(t, err)
			for _, o := range outputs {
				assert.Equal(t, "dash-db", o.Type)
			}
		})

		t.Run("search with filter", func(t *testing.T) {
			result, err := tool.InvokableRun(ctx, `{"instance":"test","filter":"staging"}`)
			require.NoError(t, err)

			var outputs []DashboardSearchOutput
			err = json.Unmarshal([]byte(result), &outputs)
			require.NoError(t, err)
			assert.Len(t, outputs, 1)
			assert.Equal(t, "Staging Environment", outputs[0].Title)
		})

		t.Run("search with pagination", func(t *testing.T) {
			result, err := tool.InvokableRun(ctx, `{"instance":"test","paginate":{"pageSize":1,"page":1}}`)
			require.NoError(t, err)

			var outputs []DashboardSearchOutput
			err = json.Unmarshal([]byte(result), &outputs)
			require.NoError(t, err)
			assert.Len(t, outputs, 1)
		})
	})

	// ─── 3. Dashboard Describe (READ — must run before any write tests) ───

	t.Run("dashboard_describe", func(t *testing.T) {
		tool, err := NewDashboardDescribeTool(ctx, configs)
		require.NoError(t, err)

		t.Run("describe existing", func(t *testing.T) {
			result, err := tool.InvokableRun(ctx, `{"instance":"test","uid":"test-dashboard-001"}`)
			require.NoError(t, err)
			assert.Contains(t, result, `"Test Production Overview"`)
			assert.Contains(t, result, `"panels"`)
			assert.Contains(t, result, `"meta"`)
		})

		t.Run("describe with excludes", func(t *testing.T) {
			result, err := tool.InvokableRun(ctx, `{"instance":"test","uid":"test-dashboard-001","excludeFieldsOutput":["panels","meta"]}`)
			require.NoError(t, err)
			assert.NotContains(t, result, `"panels"`)
			assert.NotContains(t, result, `"meta"`)
			assert.Contains(t, result, `"dashboard"`)
		})

		t.Run("describe nonexistent", func(t *testing.T) {
			_, err := tool.InvokableRun(ctx, `{"instance":"test","uid":"nonexistent-dashboard"}`)
			assert.Error(t, err)
		})
	})

	// ─── 4. Data Source List (READ — must run before any write tests) ───

	t.Run("datasource_list", func(t *testing.T) {
		tool, err := NewDataSourceListTool(ctx, configs)
		require.NoError(t, err)

		result, err := tool.InvokableRun(ctx, `{"instance":"test"}`)
		require.NoError(t, err)

		var outputs []DataSourceListOutput
		err = json.Unmarshal([]byte(result), &outputs)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(outputs), 1, "should find at least one data source")

		assert.NotContains(t, result, token, "raw token must not appear in data source list output")
	})

	// ─── 5. Data Source Describe (READ — must run before any write tests) ─

	t.Run("datasource_describe", func(t *testing.T) {
		listTool, err := NewDataSourceListTool(ctx, configs)
		require.NoError(t, err)

		listResult, err := listTool.InvokableRun(ctx, `{"instance":"test"}`)
		require.NoError(t, err)

		var outputs []DataSourceListOutput
		err = json.Unmarshal([]byte(listResult), &outputs)
		require.NoError(t, err)
		require.NotEmpty(t, outputs, "need at least one data source to describe")
		firstUID := outputs[0].UID
		require.NotEmpty(t, firstUID, "first data source must have a UID")

		describeTool, err := NewDataSourceDescribeTool(ctx, configs)
		require.NoError(t, err)

		result, err := describeTool.InvokableRun(ctx, fmt.Sprintf(`{"instance":"test","uid":%q}`, firstUID))
		require.NoError(t, err)
		assert.Contains(t, result, `"uid"`)
		assert.Contains(t, result, `"type"`)
		assert.NotContains(t, result, `"password"`)
		assert.NotContains(t, result, `"basicAuthPassword"`)
		assert.NotContains(t, result, token, "raw token must not appear in data source describe output")
	})

	// ─── 6. Dashboard Build (WRITE tests — all run after reads) ──────────

	t.Run("dashboard_build", func(t *testing.T) {
		tool, err := NewDashboardBuildTool(ctx, configs)
		require.NoError(t, err)

		t.Run("dry run new dashboard", func(t *testing.T) {
			params := fmt.Sprintf(`{"instance":"test","dashboard":%q,"dryRun":true}`, `{"title":"Integration Test Dashboard","tags":["integration-test"]}`)
			result, err := tool.InvokableRun(ctx, params)
			require.NoError(t, err)
			assert.Contains(t, result, `"dryRun":true`)
			assert.Contains(t, result, `"Integration Test Dashboard"`)
		})

		t.Run("create new dashboard", func(t *testing.T) {
			params := fmt.Sprintf(`{"instance":"test","dashboard":%q,"confirmed":true}`, `{"title":"Integration Test Dashboard","tags":["integration-test"]}`)
			result, err := tool.InvokableRun(ctx, params)
			require.NoError(t, err)

			var out DashboardBuildOutput
			err = json.Unmarshal([]byte(result), &out)
			require.NoError(t, err)
			assert.NotEmpty(t, out.UID)
			assert.Equal(t, "success", out.Status)
			assert.True(t, strings.HasPrefix(out.URL, baseURL))
		})

		t.Run("update existing dashboard", func(t *testing.T) {
			params := fmt.Sprintf(`{"instance":"test","dashboard":%q,"overwrite":true,"confirmed":true}`, `{"uid":"test-dashboard-001","title":"Test Production Updated","tags":["test","updated"],"panels":[{"id":1,"title":"CPU","type":"graph"}],"schemaVersion":36}`)
			result, err := tool.InvokableRun(ctx, params)
			require.NoError(t, err)

			var out DashboardBuildOutput
			err = json.Unmarshal([]byte(result), &out)
			require.NoError(t, err)
			assert.Equal(t, "test-dashboard-001", out.UID)
			assert.Equal(t, "success", out.Status)

			// verify title changed
			describeTool, _ := NewDashboardDescribeTool(ctx, configs)
			dr, err := describeTool.InvokableRun(ctx, `{"instance":"test","uid":"test-dashboard-001","excludeFieldsOutput":["panels"]}`)
			require.NoError(t, err)
			assert.Contains(t, dr, `"Test Production Updated"`)
		})

		t.Run("protected by UID", func(t *testing.T) {
			params := fmt.Sprintf(`{"instance":"test","dashboard":%q,"confirmed":true}`, `{"uid":"kube-protected","title":"Kubernetes Monitoring"}`)
			_, err := tool.InvokableRun(ctx, params)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "is protected")
		})

		t.Run("block new dashboard with protected title", func(t *testing.T) {
			params := fmt.Sprintf(`{"instance":"test","dashboard":%q,"confirmed":true}`, `{"title":"Kubernetes My New Dashboard"}`)
			_, err := tool.InvokableRun(ctx, params)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "protected blocklist")
		})

		t.Run("no confirmation", func(t *testing.T) {
			params := fmt.Sprintf(`{"instance":"test","dashboard":%q}`, `{"title":"No Confirm Dashboard"}`)
			_, err := tool.InvokableRun(ctx, params)
			assert.Error(t, err)
		})

		t.Run("missing title", func(t *testing.T) {
			_, err := tool.InvokableRun(ctx, `{"instance":"test","dashboard":"{}","confirmed":true}`)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "must include a title")
		})
	})
}
