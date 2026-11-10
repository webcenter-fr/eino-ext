package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"

	approvalserver "github.com/webcenter-fr/eino-ext/server/approval"
)

func runCommand(t *testing.T, action cli.ActionFunc, args ...string) error {
	t.Helper()
	return newCommand(action).Run(context.Background(), append([]string{"approval"}, args...))
}

func loadOptionsForTest(t *testing.T, args ...string) (*options, error) {
	t.Helper()
	var opts *options
	err := runCommand(t, func(_ context.Context, cmd *cli.Command) error {
		var loadErr error
		opts, loadErr = loadOptions(cmd)
		return loadErr
	}, args...)
	return opts, err
}

func loadFileConfigForTest(t *testing.T, args ...string) (*viper.Viper, *fileConfig, error) {
	t.Helper()
	var v *viper.Viper
	var cfg *fileConfig
	err := runCommand(t, func(_ context.Context, cmd *cli.Command) error {
		var loadErr error
		v, cfg, loadErr = loadFileConfig(cmd)
		return loadErr
	}, args...)
	return v, cfg, err
}

func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func TestParseCredentials(t *testing.T) {
	creds, err := parseCredentials([]string{
		"grafana=secret@prod,staging",
		"kubernetes=sha256:" + approvalserver.HashServerToken("k8s"),
	})
	require.NoError(t, err)
	require.Len(t, creds, 2)
	assert.Equal(t, "grafana", creds[0].ServerName)
	assert.Equal(t, approvalserver.HashServerToken("secret"), creds[0].TokenHash)
	assert.Equal(t, []string{"prod", "staging"}, creds[0].AllowedInstances)
	assert.Equal(t, approvalserver.HashServerToken("k8s"), creds[1].TokenHash)
}

func TestParseCredentialsErrors(t *testing.T) {
	_, err := parseCredentials(nil)
	assert.Error(t, err)
	_, err = parseCredentials([]string{"missing-equals"})
	assert.Error(t, err)
}

func TestDefaultBaseURL(t *testing.T) {
	assert.Equal(t, "http://localhost:8080", defaultBaseURL(":8080"))
	assert.Equal(t, "http://127.0.0.1:8080", defaultBaseURL("127.0.0.1:8080"))
}

func TestLoadOptionsPrecedence(t *testing.T) {
	path := writeTemp(t, "config.yaml", "addr: \":1111\"\n")

	require.NoError(t, os.Unsetenv("EINO_APPROVAL_ADDR"))
	opts, err := loadOptionsForTest(t, "--config", path)
	require.NoError(t, err)
	assert.Equal(t, ":1111", opts.addr, "file overrides defaults")

	t.Setenv("EINO_APPROVAL_ADDR", ":2222")
	opts, err = loadOptionsForTest(t, "--config", path)
	require.NoError(t, err)
	assert.Equal(t, ":2222", opts.addr, "environment overrides the file")

	opts, err = loadOptionsForTest(t, "--config", path, "--addr", ":3333")
	require.NoError(t, err)
	assert.Equal(t, ":3333", opts.addr, "flags override the environment")
}

func TestLoadOptionsSingleDashFlag(t *testing.T) {
	opts, err := loadOptionsForTest(t, "-addr", ":4444")
	require.NoError(t, err)
	assert.Equal(t, ":4444", opts.addr)
}

