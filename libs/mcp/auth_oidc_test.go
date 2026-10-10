package mcp

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOIDCProviderVerify(t *testing.T) {
	idp := newTestIDP(t)
	p, err := NewOIDCProvider(context.Background(), &OIDCProviderConfig{
		Issuer:   idp.issuer(),
		Audience: "mcp-server",
	})
	require.NoError(t, err)
	assert.Equal(t, int32(1), idp.discoveryHits.Load(), "discovery document fetched at construction")

	token := idp.sign(t, jwt.MapClaims{
		"sub":                "user-123",
		"preferred_username": "alice",
		"groups":             []string{"dev", "ops"},
		"scope":              "mcp read",
	})
	v, err := p.Verify(context.Background(), token, nil)
	require.NoError(t, err)
	assert.Equal(t, "alice", v.Identity.User)
	assert.Equal(t, []string{"dev", "ops"}, v.Identity.Groups)
	assert.Equal(t, []string{"mcp", "read"}, v.Scopes, "space-separated scope claim is split")
	assert.False(t, v.ExpiresAt.IsZero(), "expiration comes from the exp claim")
	assert.Equal(t, int32(1), idp.jwksHits.Load(), "JWKS fetched on first verification")
}

func TestOIDCProviderVerifyFailures(t *testing.T) {
	idp := newTestIDP(t)
	p, err := NewOIDCProvider(context.Background(), &OIDCProviderConfig{
		Issuer:   idp.issuer(),
		Audience: "mcp-server",
	})
	require.NoError(t, err)

	tests := []struct {
		name   string
		claims jwt.MapClaims
	}{
		{"wrong audience", jwt.MapClaims{"aud": "other-audience", "sub": "u"}},
		{"wrong issuer", jwt.MapClaims{"iss": "https://evil.example.com", "sub": "u"}},
		{"expired", jwt.MapClaims{"sub": "u", "exp": time.Now().Add(-time.Hour).Unix()}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := p.Verify(context.Background(), idp.sign(t, tt.claims), nil)
			assert.ErrorIs(t, err, auth.ErrInvalidToken)
		})
	}

	// Bad signature: signed with a different key but the same kid.
	otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": idp.issuer(),
		"aud": "mcp-server",
		"sub": "u",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	token.Header["kid"] = "test-key"
	signed, err := token.SignedString(otherKey)
	require.NoError(t, err)
	_, err = p.Verify(context.Background(), signed, nil)
	assert.ErrorIs(t, err, auth.ErrInvalidToken)

	// Garbage token.
	_, err = p.Verify(context.Background(), "not-a-jwt", nil)
	assert.ErrorIs(t, err, auth.ErrInvalidToken)
}

func TestOIDCProviderClaimsMapping(t *testing.T) {
	idp := newTestIDP(t)
	p, err := NewOIDCProvider(context.Background(), &OIDCProviderConfig{
		Issuer:   idp.issuer(),
		Audience: "mcp-server",
	})
	require.NoError(t, err)

	// Missing groups claim → empty groups.
	v, err := p.Verify(context.Background(), idp.sign(t, jwt.MapClaims{
		"sub": "u", "preferred_username": "alice",
	}), nil)
	require.NoError(t, err)
	assert.Equal(t, "alice", v.Identity.User)
	assert.Empty(t, v.Identity.Groups)
	assert.Empty(t, v.Scopes, "missing scope claim → no scopes")

	// Missing preferred_username → falls back to sub.
	v, err = p.Verify(context.Background(), idp.sign(t, jwt.MapClaims{"sub": "user-42"}), nil)
	require.NoError(t, err)
	assert.Equal(t, "user-42", v.Identity.User)

	// Custom UserClaim/GroupsClaim are honored.
	p2, err := NewOIDCProvider(context.Background(), &OIDCProviderConfig{
		Issuer:      idp.issuer(),
		Audience:    "mcp-server",
		UserClaim:   "email",
		GroupsClaim: "roles",
	})
	require.NoError(t, err)
	v, err = p2.Verify(context.Background(), idp.sign(t, jwt.MapClaims{
		"sub": "u", "email": "alice@example.com", "roles": []string{"admin"},
	}), nil)
	require.NoError(t, err)
	assert.Equal(t, "alice@example.com", v.Identity.User)
	assert.Equal(t, []string{"admin"}, v.Identity.Groups)
}

func TestOIDCProviderScopeClaimArray(t *testing.T) {
	// Some issuers send the scope claim as a JSON array instead of a
	// space-separated string.
	idp := newTestIDP(t)
	p, err := NewOIDCProvider(context.Background(), &OIDCProviderConfig{
		Issuer:   idp.issuer(),
		Audience: "mcp-server",
	})
	require.NoError(t, err)
	v, err := p.Verify(context.Background(), idp.sign(t, jwt.MapClaims{
		"sub": "u", "scope": []any{"mcp", "write"},
	}), nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"mcp", "write"}, v.Scopes)
}

func TestOIDCProviderJWKSURLOverride(t *testing.T) {
	idp := newTestIDP(t)
	// JWKSURL set: the discovery document is NOT fetched (fail fast only on
	// the JWKS endpoint).
	p, err := NewOIDCProvider(context.Background(), &OIDCProviderConfig{
		Issuer:   idp.issuer(),
		Audience: "mcp-server",
		JWKSURL:  idp.issuer() + "/keys",
	})
	require.NoError(t, err)
	assert.Equal(t, int32(0), idp.discoveryHits.Load())

	v, err := p.Verify(context.Background(), idp.sign(t, jwt.MapClaims{
		"sub": "u", "preferred_username": "alice",
	}), nil)
	require.NoError(t, err)
	assert.Equal(t, "alice", v.Identity.User)
	assert.Equal(t, int32(1), idp.jwksHits.Load())
}

func TestNewOIDCProviderFailures(t *testing.T) {
	_, err := NewOIDCProvider(context.Background(), nil)
	assert.Error(t, err)

	// Unreachable issuer: fail fast at construction.
	_, err = NewOIDCProvider(context.Background(), &OIDCProviderConfig{
		Issuer:   "http://127.0.0.1:1",
		Audience: "mcp-server",
	})
	assert.Error(t, err)
}
