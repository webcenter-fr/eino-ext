package dagger

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dagger.io/dagger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/egress"
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

// TestDaggerEgressE2E exercises the host-tunnel egress proxy against a real
// Dagger engine. It is skipped by default; set EINO_DAGGER_E2E=1 and provide a
// reachable engine to run it.
func TestDaggerEgressE2E(t *testing.T) {
	if os.Getenv("EINO_DAGGER_E2E") == "" {
		t.Skip("set EINO_DAGGER_E2E=1 and provide a reachable Dagger engine to run this test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	client, err := NewClient(ctx, &EngineConfig{})
	require.NoError(t, err)
	defer func() { _ = client.Close() }()

	// dl-cdn.alpinelinux.org is required to install curl through the proxy.
	pol := &egress.Policy{
		DefaultDeny: true,
		AllowHosts:  []string{"example.com", "dl-cdn.alpinelinux.org"},
	}
	cont, err := client.Container(ctx, "alpine:3.20", WithEgressPolicy(pol))
	require.NoError(t, err)

	cont = cont.WithExec([]string{"apk", "add", "--no-cache", "curl"})

	run := func(command string) string {
		out, err := cont.WithExec(
			[]string{"sh", "-c", command},
			dagger.ContainerWithExecOpts{Expect: dagger.ReturnTypeAny},
		).Stdout(ctx)
		require.NoError(t, err)
		return strings.TrimSpace(out)
	}

	assert.Equal(t, "1", run("env | grep -c HTTP_PROXY"))
	assert.Contains(t, run("getent hosts eino-egress"), "eino-egress")

	assert.Equal(t, "200", run("curl -s -o /dev/null -w '%{http_code}' https://example.com/"))
	assert.Equal(t, "200", run("curl -s -o /dev/null -w '%{http_code}' http://example.com/"))
	assert.NotEqual(t, "200", run("curl -s -o /dev/null -w '%{http_code}' https://github.com/"))

	// F6: raw TCP bypasses the proxy. This is a soft control; the command below
	// succeeds from inside a policy-bound container because nc ignores
	// HTTP_PROXY. Do not assert on it: it depends on real outbound connectivity.
	//   nc -z -w 5 github.com 443
}
