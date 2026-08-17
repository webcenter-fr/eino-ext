package grafana

import (
	"context"
	"fmt"

	"github.com/goccy/go-json"
	"github.com/stretchr/testify/assert"
)

func (t *ToolTestSuite) TestInstanceList() {
	ctx := context.Background()

	listTool, err := NewInstanceListTool(ctx, t.configs)
	assert.NoError(t.T(), err)

	_, err = listTool.Info(ctx)
	assert.NoError(t.T(), err)

	listResult, err := listTool.InvokableRun(ctx, "")
	assert.NoError(t.T(), err)
	assert.NotEmpty(t.T(), listResult)
	assert.Contains(t.T(), listResult, "test")

	listResult, err = listTool.InvokableRun(ctx, "{}")
	assert.NoError(t.T(), err)
	assert.NotEmpty(t.T(), listResult)
	assert.Contains(t.T(), listResult, "test")
}

func (t *ToolTestSuite) TestDashboard() {
	ctx := context.Background()

	dashboardTool, err := NewDashboardTool(ctx, t.configs)
	assert.NoError(t.T(), err)

	_, err = dashboardTool.Info(ctx)
	assert.NoError(t.T(), err)

	t.Run("search all", func() {
		result, err := dashboardTool.InvokableRun(ctx, `{"instance": "test"}`)
		assert.NoError(t.T(), err)

		var outputs []DashboardSearchOutput
		err = json.Unmarshal([]byte(result), &outputs)
		assert.NoError(t.T(), err)
		assert.Len(t.T(), outputs, 2)
		assert.Contains(t.T(), outputs[0].URL, t.server.URL)
	})

	t.Run("search by query", func() {
		result, err := dashboardTool.InvokableRun(ctx, `{"instance": "test", "query": "Production"}`)
		assert.NoError(t.T(), err)

		var outputs []DashboardSearchOutput
		err = json.Unmarshal([]byte(result), &outputs)
		assert.NoError(t.T(), err)
		assert.Len(t.T(), outputs, 1)
		assert.Equal(t.T(), "Production Overview", outputs[0].Title)
	})

	t.Run("search with filter", func() {
		result, err := dashboardTool.InvokableRun(ctx, `{"instance": "test", "filter": "prod"}`)
		assert.NoError(t.T(), err)

		var outputs []DashboardSearchOutput
		err = json.Unmarshal([]byte(result), &outputs)
		assert.NoError(t.T(), err)
		assert.Len(t.T(), outputs, 1)
	})

	t.Run("search with type filter", func() {
		result, err := dashboardTool.InvokableRun(ctx, `{"instance": "test", "type": "dash-db"}`)
		assert.NoError(t.T(), err)

		var outputs []DashboardSearchOutput
		err = json.Unmarshal([]byte(result), &outputs)
		assert.NoError(t.T(), err)
		assert.Len(t.T(), outputs, 2)
	})

	t.Run("search with pagination", func() {
		result, err := dashboardTool.InvokableRun(ctx, `{"instance": "test", "paginate": {"pageSize": 1, "page": 1}}`)
		assert.NoError(t.T(), err)

		var outputs []DashboardSearchOutput
		err = json.Unmarshal([]byte(result), &outputs)
		assert.NoError(t.T(), err)
		assert.Len(t.T(), outputs, 1)
	})

	t.Run("search unknown instance", func() {
		_, err := dashboardTool.InvokableRun(ctx, `{"instance": "invalid"}`)
		assert.Error(t.T(), err)
	})

	t.Run("search invalid filter regex", func() {
		_, err := dashboardTool.InvokableRun(ctx, `{"instance": "test", "filter": "(?=...)"}`)
		assert.Error(t.T(), err)
	})

	t.Run("describe existing", func() {
		result, err := dashboardTool.InvokableRun(ctx, `{"instance": "test", "uid": "abc123"}`)
		assert.NoError(t.T(), err)
		assert.NotEmpty(t.T(), result)
		assert.Contains(t.T(), result, `"title":"Production Overview"`)
		assert.Contains(t.T(), result, `"panels"`)
		assert.Contains(t.T(), result, `"meta"`)
	})

	t.Run("describe with excludes", func() {
		result, err := dashboardTool.InvokableRun(ctx, `{"instance": "test", "uid": "abc123", "excludeFieldsOutput": ["panels", "meta"]}`)
		assert.NoError(t.T(), err)
		assert.NotEmpty(t.T(), result)
		assert.NotContains(t.T(), result, `"panels"`)
		assert.NotContains(t.T(), result, `"meta"`)
		assert.Contains(t.T(), result, `"dashboard"`)
	})

	t.Run("describe nonexistent uid", func() {
		_, err := dashboardTool.InvokableRun(ctx, `{"instance": "test", "uid": "nonexistent"}`)
		assert.Error(t.T(), err)
	})

	t.Run("describe invalid exclude field", func() {
		_, err := dashboardTool.InvokableRun(ctx, `{"instance": "test", "uid": "abc123", "excludeFieldsOutput": ["foo"]}`)
		assert.Error(t.T(), err)
	})
}

