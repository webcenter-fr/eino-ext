package dagger

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDaggerWorkspaceE2E exercises a real Dagger engine. It is skipped by
// default; set EINO_DAGGER_E2E=1 and provide a reachable engine to run it.
func TestDaggerWorkspaceE2E(t *testing.T) {
	if os.Getenv("EINO_DAGGER_E2E") == "" {
		t.Skip("set EINO_DAGGER_E2E=1 and provide a reachable Dagger engine to run this test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	client, err := NewClient(ctx, &EngineConfig{})
	require.NoError(t, err)
	defer func() { _ = client.Close() }()

	hostDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(hostDir, ".git"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(hostDir, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644))

	cont, err := client.Container(ctx, "alpine:3.20", WithWorkspace(hostDir))
	require.NoError(t, err)

	cont = cont.WithExec([]string{"sh", "-c", "echo hello > /workspace/hello.txt"})
	_, err = cont.Sync(ctx)
	require.NoError(t, err)

	require.NoError(t, client.ExportWorkspace(ctx, cont, hostDir))

	data, err := os.ReadFile(filepath.Join(hostDir, "hello.txt"))
	require.NoError(t, err)
	assert.Equal(t, "hello\n", string(data))

	data, err = os.ReadFile(filepath.Join(hostDir, ".git", "HEAD"))
	require.NoError(t, err)
	assert.Equal(t, "ref: refs/heads/main\n", string(data), ".git must not be exported back")
}
