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
		{Token: "secret-1", User: "alice", Groups: []string{"dev", "ops"}},
		{Token: "secret-2", User: "bob"},
	}})
	require.NoError(t, err)

	id, exp, err := p.Verify(context.Background(), "secret-1", nil)
	require.NoError(t, err)
	assert.Equal(t, "alice", id.User)
	assert.Equal(t, []string{"dev", "ops"}, id.Groups)
	assert.True(t, exp.IsZero(), "local tokens never expire")

	id, _, err = p.Verify(context.Background(), "secret-2", nil)
	require.NoError(t, err)
	assert.Equal(t, "bob", id.User)
	assert.Empty(t, id.Groups)

	_, _, err = p.Verify(context.Background(), "wrong-token", nil)
	assert.ErrorIs(t, err, auth.ErrInvalidToken)

	_, _, err = p.Verify(context.Background(), "", nil)
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
