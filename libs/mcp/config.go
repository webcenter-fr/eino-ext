package mcp

import (
	"github.com/cloudwego/eino/components/tool"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/safety"
)

// Config configures a Server.
type Config struct {
	// Name is the MCP server name (Implementation.Name). Default "eino-ext-mcp".
	Name string `validate:"required" jsonschema:"(required) MCP server name"`
	// Version is the MCP server version (Implementation.Version). Default "0.0.0".
	Version string `validate:"required" jsonschema:"(required) MCP server version"`
	// Tools are the eino tools to expose.
	Tools []tool.InvokableTool `json:"-"`
	// WriteTools lists tool names that perform write/mutative operations. They
	// are gated (dry-run/confirmed + human approval) and annotated
	// destructiveHint. Contract: every name listed MUST honor dryRun=true as a
	// no-side-effect preview (same contract as the safety middleware).
	WriteTools []string `json:"writeTools" jsonschema:"description=Tool names that perform write/mutative operations"`
	// InstanceParam is the name of the tool argument holding the target
	// cluster/instance (e.g. "cluster" for kubernetes, "instance" for argocd,
	// prometheus, grafana). Used for per-instance RBAC.
	InstanceParam string `validate:"required" jsonschema:"(required) Name of the tool argument holding the target cluster/instance (e.g. 'cluster', 'instance')"`
	// Auth configures bearer-token authentication for the HTTP transport.
	// Optional. When nil, HTTP requests carry no identity (LocalIdentity or nil).
	Auth *AuthConfig `validate:"omitempty" jsonschema:"description=Bearer-token authentication for the HTTP transport"`
	// Authorization is the RBAC policy. Optional. When nil, all tools are
	// visible and callable (open server — operator's choice).
	Authorization *AuthzConfig `validate:"omitempty" jsonschema:"description=RBAC policy mapping users/groups to operations, instances and tools"`
	// Approval configures write-tool approval. Optional (defaults: elicitation,
	// 5m timeout).
	Approval *ApprovalConfig `validate:"omitempty" jsonschema:"description=Write-tool approval settings"`
	// AuditSink receives audit events for EVERY tool call. If nil, defaults to
	// safety.LogSink.
	AuditSink safety.AuditSink `json:"-"`
	// Policy is evaluated before execution for ALL tool calls (read + write).
	// Optional. If nil, pass-through.
	Policy safety.Policy `json:"-"`
	// LocalIdentity is the identity used when no bearer token is present
	// (stdio transport, or HTTP with no auth configured). Still subject to RBAC.
	LocalIdentity *Identity `json:"-"`
	// ExecutionAuthorizer overrides the default elicitation-based authorizer
	// for write tools. If nil, the server uses ElicitationAuthorizer (MCP
	// elicitation → human approval). Custom authorizers MUST derive the decision
	// from server-side state, never from tool arguments (model-controlled).
	ExecutionAuthorizer safety.ExecutionAuthorizer `json:"-"`
}

// AuthConfig configures bearer-token authentication.
type AuthConfig struct {
	// Providers verify bearer tokens. Tried in order; first success wins.
	Providers []ProviderConfig `validate:"required,min=1,dive" jsonschema:"(required) Bearer-token providers, tried in order"`
	// ResourceMetadataURL is returned in the WWW-Authenticate header (RFC 9728).
	ResourceMetadataURL string `validate:"omitempty,url" jsonschema:"description=RFC 9728 resource metadata URL returned in the WWW-Authenticate header"`
	// RequiredScopes are required on the access token. The SDK's
	// RequireBearerToken middleware rejects (403) any request whose token
	// does not carry all of them. Scopes come from LocalToken.Scopes (local
	// provider) or the token's "scope" claim (OIDC provider).
	RequiredScopes []string `json:"requiredScopes" jsonschema:"description=Scopes required on the access token (local: LocalToken.Scopes; OIDC: the token's 'scope' claim)"`
}

// ProviderConfig is a single auth provider.
type ProviderConfig struct {
	// Type is the provider type: "local" or "oidc".
	Type string `validate:"required,oneof=local oidc" jsonschema:"(required) Provider type: 'local' or 'oidc'"`
	// Local holds local-provider settings (Type == "local").
	Local *LocalProviderConfig `validate:"omitempty" jsonschema:"description=Local provider settings (Type 'local')"`
	// OIDC holds OIDC-provider settings (Type == "oidc").
	OIDC *OIDCProviderConfig `validate:"omitempty" jsonschema:"description=OIDC provider settings (Type 'oidc')"`
}