func (t *ToolTestSuite) TestDataSource() {
	ctx := context.Background()

	dataSourceTool, err := NewDataSourceTool(ctx, t.configs)
	assert.NoError(t.T(), err)

	_, err = dataSourceTool.Info(ctx)
	assert.NoError(t.T(), err)

	t.Run("list all", func() {
		result, err := dataSourceTool.InvokableRun(ctx, `{"instance": "test"}`)
		assert.NoError(t.T(), err)

		var outputs []DataSourceListOutput
		err = json.Unmarshal([]byte(result), &outputs)
		assert.NoError(t.T(), err)
		assert.Len(t.T(), outputs, 2)
		assert.Equal(t.T(), "ds-prom", outputs[0].UID)
		assert.Equal(t.T(), "prometheus", outputs[0].Type)
		assert.True(t.T(), outputs[0].IsDefault)
		assert.NotContains(t.T(), result, "should-not-leak")
		assert.NotContains(t.T(), result, "secret-bearer")
		assert.Contains(t.T(), result, `\u003credacted\u003e`)
	})

	t.Run("list with filter", func() {
		result, err := dataSourceTool.InvokableRun(ctx, `{"instance": "test", "filter": "loki"}`)
		assert.NoError(t.T(), err)

		var outputs []DataSourceListOutput
		err = json.Unmarshal([]byte(result), &outputs)
		assert.NoError(t.T(), err)
		assert.Len(t.T(), outputs, 1)
		assert.Equal(t.T(), "Loki", outputs[0].Name)
	})

	t.Run("list unknown instance", func() {
		_, err := dataSourceTool.InvokableRun(ctx, `{"instance": "invalid"}`)
		assert.Error(t.T(), err)
	})

	t.Run("list invalid filter regex", func() {
		_, err := dataSourceTool.InvokableRun(ctx, `{"instance": "test", "filter": "(?=...)"}`)
		assert.Error(t.T(), err)
	})

	t.Run("describe existing", func() {
		result, err := dataSourceTool.InvokableRun(ctx, `{"instance": "test", "uid": "ds-prom"}`)
		assert.NoError(t.T(), err)
		assert.Contains(t.T(), result, `"ds-prom"`)
		assert.Contains(t.T(), result, `"prometheus"`)
		assert.Contains(t.T(), result, `"timeInterval"`)
		assert.Contains(t.T(), result, `"15s"`)
		assert.NotContains(t.T(), result, "should-not-leak")
		assert.NotContains(t.T(), result, "secret-bearer")
		assert.NotContains(t.T(), result, `"secureJsonFields"`)
		assert.NotContains(t.T(), result, `"password"`)
		assert.Contains(t.T(), result, `\u003credacted\u003e`)
	})

	t.Run("describe nonexistent uid", func() {
		_, err := dataSourceTool.InvokableRun(ctx, `{"instance": "test", "uid": "nonexistent"}`)
		assert.Error(t.T(), err)
		assert.Contains(t.T(), err.Error(), "not found")
	})

	t.Run("describe unknown instance", func() {
		_, err := dataSourceTool.InvokableRun(ctx, `{"instance": "invalid", "uid": "ds-prom"}`)
		assert.Error(t.T(), err)
	})
}

