package kubernetes

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/toolutil"
)

// toGVR builds a schema.GroupVersionResource from its string parts. It
// centralizes the group/version/resource field naming used by the generic
// resource tools.
func toGVR(group, version, resource string) schema.GroupVersionResource {
	return schema.GroupVersionResource{
		Group:    group,
		Version:  version,
		Resource: resource,
	}
}

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
	return toolutil.NotFoundError("Kubernetes cluster", cluster, known)
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

// newBaseToolWithDynamic builds both the controller-runtime base tool and the
// dynamic client bundle used by the generic resource tools.
func newBaseToolWithDynamic(configs Configs) (*baseToolWithDynamic, error) {
	dynamics, err := BuildClientDynamics(configs, nil)
	if err != nil {
		return nil, err
	}
	base, err := newBaseTool(configs)
	if err != nil {
		return nil, err
	}
	return &baseToolWithDynamic{
		baseTool: base,
		dynamics: dynamics,
	}, nil
}
