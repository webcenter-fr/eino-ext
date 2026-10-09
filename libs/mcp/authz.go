package mcp

import (
	"slices"
	"strings"

	emperrors "emperror.dev/errors"
)

// ErrAccessDenied is returned when an identity matches no granting rule.
var ErrAccessDenied = emperrors.New("mcp: access denied")

// Authorizer evaluates the RBAC policy. It is safe for concurrent use.
type Authorizer struct {
	rules []AccessRule
}

// NewAuthorizer builds an Authorizer from the config.
func NewAuthorizer(cfg *AuthzConfig) (*Authorizer, error) {
	if cfg == nil || len(cfg.Rules) == 0 {
		return nil, emperrors.New("mcp: authorization config requires at least one rule")
	}
	return &Authorizer{rules: cfg.Rules}, nil
}

// Authorize reports whether id may call toolName on instance.
// isWrite selects the operation ("write" for write tools, "read" otherwise).
// Fail closed: nil identity or no matching+granting rule → error wrapping
// ErrAccessDenied.
func (a *Authorizer) Authorize(id *Identity, toolName, instance string, isWrite bool) error {
	if id == nil {
		return emperrors.Wrap(ErrAccessDenied, "no authenticated identity")
	}
	op := "read"
	if isWrite {
		op = "write"
	}
	for _, rule := range a.rules {
		if !rule.matchesIdentity(id) {
			continue
		}
		if !rule.allowsOperation(op) || !rule.allowsTool(toolName) || !rule.allowsInstance(instance) {
			continue
		}
		return nil
	}
	return emperrors.Wrapf(ErrAccessDenied,
		"user %q is not allowed to %s tool %q on instance %q", id.User, op, toolName, instance)
}

// CanUse reports whether id may see/call toolName at the category level. Used
// for tools/list filtering (per-identity server). Instance is not checked here.
func (a *Authorizer) CanUse(id *Identity, toolName string, isWrite bool) bool {
	return a.Authorize(id, toolName, "", isWrite) == nil
}

func (r AccessRule) matchesIdentity(id *Identity) bool {
	if len(r.Users) > 0 && !slices.Contains(r.Users, id.User) {
		return false
	}
	if len(r.Groups) > 0 && !slices.ContainsFunc(r.Groups, func(g string) bool {
		return slices.Contains(id.Groups, g)
	}) {
		return false
	}
	return true
}

func (r AccessRule) allowsOperation(op string) bool {
	return len(r.Operations) == 0 || slices.Contains(r.Operations, op)
}

func (r AccessRule) allowsTool(toolName string) bool {
	if len(r.Tools) == 0 {
		return true
	}
	for _, pattern := range r.Tools {
		if strings.HasSuffix(pattern, "*") {
			if strings.HasPrefix(toolName, strings.TrimSuffix(pattern, "*")) {
				return true
			}
			continue
		}
		if pattern == toolName {
			return true
		}
	}
	return false
}

// allowsInstance reports whether the instance is allowed. An empty instance
// (tool without an instance argument) always passes — the tool's own required
// validation rejects a missing instance.
func (r AccessRule) allowsInstance(instance string) bool {
	if instance == "" || len(r.Instances) == 0 {
		return true
	}
	return slices.Contains(r.Instances, "*") || slices.Contains(r.Instances, instance)
}
