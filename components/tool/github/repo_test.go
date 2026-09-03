package github

import (
	"context"

	"github.com/goccy/go-json"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/safety"
)

func (s *GitHubToolTestSuite) TestOrgRepoList() {
	ctx := context.Background()

	tool, err := NewOrgRepoListTool(ctx, s.configs())
	s.NoError(err)

	_, err = tool.Info(ctx)
	s.NoError(err)

	result, err := tool.InvokableRun(ctx, `{"instance": "test", "org": "testorg"}`)
	s.NoError(err)

	var outputs []OrgRepoListOutput
	err = json.Unmarshal([]byte(result), &outputs)
	s.NoError(err)
	s.Len(outputs, 2)
	s.Equal("repo1", outputs[0].Name)
	s.Equal("testorg/repo1", outputs[0].FullName)
	s.Equal("repo2", outputs[1].Name)
	s.Equal("testorg/repo2", outputs[1].FullName)

	_, err = tool.InvokableRun(ctx, `{"instance": "invalid-instance", "org": "testorg"}`)
	s.Error(err)
}

// TestOrgRepoListFilterAcrossPages verifies that pagination traverses every
// page before applying the filter: "repo2" only exists on page 2, so a filter
// matching it must return exactly one result. This guards against regressions
// where pagination is capped before all pages are fetched.
func (s *GitHubToolTestSuite) TestOrgRepoListFilterAcrossPages() {
	ctx := context.Background()

	tool, err := NewOrgRepoListTool(ctx, s.configs())
	s.NoError(err)

	result, err := tool.InvokableRun(ctx, `{"instance": "test", "org": "testorg", "perPage": 100, "filter": "repo2"}`)
	s.NoError(err)

	var outputs []OrgRepoListOutput
	err = json.Unmarshal([]byte(result), &outputs)
	s.NoError(err)
	s.Len(outputs, 1)
	s.Equal("repo2", outputs[0].Name)
}

// TestOrgRepoListMaxPagesLimit verifies that maxPages limits pagination, so
// "repo2" on page 2 is not fetched when maxPages=1.
func (s *GitHubToolTestSuite) TestOrgRepoListMaxPagesLimit() {
	ctx := context.Background()

	tool, err := NewOrgRepoListTool(ctx, s.configs())
	s.NoError(err)

	result, err := tool.InvokableRun(ctx, `{"instance": "test", "org": "testorg", "perPage": 100, "maxPages": 1}`)
	s.NoError(err)

	var outputs []OrgRepoListOutput
	err = json.Unmarshal([]byte(result), &outputs)
	s.NoError(err)
	s.Len(outputs, 1)
	s.Equal("repo1", outputs[0].Name)
}

func (s *GitHubToolTestSuite) TestRepoClonedDryRun() {
	ctx := context.Background()

	tool, err := NewRepoCloneTool(ctx, s.configs())
	s.NoError(err)

	_, err = tool.Info(ctx)
	s.NoError(err)

	result, err := tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "dryRun": true}`)
	s.NoError(err)
	s.Contains(result, `"dryRun": true`)
	s.Contains(result, `/tmp/test-github-clones/default/testowner/testrepo`)
}

func (s *GitHubToolTestSuite) TestWebhookUpsert() {
	ctx := context.Background()

	tool, err := NewWebhookUpsertTool(ctx, s.configs())
	s.NoError(err)

	_, err = tool.Info(ctx)
	s.NoError(err)

	result, err := tool.InvokableRun(safety.WithExecutionAuthorized(ctx, "github_webhook_upsert"), `{"instance": "test", "owner": "testowner", "repo": "testrepo", "hookUrl": "https://hooks.example.com/webhook", "confirmed": true}`)
	s.NoError(err)
	s.Contains(result, `"id": 500`)

	_, err = tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "hookUrl": "http://localhost:8080/webhook"}`)
	s.Error(err)

	_, err = tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "hookUrl": "https://127.0.0.1/webhook"}`)
	s.Error(err)
}

func (s *GitHubToolTestSuite) TestRepoSettingsUpdate() {
	ctx := context.Background()

	tool, err := NewRepoSettingsUpdateTool(ctx, s.configs())
	s.NoError(err)

	_, err = tool.Info(ctx)
	s.NoError(err)

	result, err := tool.InvokableRun(safety.WithExecutionAuthorized(ctx, "github_repo_settings_update"), `{"instance": "test", "owner": "testowner", "repo": "testrepo", "description": "Updated desc", "confirmed": true}`)
	s.NoError(err)
	s.Contains(result, `"updated": true`)

	_, err = tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "description": "Updated desc"}`)
	s.Error(err)
}

func (s *GitHubToolTestSuite) TestReleaseCreate() {
	ctx := context.Background()

	tool, err := NewReleaseCreateTool(ctx, s.configs())
	s.NoError(err)

	_, err = tool.Info(ctx)
	s.NoError(err)

	result, err := tool.InvokableRun(safety.WithExecutionAuthorized(ctx, "github_release_create"), `{"instance": "test", "owner": "testowner", "repo": "testrepo", "tagName": "v1.0.0", "name": "v1.0.0 Release", "confirmed": true}`)
	s.NoError(err)
	s.Contains(result, `"id": 600`)
}

func (s *GitHubToolTestSuite) TestBranchCreateRemote() {
	ctx := context.Background()

	tool, err := NewBranchCreateTool(ctx, s.configs())
	s.NoError(err)

	_, err = tool.Info(ctx)
	s.NoError(err)

	result, err := tool.InvokableRun(safety.WithExecutionAuthorized(ctx, "github_branch_create"), `{"instance": "test", "owner": "testowner", "repo": "testrepo-remote", "branchName": "new-branch", "remote": true, "confirmed": true}`)
	s.NoError(err)
	s.Contains(result, `"mode": "remote"`)
}
