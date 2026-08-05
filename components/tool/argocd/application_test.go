package argocd

import (
	"context"
	"fmt"

	"github.com/goccy/go-json"
	"github.com/stretchr/testify/assert"
)

func (t *ToolTestSuite) TestApplicationList() {
	ctx := context.Background()

	listTool, err := NewApplicationListTool(ctx, t.configs)
	assert.NoError(t.T(), err)

	_, err = listTool.Info(ctx)
	assert.NoError(t.T(), err)

	listResult, err := listTool.InvokableRun(ctx, `{"instance": "test"}`)
	assert.NoError(t.T(), err)
	assert.NotEmpty(t.T(), listResult)

	var outputs []ApplicationListOutput
	err = json.Unmarshal([]byte(listResult), &outputs)
	assert.NoError(t.T(), err)
	assert.Len(t.T(), outputs, 2)

	assert.Equal(t.T(), "my-app", outputs[0].Name)
	assert.Equal(t.T(), "argocd", outputs[0].Namespace)
	assert.Equal(t.T(), "default", outputs[0].Project)
	assert.Equal(t.T(), "Healthy", outputs[0].Health)
	assert.Equal(t.T(), "Synced", outputs[0].SyncStatus)
	assert.Equal(t.T(), "abc123", outputs[0].Revision)

	assert.Equal(t.T(), "other-app", outputs[1].Name)
	assert.Equal(t.T(), "production", outputs[1].Project)
	assert.Equal(t.T(), "Degraded", outputs[1].Health)
	assert.Equal(t.T(), "OutOfSync", outputs[1].SyncStatus)

	listResult, err = listTool.InvokableRun(ctx, `{"instance": "test", "filter": "my-app"}`)
	assert.NoError(t.T(), err)
	assert.NotEmpty(t.T(), listResult)

	err = json.Unmarshal([]byte(listResult), &outputs)
	assert.NoError(t.T(), err)
	assert.Len(t.T(), outputs, 1)
	assert.Equal(t.T(), "my-app", outputs[0].Name)

	_, err = listTool.InvokableRun(ctx, `{"instance": "invalid-instance"}`)
	assert.Error(t.T(), err)
}

func (t *ToolTestSuite) TestApplicationDescribe() {
	ctx := context.Background()

	describeTool, err := NewApplicationDescribeTool(ctx, t.configs)
	assert.NoError(t.T(), err)

	_, err = describeTool.Info(ctx)
	assert.NoError(t.T(), err)

	describeResult, err := describeTool.InvokableRun(ctx, `{"instance": "test", "name": "my-app"}`)
	assert.NoError(t.T(), err)
	assert.NotEmpty(t.T(), describeResult)
	assert.Contains(t.T(), describeResult, `"name":"my-app"`)
	assert.Contains(t.T(), describeResult, `"health"`)
	assert.Contains(t.T(), describeResult, `"sync"`)

	describeResult, err = describeTool.InvokableRun(ctx, `{"instance": "test", "name": "my-app", "excludeFieldsOutput": ["metadata", "spec"]}`)
	assert.NoError(t.T(), err)
	assert.NotEmpty(t.T(), describeResult)
	assert.NotContains(t.T(), describeResult, `"metadata"`)
	assert.NotContains(t.T(), describeResult, `"spec"`)
	assert.Contains(t.T(), describeResult, `"status"`)

	_, err = describeTool.InvokableRun(ctx, `{"instance": "test", "name": "non-existent"}`)
	assert.Error(t.T(), err)

	_, err = describeTool.InvokableRun(ctx, `{"instance": "invalid-instance", "name": "my-app"}`)
	assert.Error(t.T(), err)
}

func (t *ToolTestSuite) TestApplicationSync() {
	ctx := context.Background()

	syncTool, err := NewApplicationSyncTool(ctx, t.configs)
	assert.NoError(t.T(), err)

	_, err = syncTool.Info(ctx)
	assert.NoError(t.T(), err)

	syncResult, err := syncTool.InvokableRun(ctx, `{"instance": "test", "name": "my-app", "confirmed": true}`)
	assert.NoError(t.T(), err)
	assert.NotEmpty(t.T(), syncResult)
	assert.Contains(t.T(), syncResult, "my-app")

	_, err = syncTool.InvokableRun(ctx, `{"instance": "invalid-instance", "name": "my-app"}`)
	assert.Error(t.T(), err)
}

