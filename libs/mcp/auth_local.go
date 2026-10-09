package mcp

import (
	"context"
	"crypto/sha256"
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
	// digests holds the SHA-256 of each token, precomputed at construction.
	digests [][sha256.Size]byte
}

// NewLocalProvider builds a LocalProvider from the config.
func NewLocalProvider(cfg *LocalProviderConfig) (*LocalProvider, error) {
	if cfg == nil || len(cfg.Tokens) == 0 {
		return nil, emperrors.New("mcp: local provider requires at least one token")
	}
	p := &LocalProvider{tokens: cfg.Tokens, digests: make([][sha256.Size]byte, len(cfg.Tokens))}
	for i, t := range cfg.Tokens {
		if t.Token == "" {
			return nil, emperrors.Errorf("mcp: local token %d is empty", i)
		}
		if t.User == "" {
			return nil, emperrors.Errorf("mcp: local token %d has no user", i)
		}
		p.digests[i] = sha256.Sum256([]byte(t.Token))
	}
	return p, nil
}

// Verify implements Provider. Local tokens are static: they never expire (the
// zero time), so revocation is done by removing the token from the config.
func (p *LocalProvider) Verify(_ context.Context, token string, _ *http.Request) (*Verification, error) {
	if token == "" {
		return nil, emperrors.Wrap(auth.ErrInvalidToken, "empty token")
	}
	// Compare SHA-256 digests, not raw tokens: digests have a fixed length, so
	// ConstantTimeCompare runs in constant time regardless of the token length
	// (a raw comparison returns immediately when the lengths differ, leaking
	// the length of the configured tokens). All tokens are always compared, so
	// the lookup does not leak which tokens exist either.
	presented := sha256.Sum256([]byte(token))
	for i, t := range p.tokens {
		if subtle.ConstantTimeCompare(p.digests[i][:], presented[:]) == 1 {
			return &Verification{
				Identity:  &Identity{User: t.User, Groups: append([]string(nil), t.Groups...)},
				ExpiresAt: time.Time{},
				Scopes:    append([]string(nil), t.Scopes...),
			}, nil
		}
	}
	return nil, emperrors.Wrap(auth.ErrInvalidToken, "unknown token")
}
