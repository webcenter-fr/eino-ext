// Package serverconfig loads the YAML configuration of the deployable MCP
// server, resolves file/environment secret references, and wires the selected
// tool family into a libs/mcp server.
package serverconfig

import (
	mcpserver "github.com/webcenter-fr/eino-ext/libs/mcp"
)

// Config is the top-level MCP server configuration.
type Config struct {
	// Server selects the tool family to expose.
	Server string `mapstructure:"server" validate:"required,oneof=kubernetes argocd prometheus grafana" jsonschema:"(required) Tool family: kubernetes, argocd, prometheus or grafana"`
	// Transport is the MCP transport: stdio or http.
	Transport string `mapstructure:"transport" validate:"omitempty,oneof=stdio http" jsonschema:"description=MCP transport: 'stdio' or 'http' (default 'stdio')"`
	// Addr is the HTTP listen address for the MCP transport.
	Addr string `mapstructure:"addr" validate:"omitempty" jsonschema:"description=HTTP listen address for the MCP transport (default ':8080')"`
	// OpsAddr is the unauthenticated operations listen address (/healthz,
	// /readyz, /metrics). It is never exposed through a Service.
	OpsAddr string `mapstructure:"opsAddr" validate:"omitempty" jsonschema:"description=Operations listen address for /healthz, /readyz and /metrics (default ':9090')"`
	// Log configures the process logger.
	Log LogConfig `mapstructure:"log" validate:"omitempty" jsonschema:"description=Process logging"`
	// Kubernetes configures the Kubernetes family.
	Kubernetes *KubernetesFamily `mapstructure:"kubernetes" validate:"omitempty" jsonschema:"description=Kubernetes family configuration"`
	// ArgoCD configures the ArgoCD family.
	ArgoCD *InstanceFamily `mapstructure:"argocd" validate:"omitempty" jsonschema:"description=ArgoCD family configuration"`
	// Prometheus configures the Prometheus family.
	Prometheus *InstanceFamily `mapstructure:"prometheus" validate:"omitempty" jsonschema:"description=Prometheus family configuration"`
	// Grafana configures the Grafana family.
	Grafana *InstanceFamily `mapstructure:"grafana" validate:"omitempty" jsonschema:"description=Grafana family configuration"`
	// Auth configures bearer-token authentication for the HTTP transport.
	Auth *AuthConfig `mapstructure:"auth" validate:"omitempty" jsonschema:"description=Bearer-token authentication for the HTTP transport"`
	// Authz configures RBAC.
	Authz *mcpserver.AuthzConfig `mapstructure:"authz" validate:"omitempty" jsonschema:"description=RBAC policy"`
	// Approval configures write-tool approval.
	Approval *ApprovalConfig `mapstructure:"approval" validate:"omitempty" jsonschema:"description=Write-tool approval settings"`
}

// LogConfig configures the process logger.
type LogConfig struct {
	// Level is a logrus level name.
	Level string `mapstructure:"level" validate:"omitempty,oneof=trace debug info warn warning error fatal panic" jsonschema:"description=Log level (default 'info')"`
	// Format is text or json.
	Format string `mapstructure:"format" validate:"omitempty,oneof=text json" jsonschema:"description=Log format: 'text' or 'json' (default 'text')"`
}

// KubernetesFamily configures the Kubernetes tool family.
type KubernetesFamily struct {
	// Clusters maps a cluster name to its connection settings.
	Clusters map[string]ClusterEntry `mapstructure:"clusters" validate:"required,min=1,dive" jsonschema:"(required) Clusters keyed by name"`
}

// ClusterEntry configures one Kubernetes cluster.
type ClusterEntry struct {
	// InCluster uses the pod service account.
	InCluster bool `mapstructure:"inCluster" jsonschema:"description=Use the in-cluster service account"`
	// KubeconfigFile is the path to a kubeconfig file (required unless InCluster).
	KubeconfigFile string `mapstructure:"kubeconfigFile" validate:"required_without=InCluster,omitempty,filepath" jsonschema:"description=Kubeconfig file path (required unless inCluster)"`
	// Context selects a kubeconfig context.
	Context string `mapstructure:"context" jsonschema:"description=Kubeconfig context name"`
	// DisallowedNamespaces are namespaces the tools refuse to touch.
	DisallowedNamespaces []string `mapstructure:"disallowedNamespaces" jsonschema:"description=Namespaces the tools refuse to touch (default kube-system, kube-public, kube-node-lease)"`
	// DefaultTimeout is the default operation timeout (Go duration).
	DefaultTimeout string `mapstructure:"defaultTimeout" validate:"omitempty" jsonschema:"description=Default operation timeout (Go duration, e.g. '30s')"`
}

