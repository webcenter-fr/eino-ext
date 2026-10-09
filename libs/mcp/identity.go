package mcp

import (
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/auth"
)

// Identity is the authenticated caller.
type Identity struct {
	User   string   `json:"user"`
	Groups []string `json:"groups,omitempty"`
}

// identityFromTokenInfo builds an Identity from the SDK's auth.TokenInfo.
// Returns nil when ti is nil.
func identityFromTokenInfo(ti *auth.TokenInfo) *Identity {
	if ti == nil {
		return nil
	}
	id := &Identity{User: ti.UserID}
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
func identityCacheKey(id *Identity) string {
	if id == nil {
		return "anonymous"
	}
	groups := append([]string(nil), id.Groups...)
	sort.Strings(groups)
	return id.User + "\x00" + strings.Join(groups, ",")
}

// identityMetadata returns audit metadata for an identity (nil-safe).
func identityMetadata(id *Identity) map[string]string {
	if id == nil {
		return map[string]string{"user": "anonymous"}
	}
	return map[string]string{"user": id.User, "groups": strings.Join(id.Groups, ",")}
}
