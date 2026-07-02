package argocd

import (
	"github.com/disaster37/goargocdclient"
	"github.com/thoas/go-funk"
)

// Configs is a map of ArgoCD instance configurations, where the key is the Argocd instance name.
type Configs map[string]Config

// Config represents the configuration for an ArgoCD instance.
type Config struct {
	Url string
	goargocdclient.Option
}

// GetConfig retrieves the configuration for a given instance name. It returns a api.API if found, or nil if the instance name does not exist in the Configs map.
func (c Configs) GetConfig(instanceName string) Config {
	return c[instanceName]
}

// GetInstanceNames returns a slice of all instances names present in the Configs map.
func (c Configs) GetInstanceNames() []string {
	return funk.Keys(c).([]string)
}
