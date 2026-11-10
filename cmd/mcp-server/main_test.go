package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/webcenter-fr/eino-ext/libs/mcp/serverconfig"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "token")
	require.NoError(t, os.WriteFile(tokenFile, []byte("token\n"), 0o600))
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(fmt.Sprintf(content, tokenFile)), 0o600))
	return path
}

func freeAddr(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := listener.Addr().String()
	require.NoError(t, listener.Close())
	return addr
}

func TestRunUnknownFamily(t *testing.T) {
	err := run(context.Background(), []string{"mcp-server", "--server", "nomad"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "server")
}

func TestRunStdioStartsNoListener(t *testing.T) {
	path := writeConfig(t, "server: argocd\nargocd:\n  instances:\n    prod:\n      url: https://argocd.example.com\n      tokenFile: %s\n")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// An invalid ops address would fail if stdio started an HTTP listener.
	err := run(ctx, []string{
		"mcp-server", "--config", path, "--server", "argocd",
		"--transport", "stdio", "--ops-addr", "not-an-address",
	})
	require.ErrorIs(t, err, context.Canceled)
}

func TestServeHTTPOpsAndShutdown(t *testing.T) {
	path := writeConfig(t, "server: argocd\nargocd:\n  instances:\n    prod:\n      url: https://argocd.example.com\n      tokenFile: %s\n")
	cfg, err := serverconfig.Load(path, map[string]any{
		"transport": "http",
		"addr":      freeAddr(t),
		"opsAddr":   freeAddr(t),
	})
	require.NoError(t, err)
	srv, err := serverconfig.Build(context.Background(), cfg)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- serveHTTP(ctx, srv, cfg) }()

	baseURL := "http://" + cfg.OpsAddr
	require.Eventually(t, func() bool {
		resp, err := http.Get(baseURL + "/healthz")
		if err != nil {
			return false
		}
		defer func() { _ = resp.Body.Close() }()
		return resp.StatusCode == http.StatusOK
	}, 5*time.Second, 50*time.Millisecond)

	resp, err := http.Get(baseURL + "/readyz")
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	cancel()
	select {
	case err := <-errCh:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("serveHTTP did not shut down")
	}
}
