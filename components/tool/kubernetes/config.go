package kubernetes

import (
	"github.com/thoas/go-funk"
	"k8s.io/client-go/rest"
)

// Configs is a map of Kubernetes cluster configurations, where the key is the cluster name.
type Configs map[string]*rest.Config

// GetConfig retrieves the configuration for a given cluster name. It returns a pointer to the rest.Config struct if found, or nil if the cluster name does not exist in the Configs map.
func (c Configs) GetConfig(clusterName string) *rest.Config {
	return c[clusterName]
}

// GetClusterNames returns a slice of all cluster names present in the Configs map.
func (c Configs) GetClusterNames() []string {
	return funk.Keys(c).([]string)
}
