package kubernetes

import (
	"context"
	"strings"
	"sync"

	"emperror.dev/errors"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/kretry"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
)

// kindFormatSentence is the shared description text for how the 'kind'
// parameter accepts various formats. It is used by all Kubernetes tool
// Description constants to avoid duplication and prevent drift.
const kindFormatSentence = "The 'kind' parameter accepts a PascalCase singular kind (e.g. 'Pod', 'Deployment', 'ConfigMap'), a kubectl shortname ('po', 'deploy'), or a 'resource.group' form ('deployments.apps'). Plural resource names ('pods') are accepted but PascalCase is preferred."

type resolveResult struct {
	GVR    schema.GroupVersionResource
	GVK    schema.GroupVersionKind
	Scoped bool
}

type cachedMapper struct {
	delegate   *restmapper.DeferredDiscoveryRESTMapper
	restMapper meta.RESTMapper
	mu         sync.Mutex
}

func newCachedMapper(config *rest.Config) (*cachedMapper, error) {
	dc, err := discovery.NewDiscoveryClientForConfig(config)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create discovery client")
	}
	mem := memory.NewMemCacheClient(dc)
	delegate := restmapper.NewDeferredDiscoveryRESTMapper(mem)
	expander := restmapper.NewShortcutExpander(delegate, mem, func(s string) {})
	return &cachedMapper{delegate: delegate, restMapper: expander}, nil
}

// tryResolve attempts to resolve a kind string via multiple strategies:
//  1. As a Kind (PascalCase singular, e.g. "Pod", "Deployment") via RESTMapping.
//  2. As a resource name (lowercase plural, e.g. "pods", "deployments") via KindFor.
//  3. As a "resource.group" form (e.g. "deployments.apps") via KindFor with group.
//  4. As a "kind.group" form (e.g. "Deployment.apps") — KindFor also handles this
//     because it matches by resource name, not Kind.
func tryResolve(mapper meta.RESTMapper, kind string) (*meta.RESTMapping, error) {
	// Strategy 1: try as a Kind (handles "Pod", "Deployment", shortcuts like "po").
	gk := schema.GroupKind{Group: "", Kind: kind}
	mapping, err := mapper.RESTMapping(gk)
	if err == nil {
		return mapping, nil
	}
	if !meta.IsNoMatchError(err) {
		return nil, err
	}

	// Strategy 2: try as a resource name (handles "pods", "deployments").
	// KindFor looks up the Kind from the lowercase plural resource name.
	gvr := schema.GroupVersionResource{Group: "", Resource: kind}
	gvk, gvkErr := mapper.KindFor(gvr)
	if gvkErr == nil {
		gk2 := schema.GroupKind{Group: gvk.Group, Kind: gvk.Kind}
		return mapper.RESTMapping(gk2)
	}

	// Strategy 3: try "resource.group" or "kind.group" form (e.g. "deployments.apps",
	// "certificates.cert-manager.io"). Split on the first dot to correctly separate the
	// resource name from dotted group names like "cert-manager.io" or "networking.k8s.io".
	if idx := strings.Index(kind, "."); idx > 0 && idx < len(kind)-1 {
		prefix := kind[:idx] // may be a resource name or a Kind
		group := kind[idx+1:]
		gvr2 := schema.GroupVersionResource{Group: group, Resource: prefix}
		gvk, gvkErr = mapper.KindFor(gvr2)
		if gvkErr == nil {
			gk2 := schema.GroupKind{Group: gvk.Group, Kind: gvk.Kind}
			return mapper.RESTMapping(gk2)
		}
		// Also try prefix as a Kind directly with the group (e.g. "Deployment.apps").
		gk3 := schema.GroupKind{Group: group, Kind: prefix}
		mapping2, err2 := mapper.RESTMapping(gk3)
		if err2 == nil {
			return mapping2, nil
		}
	}

	return nil, err
}

func (cm *cachedMapper) Resolve(ctx context.Context, kind string) (resolveResult, error) {
	var result resolveResult
	err := kretry.Retry(ctx, func(retryCtx context.Context) error {
		mapping, err := tryResolve(cm.restMapper, kind)
		if meta.IsNoMatchError(err) {
			cm.Reset()
			mapping, err = tryResolve(cm.restMapper, kind)
		}
		if err != nil {
			return errors.Wrapf(err, "failed to resolve kind %q", kind)
		}

		gvr := mapping.Resource
		gvk := mapping.GroupVersionKind
		if gvk.Kind == "" {
			gvk = schema.GroupVersionKind{
				Group:   gvr.Group,
				Version: gvr.Version,
				Kind:    kind,
			}
		}
		result = resolveResult{
			GVR:    gvr,
			GVK:    gvk,
			Scoped: mapping.Scope.Name() == meta.RESTScopeNameNamespace,
		}
		return nil
	})
	return result, err
}

func (cm *cachedMapper) Reset() {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.delegate.Reset()
}
