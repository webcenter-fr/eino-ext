package github

import (
	"context"

	"github.com/goccy/go-json"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/safety"
)

func (s *GitHubToolTestSuite) TestPRList() {
	ctx := context.Background()

	tool, err := NewPRListTool(ctx, s.configs())
	s.NoError(err)

	_, err = tool.Info(ctx)
	s.NoError(err)

	result, err := tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo"}`)
	s.NoError(err)
	s.NotEmpty(result)

	var outputs []PRListOutput
	err = json.Unmarshal([]byte(result), &outputs)
	s.NoError(err)
	s.Len(outputs, 1)
	s.Equal(10, outputs[0].Number)
	s.Equal("Test PR", outputs[0].Title)
	s.Equal("pruser", outputs[0].Author)
	s.Equal("main", outputs[0].BaseBranch)
	s.Equal("feature-branch", outputs[0].HeadBranch)

	_, err = tool.InvokableRun(ctx, `{"instance": "invalid-instance", "owner": "testowner", "repo": "testrepo"}`)
	s.Error(err)
}

func (s *GitHubToolTestSuite) TestPRGet() {
	ctx := context.Background()

	tool, err := NewPRGetTool(ctx, s.configs())
	s.NoError(err)

	_, err = tool.Info(ctx)
	s.NoError(err)

	result, err := tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "number": 10}`)
	s.NoError(err)
	s.Contains(result, `"number":10`)
	s.Contains(result, `"title":"Test PR"`)
	s.Contains(result, `"body":"PR body"`)

	result, err = tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "number": 10, "excludeFieldsOutput": ["body", "labels"]}`)
	s.NoError(err)
	s.NotContains(result, `"body"`)
	s.NotContains(result, `"labels"`)

	_, err = tool.InvokableRun(ctx, `{"instance": "invalid-instance", "owner": "testowner", "repo": "testrepo", "number": 10}`)
	s.Error(err)
}

func (s *GitHubToolTestSuite) TestPRCreate() {
	ctx := context.Background()

	tool, err := NewPRCreateTool(ctx, s.configs())
	s.NoError(err)

	_, err = tool.Info(ctx)
	s.NoError(err)

	result, err := tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "title": "New PR", "head": "feature-x", "dryRun": true}`)
	s.NoError(err)
	s.Contains(result, `"dryRun": true`)

	result, err = tool.InvokableRun(safety.WithExecutionAuthorized(ctx, "github_pr_create"), `{"instance": "test", "owner": "testowner", "repo": "testrepo", "title": "New PR", "head": "feature-x", "confirmed": true}`)
	s.NoError(err)
	s.Contains(result, `"created": true`)
	s.Contains(result, `"number": 11`)

	_, err = tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "title": "New PR", "head": "feature-x"}`)
	s.Error(err)
}

func (s *GitHubToolTestSuite) TestPRComment() {
	ctx := context.Background()

	tool, err := NewPRCommentTool(ctx, s.configs())
	s.NoError(err)

	_, err = tool.Info(ctx)
	s.NoError(err)

	result, err := tool.InvokableRun(safety.WithExecutionAuthorized(ctx, "github_pr_comment"), `{"instance": "test", "owner": "testowner", "repo": "testrepo", "number": 10, "body": "Nice PR!", "confirmed": true}`)
	s.NoError(err)
	s.Contains(result, `"created": true`)
	s.Contains(result, `"id": 200`)

	_, err = tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "number": 10, "body": "Nice PR!"}`)
	s.Error(err)
}

func (s *GitHubToolTestSuite) TestPRReview() {
	ctx := context.Background()

	tool, err := NewPRReviewTool(ctx, s.configs())
	s.NoError(err)

	_, err = tool.Info(ctx)
	s.NoError(err)

	result, err := tool.InvokableRun(safety.WithExecutionAuthorized(ctx, "github_pr_review"), `{"instance": "test", "owner": "testowner", "repo": "testrepo", "number": 10, "event": "APPROVE", "confirmed": true}`)
	s.NoError(err)
	s.Contains(result, `"id": 300`)

	_, err = tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "number": 10, "event": "INVALID"}`)
	s.Error(err)
}

func (s *GitHubToolTestSuite) TestPRSuggestChange() {
	ctx := context.Background()

	tool, err := NewPRSuggestChangeTool(ctx, s.configs())
	s.NoError(err)

	_, err = tool.Info(ctx)
	s.NoError(err)

	result, err := tool.InvokableRun(safety.WithExecutionAuthorized(ctx, "github_pr_suggest_change"), `{"instance": "test", "owner": "testowner", "repo": "testrepo", "number": 10, "commitId": "abc123", "filePath": "main.go", "line": 42, "body": "Consider using a constant here.", "confirmed": true}`)
	s.NoError(err)
	s.Contains(result, `"id": 400`)
	s.Contains(result, `"path":`)
	s.Contains(result, `"main.go"`)

	_, err = tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "number": 10, "commitId": "abc123", "filePath": "main.go", "line": 42, "body": "Change this."}`)
	s.Error(err)
}

func (s *GitHubToolTestSuite) TestPRRequestReviewers() {
	ctx := context.Background()

	tool, err := NewPRRequestReviewersTool(ctx, s.configs())
	s.NoError(err)

	_, err = tool.Info(ctx)
	s.NoError(err)

	result, err := tool.InvokableRun(safety.WithExecutionAuthorized(ctx, "github_pr_request_reviewers"), `{"instance": "test", "owner": "testowner", "repo": "testrepo", "number": 10, "reviewers": ["reviewer1", "reviewer2"], "confirmed": true}`)
	s.NoError(err)
	s.Contains(result, `"requestedReviewers"`)

	_, err = tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "number": 10, "reviewers": ["reviewer1"]}`)
	s.Error(err)
}