// InstanceFamily configures a URL/token based tool family.
type InstanceFamily struct {
	// Instances maps an instance name to its connection settings.
	Instances map[string]InstanceEntry `mapstructure:"instances" validate:"required,min=1,dive" jsonschema:"(required) Instances keyed by name"`
}

// InstanceEntry configures one URL/token based instance.
type InstanceEntry struct {
	// URL is the instance base URL.
	URL string `mapstructure:"url" validate:"required,url" jsonschema:"(required) Instance base URL"`
	// TokenFile is a file containing the bearer token.
	TokenFile string `mapstructure:"tokenFile" validate:"required_without=TokenEnv" jsonschema:"description=File containing the bearer token (required unless tokenEnv)"`
	// TokenEnv is the environment variable containing the bearer token.
	TokenEnv string `mapstructure:"tokenEnv" jsonschema:"description=Environment variable containing the bearer token"`
	// Token is the resolved bearer token. Never set from YAML.
	Token string `mapstructure:"-" validate:"required" jsonschema:"-"`
	// TLSSkipVerify disables TLS certificate verification.
	TLSSkipVerify bool `mapstructure:"tlsSkipVerify" jsonschema:"description=Disable TLS certificate verification"`
	// DefaultTimeout is the default request timeout (Go duration).
	DefaultTimeout string `mapstructure:"defaultTimeout" validate:"omitempty" jsonschema:"description=Default request timeout (Go duration, e.g. '30s')"`
}

// AuthConfig mirrors mcpserver.AuthConfig with secret references.
type AuthConfig struct {
	// Providers verify bearer tokens, tried in order.
	Providers []ProviderConfig `mapstructure:"providers" validate:"required,min=1,dive" jsonschema:"(required) Bearer-token providers, tried in order"`
	// ResourceMetadataURL is returned in the WWW-Authenticate header.
	ResourceMetadataURL string `mapstructure:"resourceMetadataURL" validate:"omitempty,url" jsonschema:"description=RFC 9728 resource metadata URL"`
	// RequiredScopes are required on the access token.
	RequiredScopes []string `mapstructure:"requiredScopes" jsonschema:"description=Scopes required on the access token"`
}

// ProviderConfig is a single auth provider.
type ProviderConfig struct {
	// Type is local or oidc.
	Type string `mapstructure:"type" validate:"required,oneof=local oidc" jsonschema:"(required) Provider type: 'local' or 'oidc'"`
	// Local holds local-provider settings.
	Local *LocalProviderConfig `mapstructure:"local" validate:"omitempty" jsonschema:"description=Local provider settings"`
	// OIDC holds OIDC-provider settings.
	OIDC *OIDCProviderConfig `mapstructure:"oidc" validate:"omitempty" jsonschema:"description=OIDC provider settings"`
}

// LocalProviderConfig configures the static-token provider.
type LocalProviderConfig struct {
	// Tokens maps bearer tokens to identities.
	Tokens []LocalToken `mapstructure:"tokens" validate:"required,min=1,dive" jsonschema:"(required) Static bearer tokens mapped to identities"`
}

// LocalToken maps a static bearer token to an identity.
type LocalToken struct {
	// TokenFile is a file containing the bearer token.
	TokenFile string `mapstructure:"tokenFile" validate:"required_without=TokenEnv" jsonschema:"description=File containing the bearer token (required unless tokenEnv)"`
	// TokenEnv is the environment variable containing the bearer token.
	TokenEnv string `mapstructure:"tokenEnv" jsonschema:"description=Environment variable containing the bearer token"`
	// Token is the resolved bearer token. Never set from YAML.
	Token string `mapstructure:"-" validate:"required" jsonschema:"-"`
	// User is the user name for this token.
	User string `mapstructure:"user" validate:"required" jsonschema:"(required) User name for this token"`
	// Groups are the groups for this token.
	Groups []string `mapstructure:"groups" jsonschema:"description=Groups for this token"`
	// Scopes are the scopes granted to this token.
	Scopes []string `mapstructure:"scopes" jsonschema:"description=Scopes granted to this token"`
}

