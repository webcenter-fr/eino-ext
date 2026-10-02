package github

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/goccy/go-json"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/fileutil"
)

// pullRemote is a local bare "remote" repository plus a seed worktree used to
// create and push commits. Everything runs through go-git's file transport:
// no network and no GitHub API are involved.
type pullRemote struct {
	remotePath string
	seedPath   string
}

// newPullRemote creates a bare remote with one commit on "main" (pushed from
// the seed worktree).
func newPullRemote(t *testing.T) *pullRemote {
	t.Helper()

	remotePath := filepath.Join(t.TempDir(), "remote.git")
	_, err := git.PlainInitWithOptions(remotePath, &git.PlainInitOptions{
		Bare: true,
		InitOptions: git.InitOptions{
			DefaultBranch: plumbing.NewBranchReferenceName("main"),
		},
	})
	require.NoError(t, err)

	r := &pullRemote{remotePath: remotePath, seedPath: t.TempDir()}
	seed, err := git.PlainInitWithOptions(r.seedPath, &git.PlainInitOptions{
		InitOptions: git.InitOptions{
			DefaultBranch: plumbing.NewBranchReferenceName("main"),
		},
	})
	require.NoError(t, err)

	_, err = seed.CreateRemote(&config.RemoteConfig{Name: "origin", URLs: []string{remotePath}})
	require.NoError(t, err)

	r.pushCommit(t, "initial", "v1\n")
	return r
}

// pushCommit appends a commit modifying README.md in the seed worktree and
// pushes it to the bare remote.
func (r *pullRemote) pushCommit(t *testing.T, msg, content string) {
	t.Helper()

	seed, err := git.PlainOpen(r.seedPath)
	require.NoError(t, err)
	wt, err := seed.Worktree()
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(r.seedPath, "README.md"), []byte(content), 0o644))
	_, err = wt.Add("README.md")
	require.NoError(t, err)
	_, err = wt.Commit(msg, &git.CommitOptions{Author: commitIdentity})
	require.NoError(t, err)

	require.NoError(t, seed.Push(&git.PushOptions{}))
}

// head resolves refs/heads/main in the bare remote.
func (r *pullRemote) head(t *testing.T) string {
	t.Helper()

	remote, err := git.PlainOpen(r.remotePath)
	require.NoError(t, err)
	ref, err := remote.Reference(plumbing.NewBranchReferenceName("main"), true)
	require.NoError(t, err)
	return ref.Hash().String()
}

// clonePullFixture clones the remote into <cloneDir>/<session>/<owner>/<repo>.
func clonePullFixture(t *testing.T, remoteURL, cloneDir, session, owner, repo string) {
	t.Helper()

	_, err := git.PlainClone(filepath.Join(cloneDir, session, owner, repo), false, &git.CloneOptions{URL: remoteURL})
	require.NoError(t, err)
}

func pullConfigs(cloneDir string) Configs {
	return Configs{
		"test": {
			Token:    "test-token",
			CloneDir: cloneDir,
		},
	}
}

type repoPullResult struct {
	Pulled          bool   `json:"pulled"`
	AlreadyUpToDate bool   `json:"alreadyUpToDate"`
	Path            string `json:"path"`
	Branch          string `json:"branch"`
	PreviousHead    string `json:"previousHead"`
	HeadCommit      string `json:"headCommit"`
}

// cloneHead returns the current HEAD hash of the cloned repository.
func cloneHead(t *testing.T, clonePath_ string) string {
	t.Helper()

	repo, err := git.PlainOpen(clonePath_)
	require.NoError(t, err)
	head, err := repo.Head()
	require.NoError(t, err)
	return head.Hash().String()
}

