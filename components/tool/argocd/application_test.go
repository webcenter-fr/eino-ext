package argocd

import (
	"context"
	"fmt"

	"github.com/google/go-cmp/cmp"
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

	expectedOutputs := []ApplicationListOutput{
		{
			Name:       "my-app",
			Namespace:  "argocd",
			Project:    "default",
			Health:     "Healthy",
			SyncStatus: "Synced",
			Revision:   "abc123",
		},
		{
			Name:       "other-app",
			Namespace:  "argocd",
			Project:    "production",
			Health:     "Degraded",
			SyncStatus: "OutOfSync",
		},
	}
	assert.Empty(t.T(), cmp.Diff(listResult, string(MustMarshal(expectedOutputs))))

	listResult, err = listTool.InvokableRun(ctx, `{"instance": "test", "filter": "my-app"}`)
	assert.NoError(t.T(), err)
	assert.NotEmpty(t.T(), listResult)

	expectedOutputs = []ApplicationListOutput{
		{
			Name:       "my-app",
			Namespace:  "argocd",
			Project:    "default",
			Health:     "Healthy",
			SyncStatus: "Synced",
			Revision:   "abc123",
		},
	}
	assert.Empty(t.T(), cmp.Diff(listResult, string(MustMarshal(expectedOutputs))))

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
	assert.Contains(t.T(), describeResult, "my-app")

	describeResult, err = describeTool.InvokableRun(ctx, `{"instance": "test", "name": "my-app", "excludeFieldsOutput": ["metadata", "spec"]}`)
	assert.NoError(t.T(), err)
	assert.NotEmpty(t.T(), describeResult)
	expectedOutput := &ApplicationDescribeOutput{
		Status: &ApplicationStatus{
			Health: &HealthStatus{Status: "Healthy"},
			Sync:   &SyncStatus{Status: "Synced", Revision: "abc123"},
		},
	}
	assert.Empty(t.T(), cmp.Diff(describeResult, string(MustMarshal(expectedOutput))))

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

	syncResult, err := syncTool.InvokableRun(ctx, `{"instance": "test", "name": "my-app"}`)
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

	createResult, err := createTool.InvokableRun(ctx, fmt.Sprintf(`{"instance": "test", "name": "my-new-app", "repoURL": "https://git.example.com/repo", "destServer": "%s"}`, "https://kubernetes.default.svc"))
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

	deleteResult, err := deleteTool.InvokableRun(ctx, `{"instance": "test", "name": "my-app"}`)
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

	expectedOutputs := []ProjectListOutput{
		{
			Name:        "default",
			Description: "Default project",
		},
		{
			Name:        "production",
			Description: "Production project",
		},
	}
	assert.Empty(t.T(), cmp.Diff(listResult, string(MustMarshal(expectedOutputs))))

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
	assert.Contains(t.T(), describeResult, "default")

	describeResult, err = describeTool.InvokableRun(ctx, `{"instance": "test", "name": "default", "excludeFieldsOutput": ["metadata"]}`)
	assert.NoError(t.T(), err)
	assert.NotEmpty(t.T(), describeResult)
	expectedOutput := &ProjectDescribeOutput{
		Spec: map[string]any{
			"description": "Default project",
			"sourceRepos": []any{"*"},
		},
	}
	assert.Empty(t.T(), cmp.Diff(describeResult, string(MustMarshal(expectedOutput))))

	_, err = describeTool.InvokableRun(ctx, `{"instance": "invalid-instance", "name": "default"}`)
	assert.Error(t.T(), err)
}

func (t *ToolTestSuite) TestInstanceList() {
	ctx := context.Background()

	listTool, err := NewInstanceListTool(ctx, t.configs)
	assert.NoError(t.T(), err)

	_, err = listTool.Info(ctx)
	assert.NoError(t.T(), err)

	listResult, err := listTool.InvokableRun(ctx, `{}`)
	assert.NoError(t.T(), err)
	assert.NotEmpty(t.T(), listResult)
	assert.Contains(t.T(), listResult, "test")
}