package prometheus

import "sort"

// Configs is a map of Prometheus instance configurations, where the key is the instance name.
type Configs map[string]Config

// Config holds the connection configuration for a single Prometheus instance.
type Config struct {
	// Address is the Prometheus server URL (e.g. "http://localhost:9090").
	Address string `validate:"required" jsonschema:"description=Prometheus server URL, e.g. http://localhost:9090"`
	// Username is the optional username for basic auth.
	Username string
	// Password is the optional password for basic auth.
	Password string
	// BearerToken is the optional bearer token for authentication.
	BearerToken string
	// InsecureSkipVerify disables TLS certificate verification.
	InsecureSkipVerify bool
}

// GetConfig retrieves the configuration for a given instance name.
func (c Configs) GetConfig(instanceName string) Config {
	return c[instanceName]
}

// GetInstanceNames returns a sorted slice of all instance names in the Configs map.
func (c Configs) GetInstanceNames() []string {
	names := make([]string, 0, len(c))
	for name := range c {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