func TestRepoPullFastForwardAndUpToDate(t *testing.T) {
	ctx := context.Background()
	r := newPullRemote(t)
	cloneDir := t.TempDir()
	clonePullFixture(t, r.remotePath, cloneDir, defaultSession, "testowner", "testrepo")

	tool, err := NewRepoPullTool(ctx, pullConfigs(cloneDir))
	require.NoError(t, err)
	_, err = tool.Info(ctx)
	require.NoError(t, err)

	// Remote gains a commit after the clone; the pull fast-forwards to it.
	r.pushCommit(t, "second", "v2\n")

	result, err := tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "branch": "main"}`)
	require.NoError(t, err)

	var out repoPullResult
	require.NoError(t, json.Unmarshal([]byte(result), &out))
	assert.True(t, out.Pulled)
	assert.False(t, out.AlreadyUpToDate)
	assert.Equal(t, "main", out.Branch)
	assert.Contains(t, out.Path, filepath.Join("default", "testowner", "testrepo"))
	assert.NotEqual(t, out.PreviousHead, out.HeadCommit)
	assert.Equal(t, r.head(t), out.HeadCommit)
	assert.Equal(t, r.head(t), cloneHead(t, testRepoPath(cloneDir)))

	// A second pull is a no-op reported as already up to date.
	result, err = tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "branch": "main"}`)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal([]byte(result), &out))
	assert.True(t, out.Pulled)
	assert.True(t, out.AlreadyUpToDate)
}

func TestRepoPullDirtyWorktree(t *testing.T) {
	ctx := context.Background()
	r := newPullRemote(t)
	cloneDir := t.TempDir()
	clonePullFixture(t, r.remotePath, cloneDir, defaultSession, "testowner", "testrepo")

	tool, err := NewRepoPullTool(ctx, pullConfigs(cloneDir))
	require.NoError(t, err)

	// Uncommitted modification of a tracked file.
	repoPath := testRepoPath(cloneDir)
	require.NoError(t, os.WriteFile(filepath.Join(repoPath, "README.md"), []byte("local edit\n"), 0o644))
	before := cloneHead(t, repoPath)

	r.pushCommit(t, "second", "v2\n")

	_, err = tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "branch": "main"}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "uncommitted changes")

	// Nothing was discarded: the local edit and HEAD are untouched.
	data, readErr := os.ReadFile(filepath.Join(repoPath, "README.md"))
	require.NoError(t, readErr)
	assert.Equal(t, "local edit\n", string(data))
	assert.Equal(t, before, cloneHead(t, repoPath))
}

func TestRepoPullMissingClone(t *testing.T) {
	ctx := context.Background()
	tool, err := NewRepoPullTool(ctx, pullConfigs(t.TempDir()))
	require.NoError(t, err)

	_, err = tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo"}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "github_repo_clone")
}

func TestRepoPullNonFastForward(t *testing.T) {
	ctx := context.Background()
	r := newPullRemote(t)
	cloneDir := t.TempDir()
	clonePullFixture(t, r.remotePath, cloneDir, defaultSession, "testowner", "testrepo")

	tool, err := NewRepoPullTool(ctx, pullConfigs(cloneDir))
	require.NoError(t, err)

	// Both sides advance: the remote gains a commit and the clone commits
	// locally, so the branches diverge and no fast-forward is possible.
	r.pushCommit(t, "remote change", "remote\n")

	repoPath := testRepoPath(cloneDir)
	repo, err := git.PlainOpen(repoPath)
	require.NoError(t, err)
	wt, err := repo.Worktree()
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(repoPath, "local.txt"), []byte("local work\n"), 0o644))
	_, err = wt.Add("local.txt")
	require.NoError(t, err)
	localHash, err := wt.Commit("local commit", &git.CommitOptions{Author: commitIdentity})
	require.NoError(t, err)

	_, err = tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "branch": "main"}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "commits not present on the remote")

	// The local commit is preserved: HEAD still points at it and the file is
	// still present in the worktree.
	assert.Equal(t, localHash.String(), cloneHead(t, repoPath))
	data, readErr := os.ReadFile(filepath.Join(repoPath, "local.txt"))
	require.NoError(t, readErr)
	assert.Equal(t, "local work\n", string(data))
}

