package kubernetes

import (
	"context"
	"time"

	"emperror.dev/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/toolutil"
)

const (
	defaultExecTimeout    = 60 * time.Second
	defaultOperationTimeout = 30 * time.Second
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
	clients               map[string]client.Client
	clientsets            map[string]*kubernetes.Clientset
	configs               Configs
	knownClusters         []string
	disallowedNamespaces  map[string]map[string]bool
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
	return config.Config, nil
}

// checkNamespace returns an error if the given namespace is disallowed for the
// given cluster. Disallowed namespaces are configured per-cluster via
// ClusterConfig.DisallowedNamespaces.
func (b *baseTool) checkNamespace(cluster, namespace string) error {
	if namespace == "" {
		return nil
	}
	nsMap, ok := b.disallowedNamespaces[cluster]
	if !ok {
		return nil
	}
	if nsMap[namespace] {
		return errors.Errorf("namespace %q is disallowed for cluster %q", namespace, cluster)
	}
	return nil
}

// withTimeout wraps ctx with a per-operation timeout if one is configured.
// Returns the wrapped context and the cancel function. The returned cancel is
// always safe to defer (it is a noop when timeout is zero).
func withTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout > 0 {
		return context.WithTimeout(ctx, timeout)
	}
	return ctx, func() {}
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

func buildDisallowedNamespaces(configs Configs) map[string]map[string]bool {
	result := make(map[string]map[string]bool)
	for clusterName, cc := range configs {
		if len(cc.DisallowedNamespaces) == 0 {
			continue
		}
		nsMap := make(map[string]bool, len(cc.DisallowedNamespaces))
		for _, ns := range cc.DisallowedNamespaces {
			nsMap[ns] = true
		}
		result[clusterName] = nsMap
	}
	return result
}

// parseTimeoutOrDefault parses a duration string, falling back to defaultVal 
// on empty input or parse errors.
func parseTimeoutOrDefault(timeoutStr string, defaultVal time.Duration) time.Duration {
	if timeoutStr == "" {
		return defaultVal
	}
	d, err := time.ParseDuration(timeoutStr)
	if err != nil {
		return defaultVal
	}
	return d
}

// getDefaultTimeout returns the per-cluster configured default timeout, or the
// package-level default.
func (b *baseTool) getDefaultTimeout(cluster string) time.Duration {
	config := b.configs.GetConfig(cluster)
	if config == nil {
		return defaultOperationTimeout
	}
	if config.DefaultTimeout == "" {
		return defaultOperationTimeout
	}
	return parseTimeoutOrDefault(config.DefaultTimeout, defaultOperationTimeout)
}

// newBaseTool builds the controller-runtime clients for all configured clusters.
// Only clients are built by default; callers needing clientsets must build them separately.
func newBaseTool(ctx context.Context, configs Configs) (*baseTool, error) {
	clients, err := BuildClients(ctx, configs, nil)
	if err != nil {
		return nil, err
	}
	return &baseTool{
		clients:              clients,
		configs:              configs,
		knownClusters:        configs.GetClusterNames(),
		disallowedNamespaces: buildDisallowedNamespaces(configs),
	}, nil
}

// newBaseToolWithDynamic builds both the controller-runtime base tool and the
// dynamic client bundle used by the generic resource tools.
func newBaseToolWithDynamic(ctx context.Context, configs Configs) (*baseToolWithDynamic, error) {
	dynamics, err := BuildClientDynamics(ctx, configs, nil)
	if err != nil {
		return nil, err
	}
	base, err := newBaseTool(ctx, configs)
	if err != nil {
		return nil, err
	}
	return &baseToolWithDynamic{
		baseTool: base,
		dynamics: dynamics,
	}, nil
}
