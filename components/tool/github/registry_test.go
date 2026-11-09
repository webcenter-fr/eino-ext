package github

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/go-git/go-git/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	toolkitsafety "github.com/webcenter-fr/eino-ext/libs/toolkit/safety"
)

func registryTestConfigs(cloneDir, baseURL string) Configs {
	return Configs{
		"test": {
			Token:    "test-token",
			CloneDir: cloneDir,
			BaseURL:  baseURL,
		},
	}
}

func registryTestClone(t *testing.T) string {
	t.Helper()

	cloneDir := t.TempDir()
	repoPath := filepath.Join(cloneDir, "default", "testowner", "testrepo")
	require.NoError(t, os.MkdirAll(filepath.Join(repoPath, "sub"), 0o755))

	repo, err := git.PlainInit(repoPath, false)
	require.NoError(t, err)

	wt, err := repo.Worktree()
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(repoPath, "README.md"), []byte("line1\nline2\nline3\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(repoPath, "sub", "main.go"), []byte("package main\n"), 0o644))

	_, err = wt.Add("README.md")
	require.NoError(t, err)
	_, err = wt.Add("sub/main.go")
	require.NoError(t, err)

	_, err = wt.Commit("initial", &git.CommitOptions{Author: commitIdentity})
	require.NoError(t, err)

	return cloneDir
}

func TestNewReadOnlyToolsClassification(t *testing.T) {
	ctx := context.Background()
	tools, err := NewReadOnlyTools(ctx, registryTestConfigs(t.TempDir(), ""))
	require.NoError(t, err)
	require.Len(t, tools, 20)

	names := make([]string, 0, len(tools))
	for _, tl := range tools {
		info, err := tl.Info(ctx)
		require.NoError(t, err)
		names = append(names, info.Name)

		if !strings.HasPrefix(info.Name, "github_file_") {
			continue
		}
		schema, err := info.ToJSONSchema()
		require.NoError(t, err)
		require.NotNil(t, schema)
		require.NotNil(t, schema.Properties)
		_, hasConfirmed := schema.Properties.Get("confirmed")
		assert.False(t, hasConfirmed, "%s must not expose a confirmed property", info.Name)
	}

	assert.ElementsMatch(t, []string{
		"github_instance_list",
		"github_issue_list",
		"github_issue_get",
		"github_pr_list",
		"github_pr_get",
		"github_org_repo_list",
		"github_repo_search",
		"github_repo_clone",
		"github_repo_pull",
		"github_file_read",
		"github_file_search",
		"github_file_list",
		"github_file_write",
		"github_file_edit",
		"github_file_delete",
		"github_file_copy",
		"github_file_move",
		"github_branch_checkout",
		"github_status",
		"github_diff",
	}, names)
}

func TestWriteToolNamesAndExtractMatch(t *testing.T) {
	ctx := context.Background()
	configs := registryTestConfigs(t.TempDir(), "")

	extracted, err := ExtractWriteToolNames(ctx, configs)
	require.NoError(t, err)
	assert.Equal(t, WriteToolNames(), extracted)
	require.Len(t, WriteToolNames(), 12)

	for _, name := range []string{
		"github_file_read",
		"github_file_search",
		"github_file_list",
		"github_file_write",
		"github_file_edit",
		"github_file_delete",
		"github_file_copy",
		"github_file_move",
		"github_branch_checkout",
		"github_status",
		"github_diff",
		"github_repo_clone",
		"github_repo_pull",
	} {
		assert.NotContains(t, WriteToolNames(), name)
	}
}

func TestNewAllToolsCount(t *testing.T) {
	ctx := context.Background()
	tools, err := NewAllTools(ctx, registryTestConfigs(t.TempDir(), ""))
	require.NoError(t, err)
	require.Len(t, tools, 32)

	seen := make(map[string]bool, len(tools))
	for _, tl := range tools {
		info, err := tl.Info(ctx)
		require.NoError(t, err)
		assert.False(t, seen[info.Name], "duplicate tool name %q", info.Name)
		seen[info.Name] = true
	}
}

func TestFileToolsUngatedUnderSafetyMiddleware(t *testing.T) {
	ctx := context.Background()
	cloneDir := registryTestClone(t)
	repoPath := filepath.Join(cloneDir, "default", "testowner", "testrepo")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	tools, mw, err := NewAllToolsWithSafety(ctx, registryTestConfigs(cloneDir, server.URL+"/"), nil)
	require.NoError(t, err)

	byName := make(map[string]tool.InvokableTool, len(tools))
	for _, tl := range tools {
		info, err := tl.Info(ctx)
		require.NoError(t, err)
		byName[info.Name] = tl
	}

	invoke := func(name, args string) (string, error) {
		tl, ok := byName[name]
		require.True(t, ok, "tool %q not found", name)
		wrapped, err := mw.WrapInvokableToolCall(ctx, tl.InvokableRun, &adk.ToolContext{Name: name, CallID: "call-1"})
		require.NoError(t, err)
		return wrapped(ctx, args)
	}

	result, err := invoke("github_file_copy", `{"instance": "test", "owner": "testowner", "repo": "testrepo", "source": "README.md", "destination": "copy.md", "branch": "master"}`)
	require.NoError(t, err)
	assert.Contains(t, result, `"copied":true`)
	assert.FileExists(t, filepath.Join(repoPath, "copy.md"))

	result, err = invoke("github_file_move", `{"instance": "test", "owner": "testowner", "repo": "testrepo", "source": "copy.md", "destination": "moved.md", "branch": "master"}`)
	require.NoError(t, err)
	assert.Contains(t, result, `"moved":true`)
	assert.NoFileExists(t, filepath.Join(repoPath, "copy.md"))
	assert.FileExists(t, filepath.Join(repoPath, "moved.md"))

	result, err = invoke("github_file_delete", `{"instance": "test", "owner": "testowner", "repo": "testrepo", "path": "moved.md", "branch": "master"}`)
	require.NoError(t, err)
	assert.Contains(t, result, `"deleted":true`)
	assert.NoFileExists(t, filepath.Join(repoPath, "moved.md"))

	result, err = invoke("github_file_write", `{"instance": "test", "owner": "testowner", "repo": "testrepo", "path": "written.txt", "content": "hello"}`)
	require.NoError(t, err)
	assert.Contains(t, result, `"bytes":5`)
	data, readErr := os.ReadFile(filepath.Join(repoPath, "written.txt"))
	require.NoError(t, readErr)
	assert.Equal(t, "hello", string(data))

	result, err = invoke("github_file_read", `{"instance": "test", "owner": "testowner", "repo": "testrepo", "path": "README.md"}`)
	require.NoError(t, err)
	assert.Contains(t, result, "line1")

	result, err = invoke("github_file_list", `{"instance": "test", "owner": "testowner", "repo": "testrepo"}`)
	require.NoError(t, err)
	assert.Contains(t, result, "README.md")

	result, err = invoke("github_file_search", `{"instance": "test", "owner": "testowner", "repo": "testrepo", "pattern": "line1"}`)
	require.NoError(t, err)
	assert.Contains(t, result, "README.md")

	_, err = invoke("github_issue_create", `{"instance": "test", "owner": "testowner", "repo": "testrepo", "title": "title", "body": "body"}`)
	require.ErrorIs(t, err, toolkitsafety.ErrGateRequired)
}
