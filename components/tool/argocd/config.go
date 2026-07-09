package argocd

import (
	"github.com/disaster37/goargocdclient"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/toolutil"
)

// Configs is a map of ArgoCD instance configurations, where the key is the Argocd instance name.
type Configs map[string]Config

// Config represents the configuration for an ArgoCD instance.
//
// Token and TLSSkipVerify are convenience fields that are automatically
// converted to goargocdclient.Option values by NewClient and prepended to
// Options. If both are set, explicit Options take precedence.
type Config struct {
	URL           string `validate:"required" jsonschema:"description=ArgoCD server URL with scheme (https:// or http://)"`
	Options       []goargocdclient.Option
	Token         string `jsonschema:"-"`
	TLSSkipVerify bool   `jsonschema:"-"`
}

// GetConfig returns the configuration for the given instance name, or the zero
// value if the instance is not present in the Configs map.
func (c Configs) GetConfig(instanceName string) Config {
	return c[instanceName]
}

// GetInstanceNames returns a slice of all instances names present in the Configs map.
func (c Configs) GetInstanceNames() []string {
	return toolutil.SortedKeys(c)
}