func (t *ToolTestSuite) TestDashboardWrite() {
	ctx := context.Background()

	writeTool, err := NewDashboardWriteTool(ctx, t.configs)
	assert.NoError(t.T(), err)

	_, err = writeTool.Info(ctx)
	assert.NoError(t.T(), err)

	t.Run("create dry run", func() {
		dashboardJSON := `{"title": "My New Dashboard"}`
		paramsJSON := fmt.Sprintf(`{"instance": "test", "operation": "create", "dashboard": %q, "dryRun": true}`, dashboardJSON)
		result, err := writeTool.InvokableRun(ctx, paramsJSON)
		assert.NoError(t.T(), err)
		assert.NotEmpty(t.T(), result)
		assert.Contains(t.T(), result, `"dryRun":true`)
	})

	t.Run("create confirmed", func() {
		dashboardJSON := `{"title": "My New Dashboard"}`
		paramsJSON := fmt.Sprintf(`{"instance": "test", "operation": "create", "dashboard": %q, "confirmed": true}`, dashboardJSON)
		result, err := writeTool.InvokableRun(ctx, paramsJSON)
		assert.NoError(t.T(), err)
		assert.NotEmpty(t.T(), result)
		assert.Contains(t.T(), result, `"uid"`)
		assert.Contains(t.T(), result, `"status":"success"`)
		assert.Contains(t.T(), result, t.server.URL)
	})

	t.Run("update existing", func() {
		dashboardJSON := `{"uid": "abc123", "title": "Production Overview"}`
		paramsJSON := fmt.Sprintf(`{"instance": "test", "operation": "update", "dashboard": %q, "overwrite": true, "confirmed": true}`, dashboardJSON)
		result, err := writeTool.InvokableRun(ctx, paramsJSON)
		assert.NoError(t.T(), err)
		assert.NotEmpty(t.T(), result)
		assert.Contains(t.T(), result, `"uid":"new-uid"`)
	})

	t.Run("delete dry run", func() {
		result, err := writeTool.InvokableRun(ctx, `{"instance": "test", "operation": "delete", "uid": "abc123", "dryRun": true}`)
		assert.NoError(t.T(), err)
		assert.Contains(t.T(), result, `"dryRun":true`)
		assert.Contains(t.T(), result, `"delete"`)
	})

	t.Run("delete confirmed", func() {
		result, err := writeTool.InvokableRun(ctx, `{"instance": "test", "operation": "delete", "uid": "abc123", "confirmed": true}`)
		assert.NoError(t.T(), err)
		assert.Contains(t.T(), result, `"status":"success"`)
		assert.Contains(t.T(), result, `"Production Overview"`)
	})

	t.Run("delete protected by uid", func() {
		_, err := writeTool.InvokableRun(ctx, `{"instance": "test", "operation": "delete", "uid": "protected-uid", "confirmed": true}`)
		assert.Error(t.T(), err)
		assert.Contains(t.T(), err.Error(), "is protected")
	})

	t.Run("delete nonexistent", func() {
		_, err := writeTool.InvokableRun(ctx, `{"instance": "test", "operation": "delete", "uid": "nonexistent", "confirmed": true}`)
		assert.Error(t.T(), err)
		assert.Contains(t.T(), err.Error(), "not found")
	})

	t.Run("update protected by uid", func() {
		dashboardJSON := `{"uid": "protected-uid", "title": "Kubernetes Monitoring"}`
		paramsJSON := fmt.Sprintf(`{"instance": "test", "operation": "update", "dashboard": %q, "confirmed": true}`, dashboardJSON)
		_, err := writeTool.InvokableRun(ctx, paramsJSON)
		assert.Error(t.T(), err)
		assert.Contains(t.T(), err.Error(), "is protected")
	})

	t.Run("no confirmation", func() {
		dashboardJSON := `{"title": "X"}`
		paramsJSON := fmt.Sprintf(`{"instance": "test", "operation": "create", "dashboard": %q}`, dashboardJSON)
		_, err := writeTool.InvokableRun(ctx, paramsJSON)
		assert.Error(t.T(), err)
	})

	t.Run("missing title", func() {
		dashboardJSON := `{}`
		paramsJSON := fmt.Sprintf(`{"instance": "test", "operation": "create", "dashboard": %q, "confirmed": true}`, dashboardJSON)
		_, err := writeTool.InvokableRun(ctx, paramsJSON)
		assert.Error(t.T(), err)
		assert.Contains(t.T(), err.Error(), "must include a title")
	})

	t.Run("invalid json", func() {
		_, err := writeTool.InvokableRun(ctx, `{"instance": "test", "operation": "create", "dashboard": "not json", "confirmed": true}`)
		assert.Error(t.T(), err)
		assert.Contains(t.T(), err.Error(), "invalid dashboard JSON")
	})

	t.Run("unknown instance", func() {
		dashboardJSON := `{"title": "X"}`
		paramsJSON := fmt.Sprintf(`{"instance": "invalid", "operation": "create", "dashboard": %q, "confirmed": true}`, dashboardJSON)
		_, err := writeTool.InvokableRun(ctx, paramsJSON)
		assert.Error(t.T(), err)
	})
}

func (t *ToolTestSuite) TestDashboardProtection() {
	protection := buildProtection(ProtectedDashboardsConfig{
		UIDs:          []string{"protected-uid"},
		TitlePrefixes: []string{"Kubernetes "},
		Folders:       []string{"infra-folder"},
		Tags:          []string{"infrastructure"},
	})

	t.Run("nil protection", func() {
		var nilProt *dashboardProtection
		assert.False(t.T(), nilProt.isProtected("abc", "X", "", nil))
	})

	t.Run("uid match", func() {
		assert.True(t.T(), protection.isProtected("protected-uid", "X", "", nil))
	})

	t.Run("title prefix match", func() {
		assert.True(t.T(), protection.isProtected("some-uid", "Kubernetes Monitoring", "", nil))
	})

	t.Run("no title prefix match", func() {
		assert.False(t.T(), protection.isProtected("some-uid", "My App", "", nil))
	})

	t.Run("folder match", func() {
		assert.True(t.T(), protection.isProtected("some-uid", "X", "infra-folder", nil))
	})

	t.Run("tag match", func() {
		assert.True(t.T(), protection.isProtected("some-uid", "X", "", []string{"infrastructure"}))
	})

	t.Run("no tag match", func() {
		assert.False(t.T(), protection.isProtected("some-uid", "X", "", []string{"app"}))
	})

	t.Run("empty config", func() {
		empty := buildProtection(ProtectedDashboardsConfig{})
		assert.Nil(t.T(), empty)
	})
}
