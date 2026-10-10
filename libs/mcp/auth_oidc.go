package mcp

import (
	"context"
	"net/http"
	"strings"

	emperrors "emperror.dev/errors"
	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/modelcontextprotocol/go-sdk/auth"
)

// OIDCProvider verifies OIDC-issued JWT access tokens (resource-server
// pattern): signature via JWKS, issuer, audience, expiry.
type OIDCProvider struct {
	verifier    *oidc.IDTokenVerifier
	userClaim   string
	groupsClaim string
}

// NewOIDCProvider builds an OIDCProvider. It fetches the issuer's discovery
// document (and JWKS) at construction — fail fast at startup if the IdP is
// unreachable.
func NewOIDCProvider(ctx context.Context, cfg *OIDCProviderConfig) (*OIDCProvider, error) {
	if cfg == nil {
		return nil, emperrors.New("mcp: oidc provider config is required")
	}
	userClaim := cfg.UserClaim
	if userClaim == "" {
		userClaim = "preferred_username"
	}
	groupsClaim := cfg.GroupsClaim
	if groupsClaim == "" {
		groupsClaim = "groups"
	}
	verifierConfig := &oidc.Config{ClientID: cfg.Audience}
	var verifier *oidc.IDTokenVerifier
	if cfg.JWKSURL != "" {
		keySet := oidc.NewRemoteKeySet(ctx, cfg.JWKSURL)
		verifier = oidc.NewVerifier(cfg.Issuer, keySet, verifierConfig)
	} else {
		provider, err := oidc.NewProvider(ctx, cfg.Issuer)
		if err != nil {
			return nil, emperrors.Wrap(err, "failed to fetch OIDC discovery document")
		}
		verifier = provider.Verifier(verifierConfig)
	}
	return &OIDCProvider{verifier: verifier, userClaim: userClaim, groupsClaim: groupsClaim}, nil
}

// Verify implements Provider. The returned expiration is the JWT's exp claim,
// so the SDK's RequireBearerToken middleware re-checks it on every request.
func (p *OIDCProvider) Verify(ctx context.Context, token string, _ *http.Request) (*Verification, error) {
	idToken, err := p.verifier.Verify(ctx, token)
	if err != nil {
		return nil, emperrors.Wrapf(auth.ErrInvalidToken, "OIDC token verification failed: %v", err)
	}
	var claims map[string]any
	if err := idToken.Claims(&claims); err != nil {
		return nil, emperrors.Wrapf(auth.ErrInvalidToken, "failed to read OIDC claims: %v", err)
	}
	user := stringClaim(claims, p.userClaim)
	if user == "" {
		user = idToken.Subject
	}
	return &Verification{
		Identity:  &Identity{User: user, Groups: stringSliceClaim(claims, p.groupsClaim)},
		ExpiresAt: idToken.Expiry,
		Scopes:    scopeClaim(claims),
	}, nil
}

func stringClaim(claims map[string]any, name string) string {
	s, _ := claims[name].(string)
	return s
}

func stringSliceClaim(claims map[string]any, name string) []string {
	raw, ok := claims[name].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// scopeClaim extracts the OAuth "scope" claim: a space-separated string per
// RFC 8693, tolerated as a string array by some issuers.
func scopeClaim(claims map[string]any) []string {
	switch raw := claims["scope"].(type) {
	case string:
		return strings.Fields(raw)
	case []any:
		out := make([]string, 0, len(raw))
		for _, v := range raw {
			if s, ok := v.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}
