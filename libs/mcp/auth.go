package mcp

import (
	"context"
	"net/http"
	"time"

	emperrors "emperror.dev/errors"
	"github.com/modelcontextprotocol/go-sdk/auth"
)

// neverExpires is the auth.TokenInfo.Expiration used for tokens that never
// expire (local static tokens). The SDK's RequireBearerToken middleware
// rejects a zero or past expiration, so "never expires" is represented as a
// far-future time.
var neverExpires = time.Date(9999, time.December, 31, 23, 59, 59, 0, time.UTC)

// Provider verifies a bearer token and returns the caller's identity.
// Implementations must be safe for concurrent use.
type Provider interface {
	// Verify checks the token and returns the identity and the token's
	// expiration (the zero time when the token never expires), or an error
	// unwrapping auth.ErrInvalidToken when the token is not accepted.
	Verify(ctx context.Context, token string, req *http.Request) (*Identity, time.Time, error)
}

// tokenVerifier adapts a provider chain to the SDK's auth.TokenVerifier.
// Providers are tried in order; first success wins. All failures → an error
// unwrapping auth.ErrInvalidToken (→ 401).
func tokenVerifier(providers []Provider) auth.TokenVerifier {
	return func(ctx context.Context, token string, req *http.Request) (*auth.TokenInfo, error) {
		for _, p := range providers {
			id, expiresAt, err := p.Verify(ctx, token, req)
			if err == nil {
				if expiresAt.IsZero() {
					expiresAt = neverExpires
				}
				return &auth.TokenInfo{
					UserID:     id.User,
					Expiration: expiresAt,
					Extra:      map[string]any{"groups": id.Groups},
				}, nil
			}
		}
		return nil, emperrors.Wrap(auth.ErrInvalidToken, "no auth provider accepted the token")
	}
}

// newProviders builds the provider chain from the auth config.
func newProviders(ctx context.Context, cfg *AuthConfig) ([]Provider, error) {
	providers := make([]Provider, 0, len(cfg.Providers))
	for i, pc := range cfg.Providers {
		switch pc.Type {
		case "local":
			if pc.Local == nil {
				return nil, emperrors.Errorf("auth provider %d: local config is required", i)
			}
			p, err := NewLocalProvider(pc.Local)
			if err != nil {
				return nil, emperrors.Wrapf(err, "auth provider %d (local)", i)
			}
			providers = append(providers, p)
		case "oidc":
			if pc.OIDC == nil {
				return nil, emperrors.Errorf("auth provider %d: oidc config is required", i)
			}
			p, err := NewOIDCProvider(ctx, pc.OIDC)
			if err != nil {
				return nil, emperrors.Wrapf(err, "auth provider %d (oidc)", i)
			}
			providers = append(providers, p)
		default:
			return nil, emperrors.Errorf("auth provider %d: unknown type %q", i, pc.Type)
		}
	}
	return providers, nil
}
