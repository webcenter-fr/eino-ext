// Package approval implements the standalone, out-of-band human approval
// service for MCP write tools: REST registration, long-poll decisions, OIDC
// browser sessions, CSRF, policy evaluation, signed receipts and the embedded
// Nuxt SPA.
package approval

import (
	"time"

	"github.com/webcenter-fr/eino-ext/server/approval/store"
)

// Config configures the approval service.
type Config struct {
	// ListenAddr is the HTTP listen address. Default ":8080".
	ListenAddr string `validate:"omitempty" jsonschema:"description=HTTP listen address (default ':8080')"`
	// BaseURL is the public base URL of the service (used for approval links
	// and the OIDC redirect). Required.
	BaseURL string `validate:"required,url" jsonschema:"(required) Public base URL of the approval service"`
	// Store is the approval backend. Default: in-memory.
	Store store.ApprovalStore `json:"-" jsonschema:"-"`
	// TTL is the default approval record lifetime. Default "15m".
	TTL string `validate:"omitempty" jsonschema:"description=Default approval record TTL (default '15m')"`
	// SessionTTL is the browser session lifetime. Default "8h".
	SessionTTL string `validate:"omitempty" jsonschema:"description=Browser session lifetime (default '8h')"`
	// CookieName is the session cookie name. Default "approval_session".
	CookieName string `validate:"omitempty" jsonschema:"description=Session cookie name (default 'approval_session')"`
	// CSRFHeader is the double-submit CSRF header. Default "X-CSRF-Token".
	CSRFHeader string `validate:"omitempty" jsonschema:"description=CSRF header name (default 'X-CSRF-Token')"`
	// ServerCredentials are the per-MCP-server bearer credentials. Tokens are
	// stored as SHA-256 hex digests, never plaintext.
	ServerCredentials []ServerCredential `validate:"required,min=1,dive" jsonschema:"(required) Per-MCP-server bearer credentials (token hashes)"`
	// OIDC configures browser authentication. Optional; when nil the browser
	// endpoints return 503 (the REST API still works).
	OIDC *OIDCConfig `validate:"omitempty" jsonschema:"description=OIDC browser authentication"`
	// Receipt configures signed decision receipts.
	Receipt ReceiptConfig `validate:"required" jsonschema:"(required) Signed receipt settings"`
	// Policy configures who may approve. Default: self-approve.
	Policy *PolicyConfig `validate:"omitempty" jsonschema:"description=Approver policy (default self-approve)"`
	// Notifier is notified when a new approval is registered. Default no-op.
	Notifier Notifier `json:"-" jsonschema:"-"`
	// AuditSink receives the approval service's independent audit records.
	// Default: structured log.
	AuditSink AuditSink `json:"-" jsonschema:"-"`
	// Now overrides the clock (tests).
	Now func() time.Time `json:"-" jsonschema:"-"`
}

// ServerCredential binds a bearer token to a server name and allowed instances.
type ServerCredential struct {
	// ServerName is the MCP server name this credential may assert.
	ServerName string `validate:"required" jsonschema:"(required) MCP server name bound to this credential"`
	// TokenHash is the lowercase hex SHA-256 of the bearer token.
	TokenHash string `validate:"required,len=64,hexadecimal" jsonschema:"(required) SHA-256 hex digest of the bearer token"`
	// AllowedInstances restricts the instances this credential may register.
	// Empty or "*" means all.
	AllowedInstances []string `json:"allowedInstances,omitempty" jsonschema:"description=Allowed instances (empty or '*' means all)"`
}

// OIDCConfig configures the browser OIDC authorization-code + PKCE flow.
type OIDCConfig struct {
	Issuer       string   `validate:"required,url" jsonschema:"(required) OIDC issuer URL"`
	ClientID     string   `validate:"required" jsonschema:"(required) OIDC client id"`
	ClientSecret string   `validate:"required" json:"-" jsonschema:"(required) OIDC client secret"`
	RedirectURL  string   `validate:"required,url" jsonschema:"(required) OIDC redirect URL"`
	Scopes       []string `validate:"omitempty,dive,required" jsonschema:"description=OIDC scopes (default openid, profile, email, groups)"`
	UserClaim    string   `validate:"omitempty" jsonschema:"description=Claim mapped to the user name (default 'preferred_username', fallback 'sub')"`
	GroupsClaim  string   `validate:"omitempty" jsonschema:"description=Claim mapped to groups (default 'groups')"`
}

// ReceiptConfig configures receipt signing.
type ReceiptConfig struct {
	// Secret is the HS256 shared secret.
	Secret string `validate:"required_without=PrivateKeyPEM" json:"-" jsonschema:"description=HS256 shared secret"`
	// PrivateKeyPEM is a PEM-encoded RSA or Ed25519 private key.
	PrivateKeyPEM string `validate:"required_without=Secret" json:"-" jsonschema:"description=PEM private key for RS256/EdDSA"`
	// KeyID is the JWT kid header.
	KeyID string `validate:"omitempty" jsonschema:"description=JWT key id"`
	// Algorithm is HS256 (default), RS256 or EdDSA.
	Algorithm string `validate:"omitempty,oneof=HS256 RS256 EdDSA" jsonschema:"description=Receipt algorithm (default 'HS256')"`
	// TTL is the receipt validity. Default "2m".
	TTL string `validate:"omitempty" jsonschema:"description=Receipt validity (default '2m')"`
}

// PolicyConfig configures approver rules.
type PolicyConfig struct {
	Rules []PolicyRule `validate:"omitempty,dive" jsonschema:"description=Approver rules; no match means self-approve"`
}

// PolicyRule restricts who may approve matching records.
type PolicyRule struct {
	// Name identifies the rule in audit records and receipts.
	Name string `validate:"omitempty" jsonschema:"description=Rule name"`
	// Tools matches tool names (exact or '*').
	Tools []string `validate:"omitempty,dive,required" jsonschema:"description=Tool names (exact or '*')"`
	// Instances matches instances (exact or '*').
	Instances []string `validate:"omitempty,dive,required" jsonschema:"description=Instances (exact or '*')"`
	// Operations matches operations (exact or '*').
	Operations []string `validate:"omitempty,dive,required" jsonschema:"description=Operations (exact or '*')"`
	// RequireFourEyes forbids the requester from approving.
	RequireFourEyes bool `jsonschema:"description=Require approver != requester"`
	// ApproverGroups requires the approver to be in at least one group.
	ApproverGroups []string `validate:"omitempty,dive,required" jsonschema:"description=Required approver groups"`
	// ApproverUsers requires the approver to be one of these subjects.
	ApproverUsers []string `validate:"omitempty,dive,required" jsonschema:"description=Required approver subjects"`
}

// Defaults for the service.
const (
	DefaultListenAddr  = ":8080"
	DefaultTTL         = 15 * time.Minute
	DefaultSessionTTL  = 8 * time.Hour
	DefaultCookieName  = "approval_session"
	DefaultCSRFHeader  = "X-CSRF-Token"
	DefaultCSRFCookie  = "approval_csrf"
	DefaultOAuthCookie = "approval_oauth_state"
	DefaultReceiptTTL  = 2 * time.Minute
	DefaultReceiptAlg  = "HS256"
	DefaultOIDCScopes  = "openid profile email groups"
)
