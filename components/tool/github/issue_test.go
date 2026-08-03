package github

import (
	"context"

	"github.com/goccy/go-json"
)

func (s *GitHubToolTestSuite) TestInstanceList() {
	ctx := context.Background()

	listTool, err := newInstanceListTool(ctx, []string{"default", "ghes-prod"})
	s.NoError(err)

	_, err = listTool.Info(ctx)
	s.NoError(err)

	result, err := listTool.InvokableRun(ctx, `{}`)
	s.NoError(err)
	s.NotEmpty(result)
	s.Contains(result, "default")
	s.Contains(result, "ghes-prod")
}

func (s *GitHubToolTestSuite) TestInstanceListDiscoveryViaAllTools() {
	ctx := context.Background()

	tools, err := NewReadOnlyTools(ctx, s.configs())
	s.NoError(err)

	var listResult string
	for _, tl := range tools {
		info, err := tl.Info(ctx)
		s.NoError(err)
		if info.Name == "github_instance_list" {
			listResult, err = tl.InvokableRun(ctx, `{}`)
			s.NoError(err)
			break
		}
	}
	s.NotEmpty(listResult, "expected to find github_instance_list tool in read-only tools")
	s.Contains(listResult, "test")
}

func (s *GitHubToolTestSuite) TestIssueList() {
	ctx := context.Background()

	tool, err := NewIssueListTool(ctx, s.configs())
	s.NoError(err)

	_, err = tool.Info(ctx)
	s.NoError(err)

	result, err := tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo"}`)
	s.NoError(err)
	s.NotEmpty(result)

	var outputs []IssueListOutput
	err = json.Unmarshal([]byte(result), &outputs)
	s.NoError(err)
	s.Len(outputs, 2)
	s.Equal(1, outputs[0].Number)
	s.Equal("Test issue", outputs[0].Title)
	s.Equal("open", outputs[0].State)
	s.Equal("testuser", outputs[0].Author)

	s.Equal(2, outputs[1].Number)
	s.Equal("closed", outputs[1].State)

	result, err = tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "filter": "Test issue"}`)
	s.NoError(err)
	err = json.Unmarshal([]byte(result), &outputs)
	s.NoError(err)
	s.Len(outputs, 1)
	s.Equal("Test issue", outputs[0].Title)

	_, err = tool.InvokableRun(ctx, `{"instance": "invalid-instance", "owner": "testowner", "repo": "testrepo"}`)
	s.Error(err)
}

func (s *GitHubToolTestSuite) TestIssueGet() {
	ctx := context.Background()

	tool, err := NewIssueGetTool(ctx, s.configs())
	s.NoError(err)

	_, err = tool.Info(ctx)
	s.NoError(err)

	result, err := tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "number": 1}`)
	s.NoError(err)
	s.Contains(result, `"number":1`)
	s.Contains(result, `"title":"Test issue"`)
	s.Contains(result, `"body":"Issue body"`)

	result, err = tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "number": 1, "excludeFieldsOutput": ["body", "labels"]}`)
	s.NoError(err)
	s.NotContains(result, `"body"`)
	s.NotContains(result, `"labels"`)

	_, err = tool.InvokableRun(ctx, `{"instance": "invalid-instance", "owner": "testowner", "repo": "testrepo", "number": 1}`)
	s.Error(err)
}

func (s *GitHubToolTestSuite) TestIssueCreate() {
	ctx := context.Background()

	tool, err := NewIssueCreateTool(ctx, s.configs())
	s.NoError(err)

	_, err = tool.Info(ctx)
	s.NoError(err)

	result, err := tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "title": "New issue", "dryRun": true}`)
	s.NoError(err)
	s.Contains(result, `"dryRun": true`)

	result, err = tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "title": "New issue", "confirmed": true}`)
	s.NoError(err)
	s.Contains(result, `"created": true`)
	s.Contains(result, `"number": 3`)

	_, err = tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "title": "New issue"}`)
	s.Error(err)
}

func (s *GitHubToolTestSuite) TestIssueComment() {
	ctx := context.Background()

	tool, err := NewIssueCommentTool(ctx, s.configs())
	s.NoError(err)

	_, err = tool.Info(ctx)
	s.NoError(err)

	result, err := tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "number": 1, "body": "Test comment", "dryRun": true}`)
	s.NoError(err)
	s.Contains(result, `"dryRun": true`)

	result, err = tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "number": 1, "body": "Test comment", "confirmed": true}`)
	s.NoError(err)
	s.Contains(result, `"created": true`)
	s.Contains(result, `"id": 100`)

	_, err = tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "number": 1, "body": "Test comment"}`)
	s.Error(err)
}
