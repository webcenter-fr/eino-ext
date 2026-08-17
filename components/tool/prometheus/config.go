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
	// MaxSamples is the maximum number of samples returned per query.
	// Defaults to 10000.
	MaxSamples int `validate:"omitempty,gte=1,lte=50000" jsonschema:"description=Maximum number of samples returned per query (1-50000, defaults to 10000)"`
	// MaxTimeRange is the maximum time range for range queries.
	// Defaults to 7 days. Parsed as a Go duration string.
	MaxTimeRange string `validate:"omitempty" jsonschema:"description=Maximum time range for range queries (Go duration string, e.g. 168h), defaults to 168h (7 days)"`
	// MinStep is the minimum step size for range queries.
	// Defaults to 15s. Parsed as a Go duration string.
	MinStep string `validate:"omitempty" jsonschema:"description=Minimum step size for range queries (Go duration string, e.g. 15s), defaults to 15s"`
	// Alertmanager holds optional Alertmanager connection settings for this
	// instance. When nil, the Alertmanager tools are unavailable for this
	// instance. Backward compatible: existing Prometheus-only configs are
	// unchanged.
	Alertmanager *AlertmanagerConfig `validate:"omitempty" jsonschema:"(optional) Alertmanager connection settings. When set, the Alertmanager tools become available for this instance."`
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
