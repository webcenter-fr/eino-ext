package mcp

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocalProviderVerify(t *testing.T) {
	p, err := NewLocalProvider(&LocalProviderConfig{Tokens: []LocalToken{
		{Token: "secret-1", User: "alice", Groups: []string{"dev", "ops"}, Scopes: []string{"mcp"}},
		{Token: "secret-2", User: "bob"},
	}})
	require.NoError(t, err)

	v, err := p.Verify(context.Background(), "secret-1", nil)
	require.NoError(t, err)
	assert.Equal(t, "alice", v.Identity.User)
	assert.Equal(t, []string{"dev", "ops"}, v.Identity.Groups)
	assert.Equal(t, []string{"mcp"}, v.Scopes)
	assert.True(t, v.ExpiresAt.IsZero(), "local tokens never expire")

	v, err = p.Verify(context.Background(), "secret-2", nil)
	require.NoError(t, err)
	assert.Equal(t, "bob", v.Identity.User)
	assert.Empty(t, v.Identity.Groups)
	assert.Empty(t, v.Scopes)

	_, err = p.Verify(context.Background(), "wrong-token", nil)
	assert.ErrorIs(t, err, auth.ErrInvalidToken)

	_, err = p.Verify(context.Background(), "", nil)
	assert.ErrorIs(t, err, auth.ErrInvalidToken)

	// A token that is a strict prefix/suffix of a configured token must not
	// match (the digest comparison is exact).
	_, err = p.Verify(context.Background(), "secret-", nil)
	assert.ErrorIs(t, err, auth.ErrInvalidToken)
	_, err = p.Verify(context.Background(), "secret-11", nil)
	assert.ErrorIs(t, err, auth.ErrInvalidToken)
}

func TestNewLocalProviderValidation(t *testing.T) {
	_, err := NewLocalProvider(nil)
	assert.Error(t, err)
	_, err = NewLocalProvider(&LocalProviderConfig{})
	assert.Error(t, err)
	_, err = NewLocalProvider(&LocalProviderConfig{Tokens: []LocalToken{{Token: "", User: "alice"}}})
	assert.Error(t, err)
	_, err = NewLocalProvider(&LocalProviderConfig{Tokens: []LocalToken{{Token: "t", User: ""}}})
	assert.Error(t, err)
}