// OIDCProviderConfig configures the OIDC provider.
type OIDCProviderConfig struct {
	// Issuer is the OIDC issuer URL.
	Issuer string `mapstructure:"issuer" validate:"required,url" jsonschema:"(required) OIDC issuer URL"`
	// Audience is the expected audience.
	Audience string `mapstructure:"audience" validate:"required" jsonschema:"(required) Expected audience (aud claim)"`
	// UserClaim is the claim mapped to Identity.User.
	UserClaim string `mapstructure:"userClaim" validate:"omitempty" jsonschema:"description=JWT claim mapped to Identity.User"`
	// SubClaim is the claim mapped to Identity.Sub.
	SubClaim string `mapstructure:"subClaim" validate:"omitempty" jsonschema:"description=JWT claim mapped to Identity.Sub"`
	// GroupsClaim is the claim mapped to Identity.Groups.
	GroupsClaim string `mapstructure:"groupsClaim" validate:"omitempty" jsonschema:"description=JWT claim mapped to Identity.Groups"`
	// JWKSURL overrides the JWKS endpoint.
	JWKSURL string `mapstructure:"jwksURL" validate:"omitempty,url" jsonschema:"description=JWKS endpoint override"`
}

// ApprovalConfig configures write-tool approval.
type ApprovalConfig struct {
	// Timeout is the maximum time to wait for a human decision.
	Timeout string `mapstructure:"timeout" validate:"omitempty" jsonschema:"description=Max time to wait for a human decision (default '5m')"`
	// Service configures the standalone approval service.
	Service *ApprovalServiceConfig `mapstructure:"service" validate:"omitempty" jsonschema:"description=Standalone approval service"`
}

// ApprovalServiceConfig configures the approval service client with secret
// references.
type ApprovalServiceConfig struct {
	// URL is the approval service base URL.
	URL string `mapstructure:"url" validate:"required,url" jsonschema:"(required) Approval service base URL"`
	// ServerName is the MCP server name asserted to the approval service.
	ServerName string `mapstructure:"serverName" validate:"required" jsonschema:"(required) MCP server name asserted to the approval service"`
	// ServerTokenFile is a file containing the per-server bearer token.
	ServerTokenFile string `mapstructure:"serverTokenFile" validate:"required_without=ServerTokenEnv" jsonschema:"description=File containing the per-server bearer token"`
	// ServerTokenEnv is the environment variable containing the per-server bearer token.
	ServerTokenEnv string `mapstructure:"serverTokenEnv" jsonschema:"description=Environment variable containing the per-server bearer token"`
	// ServerToken is the resolved per-server bearer token.
	ServerToken string `mapstructure:"-" validate:"required" jsonschema:"-"`
	// ReceiptSecretFile is a file containing the HS256 receipt secret.
	ReceiptSecretFile string `mapstructure:"receiptSecretFile" jsonschema:"description=File containing the HS256 receipt secret"`
	// ReceiptSecretEnv is the environment variable containing the HS256 receipt secret.
	ReceiptSecretEnv string `mapstructure:"receiptSecretEnv" jsonschema:"description=Environment variable containing the HS256 receipt secret"`
	// ReceiptSecret is the resolved HS256 receipt secret.
	ReceiptSecret string `mapstructure:"-" validate:"required_without=ReceiptPrivateKeyPEM" jsonschema:"-"`
	// ReceiptPrivateKeyFile is a file containing the PEM private key.
	ReceiptPrivateKeyFile string `mapstructure:"receiptPrivateKeyFile" jsonschema:"description=File containing the PEM private key"`
	// ReceiptPrivateKeyEnv is the environment variable containing the PEM private key.
	ReceiptPrivateKeyEnv string `mapstructure:"receiptPrivateKeyEnv" jsonschema:"description=Environment variable containing the PEM private key"`
	// ReceiptPrivateKeyPEM is the resolved PEM private key.
	ReceiptPrivateKeyPEM string `mapstructure:"-" validate:"required_without=ReceiptSecret" jsonschema:"-"`
	// ReceiptKeyID is the JWT kid header.
	ReceiptKeyID string `mapstructure:"receiptKeyID" validate:"omitempty" jsonschema:"description=Receipt JWT key id"`
	// ReceiptAlgorithm is HS256, RS256 or EdDSA.
	ReceiptAlgorithm string `mapstructure:"receiptAlgorithm" validate:"omitempty,oneof=HS256 RS256 EdDSA" jsonschema:"description=Receipt algorithm (default 'HS256')"`
	// PollInterval is the delay between long-poll slices.
	PollInterval string `mapstructure:"pollInterval" validate:"omitempty" jsonschema:"description=Long-poll slice interval (default '250ms')"`
	// RequestTimeout is the per-request HTTP timeout.
	RequestTimeout string `mapstructure:"requestTimeout" validate:"omitempty" jsonschema:"description=Per-request HTTP timeout (default '35s')"`
	// TTL is the approval record TTL requested from the service.
	TTL string `mapstructure:"ttl" validate:"omitempty" jsonschema:"description=Approval record TTL (default '15m')"`
}
