// Package grafana provides eino tools for Grafana dashboard management.
package grafana

import (
	"github.com/webcenter-fr/eino-ext/libs/toolkit/toolutil"
)

// Configs is a map of Grafana instance configurations keyed by instance name.
type Configs map[string]Config

// Config represents the configuration for a single Grafana instance.
type Config struct {
	// URL is the Grafana server URL with scheme, e.g. "https://grafana.example.com".
	URL string `validate:"required" jsonschema:"description=Grafana server URL with scheme (https:// or http://)"`

	// Token is the service-account or API token used for Bearer auth.
	// Hidden from JSON schema output via json:"-".
	Token string `json:"-"`

	// TLSSkipVerify skips TLS certificate verification. Hidden from schema.
	TLSSkipVerify bool `json:"-"`

	// DefaultTimeout is a Go duration string for per-request timeouts.
	// Defaults to "30s" when empty.
	DefaultTimeout string `validate:"omitempty" jsonschema:"description=Default timeout for HTTP calls (Go duration string, e.g. '30s')"`

	// ProtectedDashboards defines the blocklist of dashboards that cannot be
	// modified by the build tool.
	ProtectedDashboards ProtectedDashboardsConfig `validate:"omitempty"`
}

// ProtectedDashboardsConfig defines criteria for dashboards that are protected
// from modification. A dashboard is protected if it matches ANY one criterion
// (logical OR across categories; within a category, it's a match if the
// dashboard's value matches ANY entry in the list).
type ProtectedDashboardsConfig struct {
	// UIDs lists exact dashboard UIDs that are protected from editing.
	UIDs []string `json:"uids,omitempty" validate:"omitempty"`

	// TitlePrefixes lists title prefixes; a dashboard whose title starts with
	// any of these strings is protected.
	TitlePrefixes []string `json:"titlePrefixes,omitempty" validate:"omitempty"`

	// Folders lists folder UIDs; any dashboard residing in one of these folders
	// is protected.
	Folders []string `json:"folders,omitempty" validate:"omitempty"`

	// Tags lists dashboard tags; any dashboard carrying one of these tags is
	// protected.
	Tags []string `json:"tags,omitempty" validate:"omitempty"`
}

// GetConfig returns the configuration for the given instance name.
// Returns the zero value if the instance is not present.
func (c Configs) GetConfig(instanceName string) Config {
	return c[instanceName]
}

// GetInstanceNames returns all instance names in sorted order.
func (c Configs) GetInstanceNames() []string {
	return toolutil.SortedKeys(c)
}
