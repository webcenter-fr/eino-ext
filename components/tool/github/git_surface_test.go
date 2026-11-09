package github

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/goccy/go-json"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	toolkitsafety "github.com/webcenter-fr/eino-ext/libs/toolkit/safety"
)

func newLocalClone(t *testing.T) (*pullRemote, string) {
	t.Helper()
	remote := newPullRemote(t)
	cloneDir := t.TempDir()
	clonePullFixture(t, remote.remotePath, cloneDir, defaultSession, "testowner", "testrepo")
	return remote, cloneDir
}

func cloneHeadHash(t *testing.T, clonePath_ string) plumbing.Hash {
	t.Helper()
	repo, err := git.PlainOpen(clonePath_)
	require.NoError(t, err)
	head, err := repo.Head()
	require.NoError(t, err)
	return head.Hash()
}

func currentBranch(t *testing.T, clonePath_ string) string {
	t.Helper()
	repo, err := git.PlainOpen(clonePath_)
	require.NoError(t, err)
	head, err := repo.Head()
	require.NoError(t, err)
	return head.Name().Short()
}

// ---- github_file_edit ----

func TestFileEditNotFound(t *testing.T) {
	ctx := context.Background()
	_, cloneDir := newLocalClone(t)
	tool, err := NewFileEditTool(ctx, pullConfigs(cloneDir))
	require.NoError(t, err)

	_, err = tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "path": "README.md", "oldString": "zzz", "newString": "x"}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestFileEditNonUnique(t *testing.T) {
	ctx := context.Background()
	_, cloneDir := newLocalClone(t)
	repoPath := testRepoPath(cloneDir)
	require.NoError(t, os.WriteFile(filepath.Join(repoPath, "README.md"), []byte("line1\nline2\nline3\n"), 0o644))

	tool, err := NewFileEditTool(ctx, pullConfigs(cloneDir))
	require.NoError(t, err)

	_, err = tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "path": "README.md", "oldString": "line", "newString": "row"}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "occurs 3 times")
}

func TestFileEditReplaceAll(t *testing.T) {
	ctx := context.Background()
	_, cloneDir := newLocalClone(t)
	repoPath := testRepoPath(cloneDir)
	require.NoError(t, os.WriteFile(filepath.Join(repoPath, "README.md"), []byte("line1\nline2\nline3\n"), 0o644))

	tool, err := NewFileEditTool(ctx, pullConfigs(cloneDir))
	require.NoError(t, err)

	result, err := tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "path": "README.md", "oldString": "line", "newString": "row", "replaceAll": true}`)
	require.NoError(t, err)

	var output FileEditOutput
	require.NoError(t, json.Unmarshal([]byte(result), &output))
	assert.Equal(t, "README.md", output.Path)
	assert.Equal(t, 3, output.Replacements)
	assert.Equal(t, len("row1\nrow2\nrow3\n"), output.Bytes)

	data, err := os.ReadFile(filepath.Join(repoPath, "README.md"))
	require.NoError(t, err)
	assert.Equal(t, "row1\nrow2\nrow3\n", string(data))
}

func TestFileEditReplaceOnce(t *testing.T) {
	ctx := context.Background()
	_, cloneDir := newLocalClone(t)
	repoPath := testRepoPath(cloneDir)
	require.NoError(t, os.WriteFile(filepath.Join(repoPath, "README.md"), []byte("line1\nline2\nline3\n"), 0o644))

	tool, err := NewFileEditTool(ctx, pullConfigs(cloneDir))
	require.NoError(t, err)

	result, err := tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "path": "README.md", "oldString": "line2", "newString": "LINE2"}`)
	require.NoError(t, err)

	var output FileEditOutput
	require.NoError(t, json.Unmarshal([]byte(result), &output))
	assert.Equal(t, 1, output.Replacements)

	data, err := os.ReadFile(filepath.Join(repoPath, "README.md"))
	require.NoError(t, err)
	assert.Equal(t, "line1\nLINE2\nline3\n", string(data))
}

