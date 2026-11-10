package serverconfig

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/secretref"
)

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	return writeFile(t, t.TempDir(), "config.yaml", content)
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load("", map[string]any{
		"server": "kubernetes",
		"kubernetes.clusters.in-cluster.inCluster": true,
	})
	require.NoError(t, err)
	assert.Equal(t, DefaultTransport, cfg.Transport)
	assert.Equal(t, DefaultAddr, cfg.Addr)
	assert.Equal(t, DefaultOpsAddr, cfg.OpsAddr)
	assert.Equal(t, DefaultLogLevel, cfg.Log.Level)
	assert.Equal(t, DefaultLogFormat, cfg.Log.Format)
	require.Contains(t, cfg.Kubernetes.Clusters, "in-cluster")
	assert.Equal(t, DefaultDisallowedNamespaces, cfg.Kubernetes.Clusters["in-cluster"].DisallowedNamespaces)
	assert.Equal(t, DefaultTimeout, cfg.Kubernetes.Clusters["in-cluster"].DefaultTimeout)
}

func TestLoadPrecedence(t *testing.T) {
	path := writeConfig(t, "server: kubernetes\naddr: \":1111\"\nkubernetes:\n  clusters:\n    in-cluster:\n      inCluster: true\n")

	t.Setenv("EINO_MCP_ADDR", ":2222")
	cfg, err := Load(path, nil)
	require.NoError(t, err)
	assert.Equal(t, ":2222", cfg.Addr, "environment must override the file")

	cfg, err = Load(path, map[string]any{"addr": ":3333"})
	require.NoError(t, err)
	assert.Equal(t, ":3333", cfg.Addr, "flags must override the environment")
}

func TestLoadSecretFile(t *testing.T) {
	dir := t.TempDir()
	tokenFile := writeFile(t, dir, "token", "s3cr3t\r\n")
	path := writeConfig(t, "server: argocd\nargocd:\n  instances:\n    prod:\n      url: https://argocd.example.com\n      tokenFile: "+tokenFile+"\n")

	cfg, err := Load(path, nil)
	require.NoError(t, err)
	assert.Equal(t, "s3cr3t", cfg.ArgoCD.Instances["prod"].Token)
}

func TestLoadSecretFileWinsOverEnv(t *testing.T) {
	dir := t.TempDir()
	tokenFile := writeFile(t, dir, "token", "from-file\n")
	path := writeConfig(t, "server: grafana\ngrafana:\n  instances:\n    prod:\n      url: https://grafana.example.com\n      tokenFile: "+tokenFile+"\n      tokenEnv: TEST_GRAFANA_TOKEN\n")
	t.Setenv("TEST_GRAFANA_TOKEN", "from-env")

	cfg, err := Load(path, nil)
	require.NoError(t, err)
	assert.Equal(t, "from-file", cfg.Grafana.Instances["prod"].Token)
}

func TestLoadSecretErrors(t *testing.T) {
	dir := t.TempDir()
	empty := writeFile(t, dir, "empty", "\n")
	oversize := writeFile(t, dir, "oversize", strings.Repeat("x", secretref.MaxBytes+1))

	tests := []struct {
		name    string
		token   string
		wantErr string
	}{
		{name: "missing file", token: filepath.Join(dir, "missing"), wantErr: "failed to read"},
		{name: "empty file", token: empty, wantErr: "is empty"},
		{name: "oversize file", token: oversize, wantErr: "exceeds"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeConfig(t, "server: argocd\nargocd:\n  instances:\n    prod:\n      url: https://argocd.example.com\n      tokenFile: "+tt.token+"\n")
			_, err := Load(path, nil)
			require.Error(t, err)
			assert.Contains(t, strings.ToLower(err.Error()), strings.ToLower(tt.wantErr))
		})
	}
}

func TestLoadUnknownKey(t *testing.T) {
	path := writeConfig(t, "server: kubernetes\nunknownKey: true\nkubernetes:\n  clusters:\n    in-cluster:\n      inCluster: true\n")
	_, err := Load(path, nil)
	require.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "unknownkey")
}

