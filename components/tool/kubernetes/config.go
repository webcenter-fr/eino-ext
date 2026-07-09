package kubernetes

import (
	"k8s.io/client-go/rest"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/toolutil"
)

// ClusterConfig wraps a rest.Config with security and performance options.
type ClusterConfig struct {
	*rest.Config
	DisallowedNamespaces []string `validate:"omitempty"`
	DefaultTimeout       string   `validate:"omitempty" jsonschema:"description=Default timeout for operations (Go duration string, e.g. '30s')"`
}

// Configs is a map of Kubernetes cluster configurations, where the key is the cluster name.
// Each *ClusterConfig wraps a *rest.Config with security controls such as
// disallowed namespaces and default timeouts.
//
// Example:
//
//	configs := kubernetes.Configs{
//	    "prod": &kubernetes.ClusterConfig{
//	        Config:               prodRestConfig,
//	        DisallowedNamespaces: []string{"kube-system", "kube-public"},
//	        DefaultTimeout:       "30s",
//	    },
//	}
type Configs map[string]*ClusterConfig

// GetConfig retrieves the configuration for a given cluster name. It returns a pointer to the ClusterConfig struct if found, or nil if the cluster name does not exist in the Configs map.
func (c Configs) GetConfig(clusterName string) *ClusterConfig {
	return c[clusterName]
}

// GetClusterNames returns a slice of all cluster names present in the Configs map.
func (c Configs) GetClusterNames() []string {
	return toolutil.SortedKeys(c)
}
