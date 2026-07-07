package argocd

import (
	"github.com/disaster37/goargocdclient"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/toolutil"
)

// Configs is a map of ArgoCD instance configurations, where the key is the Argocd instance name.
type Configs map[string]Config

// Config represents the configuration for an ArgoCD instance.
type Config struct {
	URL     string `validate:"required" jsonschema:"description=ArgoCD server URL with scheme (https:// or http://)"`
	Options []goargocdclient.Option
}

// GetConfig retrieves the configuration for a given instance name. It returns a api.API if found, or nil if the instance name does not exist in the Configs map.
func (c Configs) GetConfig(instanceName string) Config {
	return c[instanceName]
}

// GetInstanceNames returns a slice of all instances names present in the Configs map.
func (c Configs) GetInstanceNames() []string {
	return toolutil.SortedKeys(c)
}
