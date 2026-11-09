package mcp

import (
	"sort"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/auth"
)

// Identity is the authenticated caller.
type Identity struct {
	// User is the display name (e.g. preferred_username, or the local token
	// user). It is used for RBAC and audit display.
	User string `json:"user"`
	// Sub is the stable subject (the OIDC "sub" claim) used for approval and
	// anti-phishing binding. It may be empty for identities without a distinct
	// subject (local tokens); Subject() falls back to User in that case.
	Sub    string   `json:"sub,omitempty"`
	Groups []string `json:"groups,omitempty"`
}

// Subject returns the identity's subject for approval/anti-phishing binding.
// It is the OIDC sub when available, falling back to User for identities
// without a distinct subject (local tokens, or a LocalIdentity with no Sub).
func (id *Identity) Subject() string {
	if id == nil {
		return ""
	}
	if id.Sub != "" {
		return id.Sub
	}
	return id.User
}

// identityFromTokenInfo builds an Identity from the SDK's auth.TokenInfo.
// Returns nil when ti is nil.
func identityFromTokenInfo(ti *auth.TokenInfo) *Identity {
	if ti == nil {
		return nil
	}
	id := &Identity{User: ti.UserID}
	if sub, ok := ti.Extra["sub"].(string); ok {
		id.Sub = sub
	}
	switch g := ti.Extra["groups"].(type) {
	case []string:
		id.Groups = g
	case []any:
		for _, v := range g {
			if s, ok := v.(string); ok {
				id.Groups = append(id.Groups, s)
			}
		}
	}
	return id
}

// identityCacheKey returns a stable cache key for per-identity servers.
// nil identity → "anonymous".
//
// The key must be injective: two different identities must never share a
// per-identity server (a collision would serve one identity the other's
// tools/list view). Each element is quoted with strconv.Quote — quoted strings
// never contain a raw NUL byte — so the separator-joined key cannot collide
// when a user or group name contains the separator (a plain
// user+"\x00"+join(groups, ",") key collides for groups ["b,c"] vs ["b","c"]).
func identityCacheKey(id *Identity) string {
	if id == nil {
		return "anonymous"
	}
	groups := append([]string(nil), id.Groups...)
	sort.Strings(groups)
	parts := make([]string, 0, len(groups)+1)
	parts = append(parts, strconv.Quote(id.User))
	for _, g := range groups {
		parts = append(parts, strconv.Quote(g))
	}
	return strings.Join(parts, "\x00")
}

// identityMetadata returns audit metadata for an identity (nil-safe).
func identityMetadata(id *Identity) map[string]string {
	if id == nil {
		return map[string]string{"user": "anonymous"}
	}
	return map[string]string{"user": id.User, "groups": strings.Join(id.Groups, ",")}
}
