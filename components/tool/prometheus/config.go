package prometheus

import "github.com/webcenter-fr/eino-ext/libs/toolkit/toolutil"

// Configs is a map of Prometheus instance configurations, where the key is the instance name.
type Configs map[string]Config

// Config holds the connection configuration for a single Prometheus instance.
type Config struct {
	// Address is the Prometheus server URL (e.g. "http://localhost:9090").
	Address string `validate:"required" jsonschema:"description=Prometheus server URL, e.g. http://localhost:9090"`
	// Username is the optional username for basic auth.
	Username string `json:"-"`
	// Password is the optional password for basic auth.
	Password string `json:"-"`
	// BearerToken is the optional bearer token for authentication.
	BearerToken string `json:"-"`
	// TLSSkipVerify disables TLS certificate verification.
	TLSSkipVerify bool
	// Alertmanager holds optional Alertmanager connection settings for this
	// instance. When nil, the Alertmanager tools are unavailable for this
	// instance. Backward compatible: existing Prometheus-only configs are
	// unchanged. validate:"-" skips the nested struct here so an invalid
	// Alertmanager config does not fail the Prometheus client (fail-fast
	// decoupling); it is validated separately by NewAlertmanagerClient.
	Alertmanager *AlertmanagerConfig `validate:"-" jsonschema:"(optional) Alertmanager connection settings. When set, the Alertmanager tools become available for this instance."`
}

// AlertmanagerConfig holds the connection configuration for an Alertmanager
// instance associated with a Prometheus instance.
type AlertmanagerConfig struct {
	// Address is the Alertmanager server URL (e.g. "http://localhost:9093").
	Address string `validate:"required" jsonschema:"description=Alertmanager server URL, e.g. http://localhost:9093"`
	// Username is the optional username for basic auth.
	Username string `json:"-"`
	// Password is the optional password for basic auth.
	Password string `json:"-"`
	// BearerToken is the optional bearer token for authentication.
	BearerToken string `json:"-"`
	// TLSSkipVerify disables TLS certificate verification.
	TLSSkipVerify bool `json:"-"`
	// Timeout is a Go duration string for per-request timeouts.
	// Defaults to "30s" when empty.
	Timeout string `validate:"omitempty" jsonschema:"description=Per-request timeout (Go duration string, e.g. '30s'), defaults to 30s"`
}

// GetConfig retrieves the configuration for a given instance name.
func (c Configs) GetConfig(instanceName string) Config {
	return c[instanceName]
}

// GetInstanceNames returns a sorted slice of all instance names in the Configs map.
func (c Configs) GetInstanceNames() []string {
	return toolutil.SortedKeys(c)
}
