package mcp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewAuthorizer(t *testing.T) {
	_, err := NewAuthorizer(nil)
	assert.Error(t, err)
	_, err = NewAuthorizer(&AuthzConfig{})
	assert.Error(t, err)
	a, err := NewAuthorizer(&AuthzConfig{Rules: []AccessRule{{}}})
	require.NoError(t, err)
	assert.NotNil(t, a)
}

func TestAuthorize(t *testing.T) {
	a, err := NewAuthorizer(&AuthzConfig{Rules: []AccessRule{
		{Users: []string{"alice"}, Operations: []string{"read", "write"}, Instances: []string{"prod"}},
		{Groups: []string{"devs"}, Operations: []string{"read"}},
		{Users: []string{"bob"}, Groups: []string{"ops"}, Instances: []string{"*"}, Tools: []string{"kubernetes_resource_*"}},
	}})
	require.NoError(t, err)

	tests := []struct {
		name     string
		id       *Identity
		tool     string
		instance string
		isWrite  bool
		wantErr  bool
	}{
		{"nil identity denied", nil, "kubernetes_list", "prod", false, true},
		{"no matching rule denied", &Identity{User: "mallory"}, "kubernetes_list", "prod", false, true},
		{"user match read allowed", &Identity{User: "alice"}, "kubernetes_list", "prod", false, false},
		{"user match write allowed", &Identity{User: "alice"}, "kubernetes_resource_delete", "prod", true, false},
		{"user match other instance denied", &Identity{User: "alice"}, "kubernetes_list", "dev", false, true},
		{"group match read allowed", &Identity{User: "carol", Groups: []string{"devs"}}, "kubernetes_list", "any", false, false},
		{"group match write denied (read-only rule)", &Identity{User: "carol", Groups: []string{"devs"}}, "kubernetes_resource_delete", "any", true, true},
		{"users and groups: only user matches denied (AND)", &Identity{User: "bob", Groups: []string{"devs"}}, "kubernetes_resource_delete", "prod", true, true},
		{"users and groups: both match allowed", &Identity{User: "bob", Groups: []string{"ops"}}, "kubernetes_resource_delete", "prod", true, false},
		{"glob tool match", &Identity{User: "bob", Groups: []string{"ops"}}, "kubernetes_resource_apply", "staging", true, false},
		{"glob tool non-match denied", &Identity{User: "bob", Groups: []string{"ops"}}, "kubernetes_list", "prod", true, true},
		{"star instance allowed", &Identity{User: "bob", Groups: []string{"ops"}}, "kubernetes_resource_delete", "anything", true, false},
		{"empty instance skips instance check", &Identity{User: "alice"}, "kubernetes_cluster_list", "", false, false},
		{"empty instance skips instance check (write)", &Identity{User: "alice"}, "kubernetes_resource_delete", "", true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := a.Authorize(tt.id, tt.tool, tt.instance, tt.isWrite)
			if tt.wantErr {
				assert.ErrorIs(t, err, ErrAccessDenied)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestAuthorizeUnionAcrossRules(t *testing.T) {
	a, err := NewAuthorizer(&AuthzConfig{Rules: []AccessRule{
		{Users: []string{"alice"}, Operations: []string{"read"}, Instances: []string{"a"}},
		{Users: []string{"alice"}, Operations: []string{"write"}, Instances: []string{"b"}},
	}})
	require.NoError(t, err)
	id := &Identity{User: "alice"}

	assert.NoError(t, a.Authorize(id, "tool_read", "a", false)) // read on A
	assert.NoError(t, a.Authorize(id, "tool_write", "b", true)) // write on B
	assert.ErrorIs(t, a.Authorize(id, "tool_read", "b", false), ErrAccessDenied)
	assert.ErrorIs(t, a.Authorize(id, "tool_write", "a", true), ErrAccessDenied)
}

func TestAuthorizeExactToolAndCanUse(t *testing.T) {
	a, err := NewAuthorizer(&AuthzConfig{Rules: []AccessRule{
		{Users: []string{"alice"}, Tools: []string{"kubernetes_list"}},
	}})
	require.NoError(t, err)
	id := &Identity{User: "alice"}

	assert.NoError(t, a.Authorize(id, "kubernetes_list", "", false))
	assert.ErrorIs(t, a.Authorize(id, "kubernetes_describe", "", false), ErrAccessDenied)

	assert.True(t, a.CanUse(id, "kubernetes_list", false))
	assert.False(t, a.CanUse(id, "kubernetes_describe", false))
	assert.False(t, a.CanUse(nil, "kubernetes_list", false))
}