func TestRepoPullDryRun(t *testing.T) {
	ctx := context.Background()
	r := newPullRemote(t)
	cloneDir := t.TempDir()
	clonePullFixture(t, r.remotePath, cloneDir, defaultSession, "testowner", "testrepo")

	tool, err := NewRepoPullTool(ctx, pullConfigs(cloneDir))
	require.NoError(t, err)

	repoPath := testRepoPath(cloneDir)
	before := cloneHead(t, repoPath)

	result, err := tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "branch": "main", "dryRun": true}`)
	require.NoError(t, err)
	assert.Contains(t, result, `"dryRun":true`)
	assert.Contains(t, result, filepath.Join(cloneDir, "default", "testowner", "testrepo"))

	// No mutation: HEAD unchanged and the remote commit is not applied.
	assert.Equal(t, before, cloneHead(t, repoPath))
	r.pushCommit(t, "second", "v2\n")
	data, readErr := os.ReadFile(filepath.Join(repoPath, "README.md"))
	require.NoError(t, readErr)
	assert.Equal(t, "v1\n", string(data))
}

func TestRepoPullDetachedHead(t *testing.T) {
	ctx := context.Background()
	r := newPullRemote(t)
	cloneDir := t.TempDir()
	clonePullFixture(t, r.remotePath, cloneDir, defaultSession, "testowner", "testrepo")

	tool, err := NewRepoPullTool(ctx, pullConfigs(cloneDir))
	require.NoError(t, err)

	repoPath := testRepoPath(cloneDir)
	repo, err := git.PlainOpen(repoPath)
	require.NoError(t, err)
	head, err := repo.Head()
	require.NoError(t, err)
	wt, err := repo.Worktree()
	require.NoError(t, err)
	require.NoError(t, wt.Checkout(&git.CheckoutOptions{Hash: head.Hash()}))

	// Detached HEAD without an explicit branch is an error.
	_, err = tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo"}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "detached")

	// With an explicit branch the pull succeeds.
	result, err := tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "branch": "main"}`)
	require.NoError(t, err)
	var out repoPullResult
	require.NoError(t, json.Unmarshal([]byte(result), &out))
	assert.True(t, out.Pulled)
	assert.True(t, out.AlreadyUpToDate)
}

func TestRepoPullSessionNamespacing(t *testing.T) {
	// clonePath always places the clone under <CloneDir>/<session>/<owner>/<repo>.
	// An empty session falls back to the literal "default" segment; a non-empty
	// session id is hashed via fileutil.SessionDirName (item 4) so distinct ids
	// never collide after sanitising.
	assert.Equal(t, "/root/default/o/r", clonePath("/root", "", "o", "r"))
	assert.Equal(t, "/root/"+fileutil.SessionDirName("s1")+"/o/r", clonePath("/root", "s1", "o", "r"))

	// A traversal-ish id hashes to a fixed hex segment: no separator and no "..".
	seg := clonePath("/root", "sess/../x", "o", "r")
	assert.NotContains(t, seg, "..")
	assert.NotContains(t, seg, "sess")
	assert.Contains(t, seg, fileutil.SessionDirName("sess/../x"))

	// A plain context.Background() has no adk run session, so SessionFromContext
	// returns "" and clonePath falls back to the "default" namespace (asserted
	// above) — the same limitation as components/agent/memory tests.
	assert.Equal(t, "", fileutil.SessionFromContext(context.Background(), CloneSessionKey))
}

func TestRepoPullValidation(t *testing.T) {
	ctx := context.Background()
	tool, err := NewRepoPullTool(ctx, pullConfigs(t.TempDir()))
	require.NoError(t, err)

	for _, args := range []string{
		`{"owner": "testowner", "repo": "testrepo"}`,                  // missing instance
		`{"instance": "test", "repo": "testrepo"}`,                    // missing owner
		`{"instance": "test", "owner": "testowner"}`,                  // missing repo
		`{"instance": "invalid-instance", "owner": "o", "repo": "r"}`, // unknown instance
	} {
		_, err := tool.InvokableRun(ctx, args)
		require.Error(t, err, "args: %s", args)
	}
}
