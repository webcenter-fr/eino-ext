package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	approvalserver "github.com/webcenter-fr/eino-ext/server/approval"
)

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
