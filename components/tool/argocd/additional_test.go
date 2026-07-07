package argocd

import (
	"context"

	"github.com/goccy/go-json"
	"github.com/stretchr/testify/assert"
)

func (t *ToolTestSuite) TestCertificateList() {
	ctx := context.Background()

	listTool, err := NewCertificateListTool(ctx, t.configs)
	assert.NoError(t.T(), err)

	_, err = listTool.Info(ctx)
	assert.NoError(t.T(), err)

	listResult, err := listTool.InvokableRun(ctx, `{"instance": "test"}`)
	assert.NoError(t.T(), err)
	assert.NotEmpty(t.T(), listResult)

	var outputs []CertificateListOutput
	err = json.Unmarshal([]byte(listResult), &outputs)
	assert.NoError(t.T(), err)
	assert.Len(t.T(), outputs, 2)

	assert.Equal(t.T(), "SSL certificate for *.example.com", outputs[0].CertInfo)
	assert.Equal(t.T(), "https", outputs[0].CertType)
	assert.Equal(t.T(), "https://argocd.example.com", outputs[0].ServerName)
	assert.Equal(t.T(), "Git SSH host key", outputs[1].CertInfo)
	assert.Equal(t.T(), "ssh", outputs[1].CertType)

	listResult, err = listTool.InvokableRun(ctx, `{"instance": "test", "filter": "example"}`)
	assert.NoError(t.T(), err)
	assert.NotEmpty(t.T(), listResult)

	err = json.Unmarshal([]byte(listResult), &outputs)
	assert.NoError(t.T(), err)
	assert.Len(t.T(), outputs, 1)
	assert.Equal(t.T(), "SSL certificate for *.example.com", outputs[0].CertInfo)

	_, err = listTool.InvokableRun(ctx, `{"instance": "invalid-instance"}`)
	assert.Error(t.T(), err)
}

func (t *ToolTestSuite) TestClusterList() {
	ctx := context.Background()

	listTool, err := NewClusterListTool(ctx, t.configs)
	assert.NoError(t.T(), err)

	_, err = listTool.Info(ctx)
	assert.NoError(t.T(), err)

	listResult, err := listTool.InvokableRun(ctx, `{"instance": "test"}`)
	assert.NoError(t.T(), err)
	assert.NotEmpty(t.T(), listResult)

	var outputs []ClusterListOutput
	err = json.Unmarshal([]byte(listResult), &outputs)
	assert.NoError(t.T(), err)
	assert.Len(t.T(), outputs, 2)

	assert.Equal(t.T(), "my-cluster", outputs[0].Name)
	assert.Equal(t.T(), "https://cluster1.example.com", outputs[0].Server)
	assert.Equal(t.T(), "production", outputs[0].Project)
	assert.Equal(t.T(), "in-cluster", outputs[1].Name)
	assert.Equal(t.T(), "https://kubernetes.default.svc", outputs[1].Server)
	assert.Equal(t.T(), "default", outputs[1].Project)

	_, err = listTool.InvokableRun(ctx, `{"instance": "invalid-instance"}`)
	assert.Error(t.T(), err)
}

func (t *ToolTestSuite) TestClusterDescribe() {
	ctx := context.Background()

	describeTool, err := NewClusterDescribeTool(ctx, t.configs)
	assert.NoError(t.T(), err)

	_, err = describeTool.Info(ctx)
	assert.NoError(t.T(), err)

	describeResult, err := describeTool.InvokableRun(ctx, `{"instance": "test", "name": "my-cluster"}`)
	assert.NoError(t.T(), err)
	assert.NotEmpty(t.T(), describeResult)
	assert.Contains(t.T(), describeResult, `"name":"my-cluster"`)

	describeResult, err = describeTool.InvokableRun(ctx, `{"instance": "test", "name": "my-cluster", "excludeFieldsOutput": ["metadata"]}`)
	assert.NoError(t.T(), err)
	assert.NotEmpty(t.T(), describeResult)
	assert.NotContains(t.T(), describeResult, `"metadata"`)
	assert.Contains(t.T(), describeResult, `"info"`)

	_, err = describeTool.InvokableRun(ctx, `{"instance": "test", "name": "non-existent"}`)
	assert.Error(t.T(), err)

	_, err = describeTool.InvokableRun(ctx, `{"instance": "invalid-instance", "name": "my-cluster"}`)
	assert.Error(t.T(), err)
}

func (t *ToolTestSuite) TestRepositoryList() {
	ctx := context.Background()

	listTool, err := NewRepositoryListTool(ctx, t.configs)
	assert.NoError(t.T(), err)

	_, err = listTool.Info(ctx)
	assert.NoError(t.T(), err)

	listResult, err := listTool.InvokableRun(ctx, `{"instance": "test"}`)
	assert.NoError(t.T(), err)
	assert.NotEmpty(t.T(), listResult)

	var outputs []RepositoryListOutput
	err = json.Unmarshal([]byte(listResult), &outputs)
	assert.NoError(t.T(), err)
	assert.Len(t.T(), outputs, 2)

	assert.Equal(t.T(), "myapp-repo", outputs[0].Name)
	assert.Equal(t.T(), "Successful", outputs[0].Status)
	assert.Equal(t.T(), "git", outputs[0].Type)
	assert.Equal(t.T(), "https://github.com/myorg/myapp.git", outputs[0].URL)
	assert.Equal(t.T(), "otherapp-repo", outputs[1].Name)
	assert.Equal(t.T(), "Failed", outputs[1].Status)

	_, err = listTool.InvokableRun(ctx, `{"instance": "invalid-instance"}`)
	assert.Error(t.T(), err)
}

func (t *ToolTestSuite) TestRepositoryDescribe() {
	ctx := context.Background()

	describeTool, err := NewRepositoryDescribeTool(ctx, t.configs)
	assert.NoError(t.T(), err)

	_, err = describeTool.Info(ctx)
	assert.NoError(t.T(), err)

	describeResult, err := describeTool.InvokableRun(ctx, `{"instance": "test", "name": "https://github.com/myorg/myapp.git"}`)
	assert.NoError(t.T(), err)
	assert.NotEmpty(t.T(), describeResult)
	assert.Contains(t.T(), describeResult, "myapp-repo")
	assert.Contains(t.T(), describeResult, "Successful")

	describeResult, err = describeTool.InvokableRun(ctx, `{"instance": "test", "name": "https://github.com/myorg/myapp.git", "excludeFieldsOutput": ["metadata"]}`)
	assert.NoError(t.T(), err)
	assert.NotEmpty(t.T(), describeResult)
	assert.NotContains(t.T(), describeResult, `"metadata"`)
	assert.Contains(t.T(), describeResult, `"spec"`)

	_, err = describeTool.InvokableRun(ctx, `{"instance": "invalid-instance", "name": "my-repo"}`)
	assert.Error(t.T(), err)
}
