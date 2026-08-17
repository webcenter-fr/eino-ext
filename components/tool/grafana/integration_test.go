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

	// ─── 2. Dashboard read (search + describe) ───────────────────────────

	t.Run("dashboard_read", func(t *testing.T) {
		tool, err := NewDashboardTool(ctx, configs)
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

		t.Run("search with filter", func(t *testing.T) {
			result, err := tool.InvokableRun(ctx, `{"instance":"test","filter":"staging"}`)
			require.NoError(t, err)

			var outputs []DashboardSearchOutput
			err = json.Unmarshal([]byte(result), &outputs)
			require.NoError(t, err)
			assert.Len(t, outputs, 1)
			assert.Equal(t, "Staging Environment", outputs[0].Title)
		})

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
	})

	// ─── 3. Data source read (list + describe) ───────────────────────────

	t.Run("datasource_read", func(t *testing.T) {
		tool, err := NewDataSourceTool(ctx, configs)
		require.NoError(t, err)

		result, err := tool.InvokableRun(ctx, `{"instance":"test"}`)
		require.NoError(t, err)

		var outputs []DataSourceListOutput
		err = json.Unmarshal([]byte(result), &outputs)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(outputs), 1, "should find at least one data source")
		assert.NotContains(t, result, token, "raw token must not appear in data source list output")

		require.NotEmpty(t, outputs, "need at least one data source to describe")
		firstUID := outputs[0].UID
		require.NotEmpty(t, firstUID, "first data source must have a UID")

		describeResult, err := tool.InvokableRun(ctx, fmt.Sprintf(`{"instance":"test","uid":%q}`, firstUID))
		require.NoError(t, err)
		assert.Contains(t, describeResult, `"uid"`)
		assert.Contains(t, describeResult, `"type"`)
		assert.NotContains(t, describeResult, `"password"`)
		assert.NotContains(t, describeResult, `"basicAuthPassword"`)
		assert.NotContains(t, describeResult, token, "raw token must not appear in data source describe output")
	})

	// ─── 4. Dashboard write (create/update/delete) ───────────────────────

	t.Run("dashboard_write", func(t *testing.T) {
		tool, err := NewDashboardWriteTool(ctx, configs)
		require.NoError(t, err)

		t.Run("dry run new dashboard", func(t *testing.T) {
			params := fmt.Sprintf(`{"instance":"test","operation":"create","dashboard":%q,"dryRun":true}`, `{"title":"Integration Test Dashboard","tags":["integration-test"]}`)
			result, err := tool.InvokableRun(ctx, params)
			require.NoError(t, err)
			assert.Contains(t, result, `"dryRun":true`)
			assert.Contains(t, result, `"Integration Test Dashboard"`)
		})

		t.Run("create new dashboard", func(t *testing.T) {
			params := fmt.Sprintf(`{"instance":"test","operation":"create","dashboard":%q,"confirmed":true}`, `{"title":"Integration Test Dashboard","tags":["integration-test"]}`)
			result, err := tool.InvokableRun(ctx, params)
			require.NoError(t, err)

			var out DashboardSaveOutput
			err = json.Unmarshal([]byte(result), &out)
			require.NoError(t, err)
			assert.NotEmpty(t, out.UID)
			assert.Equal(t, "success", out.Status)
			assert.True(t, strings.HasPrefix(out.URL, baseURL))
		})

		t.Run("update existing dashboard", func(t *testing.T) {
			params := fmt.Sprintf(`{"instance":"test","operation":"update","dashboard":%q,"overwrite":true,"confirmed":true}`, `{"uid":"test-dashboard-001","title":"Test Production Updated","tags":["test","updated"],"panels":[{"id":1,"title":"CPU","type":"graph"}],"schemaVersion":36}`)
			result, err := tool.InvokableRun(ctx, params)
			require.NoError(t, err)

			var out DashboardSaveOutput
			err = json.Unmarshal([]byte(result), &out)
			require.NoError(t, err)
			assert.Equal(t, "test-dashboard-001", out.UID)
			assert.Equal(t, "success", out.Status)
		})

		t.Run("protected by UID", func(t *testing.T) {
			params := fmt.Sprintf(`{"instance":"test","operation":"update","dashboard":%q,"confirmed":true}`, `{"uid":"kube-protected","title":"Kubernetes Monitoring"}`)
			_, err := tool.InvokableRun(ctx, params)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "is protected")
		})

		t.Run("block new dashboard with protected title", func(t *testing.T) {
			params := fmt.Sprintf(`{"instance":"test","operation":"create","dashboard":%q,"confirmed":true}`, `{"title":"Kubernetes My New Dashboard"}`)
			_, err := tool.InvokableRun(ctx, params)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "protected blocklist")
		})

		t.Run("delete nonexistent", func(t *testing.T) {
			_, err := tool.InvokableRun(ctx, `{"instance":"test","operation":"delete","uid":"nonexistent-dashboard","confirmed":true}`)
			assert.Error(t, err)
		})

		t.Run("no confirmation", func(t *testing.T) {
			params := fmt.Sprintf(`{"instance":"test","operation":"create","dashboard":%q}`, `{"title":"No Confirm Dashboard"}`)
			_, err := tool.InvokableRun(ctx, params)
			assert.Error(t, err)
		})

		t.Run("missing title", func(t *testing.T) {
			_, err := tool.InvokableRun(ctx, `{"instance":"test","operation":"create","dashboard":"{}","confirmed":true}`)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "must include a title")
		})

		t.Run("delete existing dashboard", func(t *testing.T) {
			result, err := tool.InvokableRun(ctx, `{"instance":"test","operation":"delete","uid":"test-staging-002","confirmed":true}`)
			require.NoError(t, err)

			var out DashboardDeleteOutput
			err = json.Unmarshal([]byte(result), &out)
			require.NoError(t, err)
			assert.Equal(t, "test-staging-002", out.UID)
			assert.Equal(t, "success", out.Status)
		})
	})
}