// LocalProviderConfig configures the static-token provider.
type LocalProviderConfig struct {
	// Tokens maps bearer tokens to identities.
	Tokens []LocalToken `validate:"required,min=1,dive" jsonschema:"(required) Static bearer tokens mapped to identities"`
}

// LocalToken maps a static bearer token to an identity.
type LocalToken struct {
	// Token is the bearer token. Hidden from JSON schema output.
	Token string `json:"-"`
	// User is the user name for this token.
	User string `validate:"required" jsonschema:"(required) User name for this token"`
	// Groups are the groups for this token.
	Groups []string `json:"groups" jsonschema:"description=Groups for this token"`
	// Scopes are the scopes granted to this token. They are checked against
	// AuthConfig.RequiredScopes by the SDK's RequireBearerToken middleware.
	Scopes []string `validate:"omitempty,dive,required" json:"scopes,omitempty" jsonschema:"description=Scopes granted to this token (checked against AuthConfig.RequiredScopes)"`
}

// OIDCProviderConfig configures the OIDC provider.
type OIDCProviderConfig struct {
	// Issuer is the OIDC issuer URL. The discovery document is fetched from here.
	Issuer string `validate:"required,url" jsonschema:"(required) OIDC issuer URL (discovery document is fetched from here)"`
	// Audience is the expected audience (aud claim) of the access token.
	Audience string `validate:"required" jsonschema:"(required) Expected audience (aud claim) of the access token"`
	// UserClaim is the JWT claim mapped to Identity.User. Default
	// "preferred_username"; falls back to "sub" when empty.
	UserClaim string `validate:"omitempty" jsonschema:"description=JWT claim mapped to Identity.User (default 'preferred_username', fallback 'sub')"`
	// GroupsClaim is the JWT claim mapped to Identity.Groups. Default "groups".
	GroupsClaim string `validate:"omitempty" jsonschema:"description=JWT claim mapped to Identity.Groups (default 'groups')"`
	// JWKSURL overrides the JWKS endpoint (default: from issuer discovery).
	JWKSURL string `validate:"omitempty,url" jsonschema:"description=JWKS endpoint override (default: from issuer discovery)"`
}

// AuthzConfig is the RBAC policy.
type AuthzConfig struct {
	// Rules are the access rules. The union of matching rules grants access.
	// Fail closed: no matching rule → deny.
	Rules []AccessRule `validate:"required,min=1,dive" jsonschema:"(required) RBAC rules; the union of matching rules grants access. Fail closed: no match means deny"`
}

// AccessRule grants access to matching identities.
//
// A rule matches an identity when (len(Users)==0 OR identity.User ∈ Users) AND
// (len(Groups)==0 OR identity.Groups ∩ Groups ≠ ∅).
type AccessRule struct {
	// Users matches Identity.User (exact). Empty = any user.
	Users []string `json:"users" jsonschema:"description=Match Identity.User (exact). Empty means any user"`
	// Groups matches any of Identity.Groups (exact). Empty = any group.
	Groups []string `json:"groups" jsonschema:"description=Match any of Identity.Groups (exact). Empty means any group"`
	// Operations allowed: "read", "write". Empty = all.
	Operations []string `validate:"omitempty,dive,oneof=read write" jsonschema:"description=Allowed operations: 'read', 'write'. Empty means all"`
	// Instances allowed (cluster/instance names). Empty or "*" = all.
	Instances []string `json:"instances" jsonschema:"description=Allowed cluster/instance names. Empty or '*' means all"`
	// Tools allowed (exact names or 'prefix_*' glob). Empty = all tools.
	Tools []string `json:"tools" jsonschema:"description=Allowed tool names (exact or 'prefix_*' glob). Empty means all tools"`
}

// ApprovalConfig configures write-tool approval.
type ApprovalConfig struct {
	// Timeout is the maximum time to wait for the human approval decision (Go
	// duration string, e.g. "5m"). Default "5m". On timeout the write is
	// rejected (fail closed).
	Timeout string `validate:"omitempty" jsonschema:"description=Max time to wait for the human approval decision (Go duration, e.g. '5m'). Default '5m'. On timeout the write is rejected (fail closed)"`
}