func TestLoadValidation(t *testing.T) {
	t.Setenv("TOKEN", "x")
	tests := []struct {
		name    string
		content string
		wantErr string
	}{
		{name: "missing server", content: "transport: http\n", wantErr: "server"},
		{name: "unknown server", content: "server: nomad\n", wantErr: "server"},
		{name: "invalid transport", content: "server: kubernetes\ntransport: grpc\n", wantErr: "transport"},
		{name: "invalid url", content: "server: argocd\nargocd:\n  instances:\n    prod:\n      url: not-a-url\n      tokenEnv: TOKEN\n", wantErr: "url"},
		{name: "zero instances", content: "server: argocd\nargocd:\n  instances: {}\n", wantErr: "instance"},
		{name: "missing token", content: "server: argocd\nargocd:\n  instances:\n    prod:\n      url: https://argocd.example.com\n", wantErr: "token"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, tt.content), nil)
			require.Error(t, err)
			assert.Contains(t, strings.ToLower(err.Error()), strings.ToLower(tt.wantErr))
		})
	}
}

func TestLoadMissingConfigFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "missing.yaml"), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing.yaml")
}

func TestBuildFamilies(t *testing.T) {
	dir := t.TempDir()
	tokenFile := writeFile(t, dir, "token", "token\n")
	kubeconfig := writeFile(t, dir, "kubeconfig", `apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://127.0.0.1:6443
  name: test
contexts:
- context:
    cluster: test
    user: test
  name: test
current-context: test
users:
- name: test
  user:
    token: dummy
`)

	tests := []struct {
		name    string
		content string
	}{
		{name: "kubernetes", content: "server: kubernetes\nkubernetes:\n  clusters:\n    prod:\n      kubeconfigFile: " + kubeconfig + "\n      context: test\n"},
		{name: "argocd", content: "server: argocd\nargocd:\n  instances:\n    prod:\n      url: https://argocd.example.com\n      tokenFile: " + tokenFile + "\n"},
		{name: "prometheus", content: "server: prometheus\nprometheus:\n  instances:\n    prod:\n      url: https://prometheus.example.com\n      tokenFile: " + tokenFile + "\n"},
		{name: "grafana", content: "server: grafana\ngrafana:\n  instances:\n    prod:\n      url: https://grafana.example.com\n      tokenFile: " + tokenFile + "\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Load(writeConfig(t, tt.content), nil)
			require.NoError(t, err)
			srv, err := Build(context.Background(), cfg)
			require.NoError(t, err)
			assert.NotNil(t, srv)
		})
	}
}

func TestBuildUnknownFamily(t *testing.T) {
	_, err := Build(context.Background(), &Config{Server: "nomad"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown MCP server family")
}

func TestBuildMissingFamily(t *testing.T) {
	_, err := Build(context.Background(), &Config{Server: "argocd"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not configured")
}

func TestOpsHandler(t *testing.T) {
	readyErr := assert.AnError
	handler := OpsHandler(func() error { return readyErr })

	check := func(path string, want int) {
		t.Helper()
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		assert.Equal(t, want, rec.Code, path)
	}

	check("/healthz", http.StatusOK)
	check("/readyz", http.StatusServiceUnavailable)
	check("/metrics", http.StatusOK)

	readyErr = nil
	check("/readyz", http.StatusOK)
}

func TestLoadEnvOnlyServer(t *testing.T) {
	t.Setenv("EINO_MCP_SERVER", "argocd")
	t.Setenv("TEST_ARGOCD_TOKEN", "token")
	path := writeConfig(t, "argocd:\n  instances:\n    prod:\n      url: https://argocd.example.com\n      tokenEnv: TEST_ARGOCD_TOKEN\n")

	cfg, err := Load(path, nil)
	require.NoError(t, err)
	assert.Equal(t, "argocd", cfg.Server)
}