func TestLoadOptionsSecretPrecedence(t *testing.T) {
	secretFile := writeTemp(t, "receipt-secret", "from-file\n")
	configFile := writeTemp(t, "config.yaml", "baseURL: https://approve.example.com\nreceiptSecretFile: "+secretFile+"\n")

	t.Setenv("EINO_APPROVAL_RECEIPT_SECRET", "from-env")
	opts, err := loadOptionsForTest(t, "--config", configFile, "--receipt-secret", "from-flag")
	require.NoError(t, err)
	assert.Equal(t, "from-file", opts.receiptSecret, "file wins over env and flag")

	configFile = writeTemp(t, "config.yaml", "baseURL: https://approve.example.com\n")
	opts, err = loadOptionsForTest(t, "--config", configFile, "--receipt-secret", "from-flag")
	require.NoError(t, err)
	assert.Equal(t, "from-env", opts.receiptSecret, "env wins over flag")

	require.NoError(t, os.Unsetenv("EINO_APPROVAL_RECEIPT_SECRET"))
	opts, err = loadOptionsForTest(t, "--receipt-secret", "from-flag")
	require.NoError(t, err)
	assert.Equal(t, "from-flag", opts.receiptSecret)
}

func TestLoadOptionsSecretFileTrimming(t *testing.T) {
	secretFile := writeTemp(t, "oidc-secret", "s3cr3t\r\n")
	opts, err := loadOptionsForTest(t, "--oidc-client-secret-file", secretFile)
	require.NoError(t, err)
	assert.Equal(t, "s3cr3t", opts.oidcClientSecret)
}

func TestReadCredentialFile(t *testing.T) {
	path := writeTemp(t, "credentials", "# comment\r\n\r\n  grafana=secret@prod,staging  \r\nkubernetes=sha256:abc\n")
	lines, err := readCredentialFile(path)
	require.NoError(t, err)
	assert.Equal(t, []string{"grafana=secret@prod,staging", "kubernetes=sha256:abc"}, lines)

	creds, err := parseCredentials(lines)
	require.NoError(t, err)
	require.Len(t, creds, 2)
	assert.Equal(t, []string{"prod", "staging"}, creds[0].AllowedInstances)
}

func TestLoadOptionsCredentialFile(t *testing.T) {
	credentialFile := writeTemp(t, "credentials", "grafana=secret@prod\n")
	opts, err := loadOptionsForTest(t, "--server-credential-file", credentialFile)
	require.NoError(t, err)
	assert.Equal(t, []string{"grafana=secret@prod"}, opts.credentials)

	opts, err = loadOptionsForTest(t, "--server-credential", "a=one@x,y", "--server-credential", "b=two")
	require.NoError(t, err)
	assert.Equal(t, []string{"a=one@x,y", "b=two"}, opts.credentials)
}

func TestNoSecretInViperSettings(t *testing.T) {
	v, _, err := loadFileConfigForTest(t,
		"--receipt-secret", "receipt-literal",
		"--oidc-client-secret", "oidc-literal",
		"--postgres-dsn", "postgres-literal",
	)
	require.NoError(t, err)
	settings := strings.ToLower(fmt.Sprintf("%v", v.AllSettings()))
	for _, secret := range []string{"receipt-literal", "oidc-literal", "postgres-literal"} {
		assert.NotContains(t, settings, secret)
	}
}

func TestLoadOptionsUnknownKey(t *testing.T) {
	path := writeTemp(t, "config.yaml", "unknownKey: true\n")
	_, err := loadOptionsForTest(t, "--config", path)
	require.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "unknownkey")
}

func TestLoadOptionsInvalidURL(t *testing.T) {
	path := writeTemp(t, "config.yaml", "baseURL: not-a-url\n")
	_, err := loadOptionsForTest(t, "--config", path)
	require.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "url")
}

func TestLoadOptionsEnvOnly(t *testing.T) {
	t.Setenv("EINO_APPROVAL_BASEURL", "https://approve.example.com")
	opts, err := loadOptionsForTest(t)
	require.NoError(t, err)
	assert.Equal(t, "https://approve.example.com", opts.baseURL)
}

func TestLoadOptionsStoreValidation(t *testing.T) {
	path := writeTemp(t, "config.yaml", "store: opensearch\n")
	_, err := loadOptionsForTest(t, "--config", path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "openSearchURL")

	path = writeTemp(t, "config.yaml", "store: postgresql\n")
	_, err = loadOptionsForTest(t, "--config", path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "PostgreSQL DSN")
}
