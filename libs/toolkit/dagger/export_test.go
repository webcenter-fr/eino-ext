package dagger

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

func TestSyncWorkspaceCopiesNonGitAndPreservesDotGit(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	writeTestFile(t, filepath.Join(src, "main.go"), "package main\n")
	writeTestFile(t, filepath.Join(src, "sub", "file.txt"), "hello\n")
	writeTestFile(t, filepath.Join(src, ".git", "config"), "from-container\n")

	writeTestFile(t, filepath.Join(dst, "stale.txt"), "stale\n")
	writeTestFile(t, filepath.Join(dst, ".git", "HEAD"), "ref: refs/heads/main\n")

	require.NoError(t, syncWorkspace(src, dst))

	data, err := os.ReadFile(filepath.Join(dst, "main.go"))
	require.NoError(t, err)
	assert.Equal(t, "package main\n", string(data))

	data, err = os.ReadFile(filepath.Join(dst, "sub", "file.txt"))
	require.NoError(t, err)
	assert.Equal(t, "hello\n", string(data))

	// A file deleted in the container is removed from the host.
	_, err = os.Stat(filepath.Join(dst, "stale.txt"))
	assert.True(t, os.IsNotExist(err), "stale host file must be removed")

	// .git is never exported from the container and never clobbered on the host.
	_, err = os.Stat(filepath.Join(dst, ".git", "config"))
	assert.True(t, os.IsNotExist(err), "container .git must not be exported")

	data, err = os.ReadFile(filepath.Join(dst, ".git", "HEAD"))
	require.NoError(t, err)
	assert.Equal(t, "ref: refs/heads/main\n", string(data))
}

func TestSyncWorkspaceCreatesDestination(t *testing.T) {
	src := t.TempDir()
	writeTestFile(t, filepath.Join(src, "file.txt"), "content\n")

	dst := filepath.Join(t.TempDir(), "new", "workspace")
	require.NoError(t, syncWorkspace(src, dst))

	data, err := os.ReadFile(filepath.Join(dst, "file.txt"))
	require.NoError(t, err)
	assert.Equal(t, "content\n", string(data))
}

func TestSyncWorkspaceRemovesNestedDirectories(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeTestFile(t, filepath.Join(dst, "old", "nested", "file.txt"), "old\n")
	writeTestFile(t, filepath.Join(dst, "keep.txt"), "keep\n")
	writeTestFile(t, filepath.Join(src, "keep.txt"), "keep\n")

	require.NoError(t, syncWorkspace(src, dst))

	_, err := os.Stat(filepath.Join(dst, "old"))
	assert.True(t, os.IsNotExist(err), "directory absent from the export must be removed")

	_, err = os.Stat(filepath.Join(dst, "keep.txt"))
	assert.NoError(t, err)
}
