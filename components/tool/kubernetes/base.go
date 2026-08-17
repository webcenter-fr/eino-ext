// Package kubernetes provides eino tools for Kubernetes resource management.
package kubernetes

import (
	"context"
	"time"

	"emperror.dev/errors"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/toolutil"
)

const (
	defaultExecTimeout      = 60 * time.Second
	defaultOperationTimeout = 30 * time.Second
)

// baseTool holds shared client bundles for all Kubernetes tools.
type baseTool struct {
	clients              map[string]client.Client
	clientsets           map[string]*kubernetes.Clientset
	dynamics             map[string]dynamic.Interface
	mappers              map[string]*cachedMapper
	configs              Configs
	knownClusters        []string
	disallowedNamespaces map[string]map[string]bool
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

// dynamicClient returns the dynamic client for the given cluster name.
func (b *baseTool) dynamicClient(cluster string) (dynamic.Interface, error) {
	c, ok := b.dynamics[cluster]
	if !ok {
		return nil, clusterNotFoundError(cluster, b.knownClusters)
	}
	return c, nil
}

// resolveKind resolves a resource kind to GVR, GVK, and scope via the
// per-cluster cached mapper.
func (b *baseTool) resolveKind(ctx context.Context, cluster, kind string) (resolveResult, error) {
	mapper, ok := b.mappers[cluster]
	if !ok {
		return resolveResult{}, clusterNotFoundError(cluster, b.knownClusters)
	}
	resolved, err := mapper.Resolve(ctx, kind)
	if err != nil {
		return resolveResult{}, errors.Wrap(err, "failed to resolve kind")
	}
	return resolved, nil
}

// newBaseTool builds the controller-runtime clients, dynamic clients, and mappers for all configured clusters.
func newBaseTool(ctx context.Context, configs Configs) (*baseTool, error) {
	clients, err := BuildClients(ctx, configs, nil)
	if err != nil {
		return nil, err
	}
	dynamics, err := BuildClientDynamics(ctx, configs, nil)
	if err != nil {
		return nil, err
	}
	mappers := make(map[string]*cachedMapper, len(configs))
	for name, cc := range configs {
		if cc == nil {
			continue
		}
		m, err := newCachedMapper(cc.Config)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to create mapper for cluster %s", name)
		}
		mappers[name] = m
	}
	return &baseTool{
		clients:              clients,
		dynamics:             dynamics,
		mappers:              mappers,
		configs:              configs,
		knownClusters:        configs.GetClusterNames(),
		disallowedNamespaces: buildDisallowedNamespaces(configs),
	}, nil
}