func TestFileEditDryRunNoMutation(t *testing.T) {
	ctx := context.Background()
	_, cloneDir := newLocalClone(t)
	repoPath := testRepoPath(cloneDir)
	require.NoError(t, os.WriteFile(filepath.Join(repoPath, "README.md"), []byte("line1\nline2\nline3\n"), 0o644))

	tool, err := NewFileEditTool(ctx, pullConfigs(cloneDir))
	require.NoError(t, err)

	result, err := tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "path": "README.md", "oldString": "line", "newString": "row", "replaceAll": true, "dryRun": true}`)
	require.NoError(t, err)
	assert.Contains(t, result, `"replacements":3`)

	data, err := os.ReadFile(filepath.Join(repoPath, "README.md"))
	require.NoError(t, err)
	assert.Equal(t, "line1\nline2\nline3\n", string(data), "dry-run must not write")
}

// ---- github_branch_checkout ----

func TestBranchCheckoutExisting(t *testing.T) {
	ctx := context.Background()
	_, cloneDir := newLocalClone(t)
	repoPath := testRepoPath(cloneDir)

	repo, err := git.PlainOpen(repoPath)
	require.NoError(t, err)
	head, err := repo.Head()
	require.NoError(t, err)
	wt, err := repo.Worktree()
	require.NoError(t, err)
	require.NoError(t, wt.Checkout(&git.CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName("feature"),
		Hash:   head.Hash(),
		Create: true,
	}))

	tool, err := NewBranchCheckoutTool(ctx, pullConfigs(cloneDir))
	require.NoError(t, err)

	result, err := tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "branch": "main"}`)
	require.NoError(t, err)

	var output BranchCheckoutOutput
	require.NoError(t, json.Unmarshal([]byte(result), &output))
	assert.Equal(t, "main", output.Branch)
	assert.False(t, output.Created)
	assert.Equal(t, "main", currentBranch(t, repoPath))
}

func TestBranchCheckoutCreateFromBase(t *testing.T) {
	ctx := context.Background()
	_, cloneDir := newLocalClone(t)
	tool, err := NewBranchCheckoutTool(ctx, pullConfigs(cloneDir))
	require.NoError(t, err)

	result, err := tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "branch": "feature", "baseBranch": "main", "create": true}`)
	require.NoError(t, err)

	var output BranchCheckoutOutput
	require.NoError(t, json.Unmarshal([]byte(result), &output))
	assert.Equal(t, "feature", output.Branch)
	assert.True(t, output.Created)
	assert.Equal(t, "feature", currentBranch(t, testRepoPath(cloneDir)))
}

func TestBranchCheckoutCreateFromHead(t *testing.T) {
	ctx := context.Background()
	_, cloneDir := newLocalClone(t)
	tool, err := NewBranchCheckoutTool(ctx, pullConfigs(cloneDir))
	require.NoError(t, err)

	// Empty baseBranch must default to HEAD (previously ResolveRevision("") failed).
	result, err := tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "branch": "feature", "create": true}`)
	require.NoError(t, err)

	var output BranchCheckoutOutput
	require.NoError(t, json.Unmarshal([]byte(result), &output))
	assert.True(t, output.Created)
	assert.Equal(t, "feature", currentBranch(t, testRepoPath(cloneDir)))
}

func TestBranchCheckoutMissingWithoutCreate(t *testing.T) {
	ctx := context.Background()
	_, cloneDir := newLocalClone(t)
	tool, err := NewBranchCheckoutTool(ctx, pullConfigs(cloneDir))
	require.NoError(t, err)

	_, err = tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "branch": "missing"}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not exist")
}

func TestBranchCheckoutDryRunNoMutation(t *testing.T) {
	ctx := context.Background()
	_, cloneDir := newLocalClone(t)
	tool, err := NewBranchCheckoutTool(ctx, pullConfigs(cloneDir))
	require.NoError(t, err)

	result, err := tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "branch": "feature", "create": true, "dryRun": true}`)
	require.NoError(t, err)
	assert.Contains(t, result, `"dryRun":true`)

	assert.Equal(t, "main", currentBranch(t, testRepoPath(cloneDir)))
}

// ---- github_status ----

func TestStatusClean(t *testing.T) {
	ctx := context.Background()
	_, cloneDir := newLocalClone(t)
	tool, err := NewStatusTool(ctx, pullConfigs(cloneDir))
	require.NoError(t, err)

	result, err := tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo"}`)
	require.NoError(t, err)

	var output StatusOutput
	require.NoError(t, json.Unmarshal([]byte(result), &output))
	assert.Equal(t, "main", output.Branch)
	assert.True(t, output.Clean)
	assert.Empty(t, output.Files)
	assert.False(t, output.Truncated)
}

