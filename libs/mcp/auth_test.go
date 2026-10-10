package mcp

import (
	"context"
	"net/http"
	"testing"
	"time"

	emperrors "emperror.dev/errors"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubProvider is a Provider returning fixed values.
type stubProvider struct {
	ver *Verification
	err error
}

func (p stubProvider) Verify(context.Context, string, *http.Request) (*Verification, error) {
	return p.ver, p.err
}

func TestTokenVerifierFirstSuccessWins(t *testing.T) {
	v := tokenVerifier([]Provider{
		stubProvider{err: emperrors.Wrap(auth.ErrInvalidToken, "nope")},
		stubProvider{ver: &Verification{
			Identity: &Identity{User: "alice", Groups: []string{"dev"}},
			Scopes:   []string{"mcp", "read"},
		}},
		stubProvider{ver: &Verification{Identity: &Identity{User: "mallory"}}},
	})
	ti, err := v(context.Background(), "token", nil)
	require.NoError(t, err)
	assert.Equal(t, "alice", ti.UserID)
	assert.Equal(t, []string{"dev"}, ti.Extra["groups"])
	assert.Equal(t, []string{"mcp", "read"}, ti.Scopes)
	// Local tokens never expire: the zero time is mapped to neverExpires so the
	// SDK's RequireBearerToken middleware accepts the token.
	assert.Equal(t, neverExpires, ti.Expiration)
}

func TestTokenVerifierAllFail(t *testing.T) {
	v := tokenVerifier([]Provider{
		stubProvider{err: emperrors.Wrap(auth.ErrInvalidToken, "nope")},
		stubProvider{err: emperrors.New("boom")},
	})
	_, err := v(context.Background(), "token", nil)
	assert.ErrorIs(t, err, auth.ErrInvalidToken)
}

func TestTokenVerifierExpiryPassthrough(t *testing.T) {
	exp := time.Now().Add(time.Hour).Truncate(time.Second)
	v := tokenVerifier([]Provider{stubProvider{ver: &Verification{
		Identity:  &Identity{User: "alice"},
		ExpiresAt: exp,
	}}})
	ti, err := v(context.Background(), "token", nil)
	require.NoError(t, err)
	assert.Equal(t, exp, ti.Expiration)
}

func TestNewProviders(t *testing.T) {
	providers, err := newProviders(context.Background(), &AuthConfig{
		Providers: []ProviderConfig{{Type: "local", Local: &LocalProviderConfig{
			Tokens: []LocalToken{{Token: "t", User: "alice"}},
		}}},
	})
	require.NoError(t, err)
	assert.Len(t, providers, 1)

	_, err = newProviders(context.Background(), &AuthConfig{
		Providers: []ProviderConfig{{Type: "local"}},
	})
	assert.Error(t, err)

	_, err = newProviders(context.Background(), &AuthConfig{
		Providers: []ProviderConfig{{Type: "oidc"}},
	})
	assert.Error(t, err)

	_, err = newProviders(context.Background(), &AuthConfig{
		Providers: []ProviderConfig{{Type: "saml"}},
	})
	assert.Error(t, err)
}