func (t *ToolTestSuite) TestApplicationCreate() {
	ctx := context.Background()

	createTool, err := NewApplicationCreateTool(ctx, t.configs)
	assert.NoError(t.T(), err)

	_, err = createTool.Info(ctx)
	assert.NoError(t.T(), err)

	createResult, err := createTool.InvokableRun(ctx, fmt.Sprintf(`{"instance": "test", "name": "my-new-app", "repoURL": "https://git.example.com/repo", "destServer": "%s", "confirmed": true}`, "https://kubernetes.default.svc"))
	assert.NoError(t.T(), err)
	assert.NotEmpty(t.T(), createResult)
	assert.Contains(t.T(), createResult, "my-new-app")

	_, err = createTool.InvokableRun(ctx, `{"instance": "invalid-instance", "name": "test"}`)
	assert.Error(t.T(), err)
}

func (t *ToolTestSuite) TestApplicationDelete() {
	ctx := context.Background()

	deleteTool, err := NewApplicationDeleteTool(ctx, t.configs)
	assert.NoError(t.T(), err)

	_, err = deleteTool.Info(ctx)
	assert.NoError(t.T(), err)

	deleteResult, err := deleteTool.InvokableRun(ctx, `{"instance": "test", "name": "my-app", "confirmed": true}`)
	assert.NoError(t.T(), err)
	assert.Contains(t.T(), deleteResult, "deleted successfully")

	_, err = deleteTool.InvokableRun(ctx, `{"instance": "invalid-instance", "name": "my-app"}`)
	assert.Error(t.T(), err)
}

func (t *ToolTestSuite) TestProjectList() {
	ctx := context.Background()

	listTool, err := NewProjectListTool(ctx, t.configs)
	assert.NoError(t.T(), err)

	_, err = listTool.Info(ctx)
	assert.NoError(t.T(), err)

	listResult, err := listTool.InvokableRun(ctx, `{"instance": "test"}`)
	assert.NoError(t.T(), err)
	assert.NotEmpty(t.T(), listResult)

	var outputs []ProjectListOutput
	err = json.Unmarshal([]byte(listResult), &outputs)
	assert.NoError(t.T(), err)
	assert.Len(t.T(), outputs, 2)

	assert.Equal(t.T(), "default", outputs[0].Name)
	assert.Equal(t.T(), "Default project", outputs[0].Description)
	assert.Equal(t.T(), "production", outputs[1].Name)
	assert.Equal(t.T(), "Production project", outputs[1].Description)

	_, err = listTool.InvokableRun(ctx, `{"instance": "invalid-instance"}`)
	assert.Error(t.T(), err)
}

func (t *ToolTestSuite) TestProjectDescribe() {
	ctx := context.Background()

	describeTool, err := NewProjectDescribeTool(ctx, t.configs)
	assert.NoError(t.T(), err)

	_, err = describeTool.Info(ctx)
	assert.NoError(t.T(), err)

	describeResult, err := describeTool.InvokableRun(ctx, `{"instance": "test", "name": "default"}`)
	assert.NoError(t.T(), err)
	assert.NotEmpty(t.T(), describeResult)
	assert.Contains(t.T(), describeResult, `"name":"default"`)
	assert.Contains(t.T(), describeResult, "Default project")

	describeResult, err = describeTool.InvokableRun(ctx, `{"instance": "test", "name": "default", "excludeFieldsOutput": ["metadata"]}`)
	assert.NoError(t.T(), err)
	assert.NotEmpty(t.T(), describeResult)
	assert.NotContains(t.T(), describeResult, `"metadata"`)
	assert.Contains(t.T(), describeResult, `"spec"`)

	_, err = describeTool.InvokableRun(ctx, `{"instance": "invalid-instance", "name": "default"}`)
	assert.Error(t.T(), err)
}

func (t *ToolTestSuite) TestInstanceList() {
	ctx := context.Background()

	listTool, err := NewInstanceListTool(ctx, t.configs)
	assert.NoError(t.T(), err)

	_, err = listTool.Info(ctx)
	assert.NoError(t.T(), err)

	// Empty string arguments — this was the bug: sonic requires valid JSON.
	listResult, err := listTool.InvokableRun(ctx, "")
	assert.NoError(t.T(), err)
	assert.NotEmpty(t.T(), listResult)
	assert.Contains(t.T(), listResult, "test")

	// Empty JSON object arguments — also valid for a no-param tool.
	listResult, err = listTool.InvokableRun(ctx, "{}")
	assert.NoError(t.T(), err)
	assert.NotEmpty(t.T(), listResult)
	assert.Contains(t.T(), listResult, "test")
}