func TestStatusModifiedAndTruncated(t *testing.T) {
	ctx := context.Background()
	_, cloneDir := newLocalClone(t)
	repoPath := testRepoPath(cloneDir)
	require.NoError(t, os.WriteFile(filepath.Join(repoPath, "README.md"), []byte("changed\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(repoPath, "new.txt"), []byte("new\n"), 0o644))

	tool, err := NewStatusTool(ctx, pullConfigs(cloneDir))
	require.NoError(t, err)

	result, err := tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "maxResults": 1}`)
	require.NoError(t, err)

	var output StatusOutput
	require.NoError(t, json.Unmarshal([]byte(result), &output))
	assert.False(t, output.Clean)
	require.Len(t, output.Files, 1)
	assert.True(t, output.Truncated)
	assert.Equal(t, "README.md", output.Files[0].Path)
	assert.Equal(t, "modified", output.Files[0].Status)
}

// ---- github_diff ----

func TestDiffModified(t *testing.T) {
	ctx := context.Background()
	_, cloneDir := newLocalClone(t)
	repoPath := testRepoPath(cloneDir)
	require.NoError(t, os.WriteFile(filepath.Join(repoPath, "README.md"), []byte("v2\n"), 0o644))

	tool, err := NewDiffTool(ctx, pullConfigs(cloneDir))
	require.NoError(t, err)

	result, err := tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo"}`)
	require.NoError(t, err)

	var output DiffOutput
	require.NoError(t, json.Unmarshal([]byte(result), &output))
	assert.Contains(t, output.Diff, "--- a/README.md")
	assert.Contains(t, output.Diff, "+++ b/README.md")
	assert.Contains(t, output.Diff, "-v1")
	assert.Contains(t, output.Diff, "+v2")
	assert.False(t, output.Truncated)
}

func TestDiffTruncated(t *testing.T) {
	ctx := context.Background()
	_, cloneDir := newLocalClone(t)
	repoPath := testRepoPath(cloneDir)
	require.NoError(t, os.WriteFile(filepath.Join(repoPath, "README.md"), []byte("a very long changed line\n"), 0o644))

	tool, err := NewDiffTool(ctx, pullConfigs(cloneDir))
	require.NoError(t, err)

	result, err := tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "maxBytes": 20}`)
	require.NoError(t, err)

	var output DiffOutput
	require.NoError(t, json.Unmarshal([]byte(result), &output))
	assert.True(t, output.Truncated)
	assert.Contains(t, output.Diff, "truncated")
}

func TestDiffClean(t *testing.T) {
	ctx := context.Background()
	_, cloneDir := newLocalClone(t)
	tool, err := NewDiffTool(ctx, pullConfigs(cloneDir))
	require.NoError(t, err)

	result, err := tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo"}`)
	require.NoError(t, err)

	var output DiffOutput
	require.NoError(t, json.Unmarshal([]byte(result), &output))
	assert.Empty(t, output.Diff)
	assert.False(t, output.Truncated)
}

// ---- github_commit_push ----

func newCommitPushToolForTest(t *testing.T, ctx context.Context, cloneDir, remotePath string) *CommitPushTool {
	t.Helper()
	base, err := newBaseTool(ctx, pullConfigs(cloneDir))
	require.NoError(t, err)
	base.pushURLOverride = remotePath
	tool, err := newCommitPushTool(ctx, base)
	require.NoError(t, err)
	return tool
}

func TestCommitPushDryRunShape(t *testing.T) {
	ctx := context.Background()
	remote, cloneDir := newLocalClone(t)
	repoPath := testRepoPath(cloneDir)
	require.NoError(t, os.WriteFile(filepath.Join(repoPath, "README.md"), []byte("v2\n"), 0o644))

	before := cloneHeadHash(t, repoPath)
	tool := newCommitPushToolForTest(t, ctx, cloneDir, remote.remotePath)

	result, err := tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "branch": "main", "commitMessage": "update readme", "dryRun": true}`)
	require.NoError(t, err)

	var output CommitPushDryRunOutput
	require.NoError(t, json.Unmarshal([]byte(result), &output))
	assert.Equal(t, "main", output.Branch)
	assert.Equal(t, "refs/heads/main", output.RemoteBranch)
	assert.Equal(t, "update readme", output.CommitMessage)
	require.Len(t, output.Files, 1)
	assert.Equal(t, "README.md", output.Files[0].Path)
	assert.Equal(t, "modified", output.Files[0].Status)
	assert.Greater(t, output.Files[0].Insertions, 0)
	assert.Greater(t, output.Files[0].Deletions, 0)
	assert.Contains(t, output.Diffstat, "1 file(s) changed")
	assert.False(t, output.Truncated)

	// No side effects.
	assert.Equal(t, before, cloneHeadHash(t, repoPath))
	assert.Equal(t, remote.head(t), before.String())
}

func TestCommitPushUnauthorized(t *testing.T) {
	ctx := context.Background()
	remote, cloneDir := newLocalClone(t)
	repoPath := testRepoPath(cloneDir)
	require.NoError(t, os.WriteFile(filepath.Join(repoPath, "README.md"), []byte("v2\n"), 0o644))

	tool := newCommitPushToolForTest(t, ctx, cloneDir, remote.remotePath)
	_, err := tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "branch": "main", "commitMessage": "update readme", "confirmed": true}`)
	require.ErrorIs(t, err, toolkitsafety.ErrExecutionNotAuthorized)
}

