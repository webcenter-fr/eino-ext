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
	})
	id, exp, err := p.Verify(context.Background(), token, nil)
	require.NoError(t, err)
	assert.Equal(t, "alice", id.User)
	assert.Equal(t, []string{"dev", "ops"}, id.Groups)
	assert.False(t, exp.IsZero(), "expiration comes from the exp claim")
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
			_, _, err := p.Verify(context.Background(), idp.sign(t, tt.claims), nil)
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
	_, _, err = p.Verify(context.Background(), signed, nil)
	assert.ErrorIs(t, err, auth.ErrInvalidToken)

	// Garbage token.
	_, _, err = p.Verify(context.Background(), "not-a-jwt", nil)
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
	id, _, err := p.Verify(context.Background(), idp.sign(t, jwt.MapClaims{
		"sub": "u", "preferred_username": "alice",
	}), nil)
	require.NoError(t, err)
	assert.Equal(t, "alice", id.User)
	assert.Empty(t, id.Groups)

	// Missing preferred_username → falls back to sub.
	id, _, err = p.Verify(context.Background(), idp.sign(t, jwt.MapClaims{"sub": "user-42"}), nil)
	require.NoError(t, err)
	assert.Equal(t, "user-42", id.User)

	// Custom UserClaim/GroupsClaim are honored.
	p2, err := NewOIDCProvider(context.Background(), &OIDCProviderConfig{
		Issuer:      idp.issuer(),
		Audience:    "mcp-server",
		UserClaim:   "email",
		GroupsClaim: "roles",
	})
	require.NoError(t, err)
	id, _, err = p2.Verify(context.Background(), idp.sign(t, jwt.MapClaims{
		"sub": "u", "email": "alice@example.com", "roles": []string{"admin"},
	}), nil)
	require.NoError(t, err)
	assert.Equal(t, "alice@example.com", id.User)
	assert.Equal(t, []string{"admin"}, id.Groups)
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

	id, _, err := p.Verify(context.Background(), idp.sign(t, jwt.MapClaims{
		"sub": "u", "preferred_username": "alice",
	}), nil)
	require.NoError(t, err)
	assert.Equal(t, "alice", id.User)
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
