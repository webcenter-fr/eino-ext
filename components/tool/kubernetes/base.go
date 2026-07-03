package kubernetes

import (
	"strings"

	"emperror.dev/errors"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// baseTool holds shared client bundles for all Kubernetes tools.
type baseTool struct {
	clients       map[string]client.Client
	clientsets    map[string]*kubernetes.Clientset
	configs       Configs
	knownClusters []string
}

// client returns the controller-runtime client for the given cluster name.
func (b *baseTool) client(cluster string) (client.Client, error) {
	c, ok := b.clients[cluster]
	if !ok {
		return nil, clusterNotFoundError(cluster, b.knownClusters)
	}
	return c, nil
}

// clientset returns the typed Kubernetes clientset for the given cluster name.
func (b *baseTool) clientset(cluster string) (*kubernetes.Clientset, error) {
	c, ok := b.clientsets[cluster]
	if !ok {
		return nil, clusterNotFoundError(cluster, b.knownClusters)
	}
	return c, nil
}

// restConfig returns the *rest.Config for the given cluster name.
func (b *baseTool) restConfig(cluster string) (*rest.Config, error) {
	config := b.configs.GetConfig(cluster)
	if config == nil {
		return nil, clusterNotFoundError(cluster, b.knownClusters)
	}
	return config, nil
}

// clusterNotFoundError returns a formatted error for an unknown cluster.
func clusterNotFoundError(cluster string, known []string) error {
	return errors.Errorf("Kubernetes cluster not found: %s. Cluster must be one of: %s", cluster, strings.Join(known, ", "))
}

// baseToolWithDynamic extends baseTool with a dynamic client bundle.
type baseToolWithDynamic struct {
	*baseTool
	dynamics map[string]dynamic.Interface
}

// dynamicClient returns the dynamic client for the given cluster name.
func (b *baseToolWithDynamic) dynamicClient(cluster string) (dynamic.Interface, error) {
	c, ok := b.dynamics[cluster]
	if !ok {
		return nil, clusterNotFoundError(cluster, b.knownClusters)
	}
	return c, nil
}

// newBaseTool builds the controller-runtime clients for all configured clusters.
// Only clients are built by default; callers needing clientsets must build them separately.
func newBaseTool(configs Configs) (*baseTool, error) {
	clients, err := BuildClients(configs, nil)
	if err != nil {
		return nil, err
	}
	return &baseTool{
		clients:       clients,
		configs:       configs,
		knownClusters: configs.GetClusterNames(),
	}, nil
}