func TestCommitPushConfirmed(t *testing.T) {
	ctx := context.Background()
	remote, cloneDir := newLocalClone(t)
	repoPath := testRepoPath(cloneDir)
	require.NoError(t, os.WriteFile(filepath.Join(repoPath, "README.md"), []byte("v2\n"), 0o644))

	tool := newCommitPushToolForTest(t, ctx, cloneDir, remote.remotePath)
	authCtx := toolkitsafety.WithExecutionAuthorized(ctx, "github_commit_push")

	result, err := tool.InvokableRun(authCtx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "branch": "main", "commitMessage": "update readme", "confirmed": true}`)
	require.NoError(t, err)

	var output CommitPushOutput
	require.NoError(t, json.Unmarshal([]byte(result), &output))
	assert.Equal(t, "main", output.Branch)
	assert.True(t, output.Committed)
	assert.True(t, output.Pushed)
	assert.NotEmpty(t, output.CommitSHA)
	assert.Equal(t, output.CommitSHA, remote.head(t))
	assert.Equal(t, output.CommitSHA, cloneHeadHash(t, repoPath).String())
}

func TestCommitPushAlreadyUpToDate(t *testing.T) {
	ctx := context.Background()
	remote, cloneDir := newLocalClone(t)
	repoPath := testRepoPath(cloneDir)

	// Commit and push manually so the clone and the remote already match.
	repo, err := git.PlainOpen(repoPath)
	require.NoError(t, err)
	wt, err := repo.Worktree()
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(repoPath, "README.md"), []byte("v2\n"), 0o644))
	_, err = wt.Add("README.md")
	require.NoError(t, err)
	_, err = wt.Commit("update readme", &git.CommitOptions{Author: commitIdentity})
	require.NoError(t, err)
	require.NoError(t, repo.Push(&git.PushOptions{}))

	tool := newCommitPushToolForTest(t, ctx, cloneDir, remote.remotePath)
	authCtx := toolkitsafety.WithExecutionAuthorized(ctx, "github_commit_push")

	result, err := tool.InvokableRun(authCtx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "branch": "main", "commitMessage": "no changes", "confirmed": true}`)
	require.NoError(t, err)

	var output CommitPushOutput
	require.NoError(t, json.Unmarshal([]byte(result), &output))
	assert.False(t, output.Committed)
	assert.False(t, output.Pushed)
}

func TestCommitPushNonFastForward(t *testing.T) {
	ctx := context.Background()
	remote, cloneDir := newLocalClone(t)
	repoPath := testRepoPath(cloneDir)

	// Local commit not pushed.
	repo, err := git.PlainOpen(repoPath)
	require.NoError(t, err)
	wt, err := repo.Worktree()
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(repoPath, "local.txt"), []byte("local\n"), 0o644))
	_, err = wt.Add("local.txt")
	require.NoError(t, err)
	_, err = wt.Commit("local commit", &git.CommitOptions{Author: commitIdentity})
	require.NoError(t, err)

	// Remote diverges.
	remote.pushCommit(t, "remote change", "v2\n")

	tool := newCommitPushToolForTest(t, ctx, cloneDir, remote.remotePath)
	authCtx := toolkitsafety.WithExecutionAuthorized(ctx, "github_commit_push")

	_, err = tool.InvokableRun(authCtx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "branch": "main", "commitMessage": "local commit", "confirmed": true}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "non-fast-forward")
}

func TestCommitPushPaths(t *testing.T) {
	ctx := context.Background()
	remote, cloneDir := newLocalClone(t)
	repoPath := testRepoPath(cloneDir)
	require.NoError(t, os.WriteFile(filepath.Join(repoPath, "one.txt"), []byte("one\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(repoPath, "two.txt"), []byte("two\n"), 0o644))

	tool := newCommitPushToolForTest(t, ctx, cloneDir, remote.remotePath)
	authCtx := toolkitsafety.WithExecutionAuthorized(ctx, "github_commit_push")

	result, err := tool.InvokableRun(authCtx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "branch": "main", "commitMessage": "add one", "paths": ["one.txt"], "confirmed": true}`)
	require.NoError(t, err)

	var output CommitPushOutput
	require.NoError(t, json.Unmarshal([]byte(result), &output))
	assert.True(t, output.Committed)
	assert.True(t, output.Pushed)

	// The second file remains untracked/staged-out.
	status, err := git.PlainOpen(repoPath)
	require.NoError(t, err)
	wt, err := status.Worktree()
	require.NoError(t, err)
	ws, err := wt.Status()
	require.NoError(t, err)
	require.NotNil(t, ws["two.txt"])
	assert.Equal(t, git.Untracked, ws["two.txt"].Worktree)
}
