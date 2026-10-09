package mcp

import (
	"context"
	"crypto/subtle"
	"net/http"
	"time"

	emperrors "emperror.dev/errors"
	"github.com/modelcontextprotocol/go-sdk/auth"
)

// LocalProvider verifies static bearer tokens against a configured list.
// Verification iterates all tokens with a constant-time comparison so the
// lookup does not leak which tokens exist through timing.
type LocalProvider struct {
	tokens []LocalToken
}

// NewLocalProvider builds a LocalProvider from the config.
func NewLocalProvider(cfg *LocalProviderConfig) (*LocalProvider, error) {
	if cfg == nil || len(cfg.Tokens) == 0 {
		return nil, emperrors.New("mcp: local provider requires at least one token")
	}
	for i, t := range cfg.Tokens {
		if t.Token == "" {
			return nil, emperrors.Errorf("mcp: local token %d is empty", i)
		}
		if t.User == "" {
			return nil, emperrors.Errorf("mcp: local token %d has no user", i)
		}
	}
	return &LocalProvider{tokens: cfg.Tokens}, nil
}

// Verify implements Provider. Local tokens are static: they never expire (the
// zero time), so revocation is done by removing the token from the config.
func (p *LocalProvider) Verify(_ context.Context, token string, _ *http.Request) (*Identity, time.Time, error) {
	if token == "" {
		return nil, time.Time{}, emperrors.Wrap(auth.ErrInvalidToken, "empty token")
	}
	for _, t := range p.tokens {
		if subtle.ConstantTimeCompare([]byte(t.Token), []byte(token)) == 1 {
			return &Identity{User: t.User, Groups: append([]string(nil), t.Groups...)}, time.Time{}, nil
		}
	}
	return nil, time.Time{}, emperrors.Wrap(auth.ErrInvalidToken, "unknown token")
}
