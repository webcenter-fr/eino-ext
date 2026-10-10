package mcp

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/stretchr/testify/assert"
)

func TestIdentityFromTokenInfo(t *testing.T) {
	assert.Nil(t, identityFromTokenInfo(nil))

	id := identityFromTokenInfo(&auth.TokenInfo{UserID: "alice"})
	if assert.NotNil(t, id) {
		assert.Equal(t, "alice", id.User)
		assert.Empty(t, id.Groups)
	}

	id = identityFromTokenInfo(&auth.TokenInfo{
		UserID: "bob",
		Extra:  map[string]any{"groups": []string{"dev", "ops"}},
	})
	if assert.NotNil(t, id) {
		assert.Equal(t, "bob", id.User)
		assert.Equal(t, []string{"dev", "ops"}, id.Groups)
	}

	// Groups arriving as []any (e.g. decoded from JSON) are converted; non-string
	// entries are dropped.
	id = identityFromTokenInfo(&auth.TokenInfo{
		UserID: "carol",
		Extra:  map[string]any{"groups": []any{"dev", 42, "ops"}},
	})
	if assert.NotNil(t, id) {
		assert.Equal(t, "carol", id.User)
		assert.Equal(t, []string{"dev", "ops"}, id.Groups)
	}
}

func TestIdentityCacheKey(t *testing.T) {
	assert.Equal(t, "anonymous", identityCacheKey(nil))

	// Stable for the same user+groups regardless of group order.
	assert.Equal(t,
		identityCacheKey(&Identity{User: "alice", Groups: []string{"dev", "ops"}}),
		identityCacheKey(&Identity{User: "alice", Groups: []string{"ops", "dev"}}))

	assert.NotEqual(t,
		identityCacheKey(&Identity{User: "alice"}),
		identityCacheKey(&Identity{User: "bob"}))
	assert.NotEqual(t,
		identityCacheKey(&Identity{User: "alice"}),
		identityCacheKey(&Identity{User: "alice", Groups: []string{"dev"}}))
}

func TestIdentityCacheKeyNoCollision(t *testing.T) {
	// The key must be injective: different identities must never share a
	// per-identity server (a collision would serve one identity the other's
	// tools/list view).
	keys := map[string]*Identity{}
	identities := []*Identity{
		{User: "alice", Groups: []string{"b,c"}},   // group literally named "b,c"
		{User: "alice", Groups: []string{"b", "c"}}, // groups "b" and "c"
		{User: "a\x00b"},                            // NUL in the user name
		{User: "a", Groups: []string{"b"}},
		{User: "a", Groups: []string{"b\x00c"}},
		{User: "a", Groups: []string{"b", "c"}},
	}
	for _, id := range identities {
		key := identityCacheKey(id)
		if other, ok := keys[key]; ok {
			t.Fatalf("cache key collision between %+v and %+v (key %q)", other, id, key)
		}
		keys[key] = id
	}
}

func TestIdentityMetadata(t *testing.T) {
	assert.Equal(t, map[string]string{"user": "anonymous"}, identityMetadata(nil))
	assert.Equal(t,
		map[string]string{"user": "alice", "groups": "dev,ops"},
		identityMetadata(&Identity{User: "alice", Groups: []string{"dev", "ops"}}))
	assert.Equal(t,
		map[string]string{"user": "bob", "groups": ""},
		identityMetadata(&Identity{User: "bob"}))
}
