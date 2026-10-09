package mcp

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/safety"
)

func TestNewServerDefaults(t *testing.T) {
	s, err := NewServer(context.Background(), &Config{InstanceParam: "cluster"})
	require.NoError(t, err)
	assert.Equal(t, "eino-ext-mcp", s.cfg.Name)
	assert.Equal(t, "0.0.0", s.cfg.Version)
	assert.IsType(t, &safety.LogSink{}, s.cfg.AuditSink)
	assert.Equal(t, 5*time.Minute, s.approvalTimeout)
}

func TestNewServerApprovalTimeout(t *testing.T) {
	s, err := NewServer(context.Background(), &Config{
		InstanceParam: "cluster",
		Approval:      &ApprovalConfig{Timeout: "1m30s"},
	})
	require.NoError(t, err)
	assert.Equal(t, 90*time.Second, s.approvalTimeout)
}

func TestNewServerValidation(t *testing.T) {
	tests := []struct {
		name string
		cfg  *Config
	}{
		{"nil config (instance param required)", nil},
		{"missing instance param", &Config{}},
		{"unknown provider type", &Config{
			InstanceParam: "cluster",
			Auth:          &AuthConfig{Providers: []ProviderConfig{{Type: "saml"}}},
		}},
		{"local provider without tokens", &Config{
			InstanceParam: "cluster",
			Auth:          &AuthConfig{Providers: []ProviderConfig{{Type: "local", Local: &LocalProviderConfig{}}}},
		}},
		{"bad operation value", &Config{
			InstanceParam: "cluster",
			Authorization: &AuthzConfig{Rules: []AccessRule{{Operations: []string{"admin"}}}},
		}},
		{"authz without rules", &Config{
			InstanceParam: "cluster",
			Authorization: &AuthzConfig{},
		}},
		{"oidc provider missing issuer", &Config{
			InstanceParam: "cluster",
			Auth: &AuthConfig{Providers: []ProviderConfig{{
				Type: "oidc",
				OIDC: &OIDCProviderConfig{Audience: "mcp-server"},
			}}},
		}},
		{"oidc provider missing audience", &Config{
			InstanceParam: "cluster",
			Auth: &AuthConfig{Providers: []ProviderConfig{{
				Type: "oidc",
				OIDC: &OIDCProviderConfig{Issuer: "https://idp.example.com"},
			}}},
		}},
		{"invalid approval timeout", &Config{
			InstanceParam: "cluster",
			Approval:      &ApprovalConfig{Timeout: "not-a-duration"},
		}},
		{"zero approval timeout (would disable the approval deadline)", &Config{
			InstanceParam: "cluster",
			Approval:      &ApprovalConfig{Timeout: "0s"},
		}},
		{"negative approval timeout (would disable the approval deadline)", &Config{
			InstanceParam: "cluster",
			Approval:      &ApprovalConfig{Timeout: "-5m"},
		}},
		{"local token with empty scope", &Config{
			InstanceParam: "cluster",
			Auth: &AuthConfig{Providers: []ProviderConfig{{Type: "local", Local: &LocalProviderConfig{
				Tokens: []LocalToken{{Token: "t", User: "alice", Scopes: []string{""}}},
			}}}},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewServer(context.Background(), tt.cfg)
			assert.Error(t, err)
		})
	}
}
