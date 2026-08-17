// Package alertmanager provides eino tools for managing alerts on
// Alertmanager instances via the Alertmanager v2 HTTP API.
package alertmanager

import "github.com/webcenter-fr/eino-ext/libs/toolkit/toolutil"

// Configs is a map of Alertmanager instance configurations, where the key is the instance name.
type Configs map[string]Config

// Config holds the connection configuration for a single Alertmanager instance.
type Config struct {
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
