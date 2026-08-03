package kubernetes

import (
	"context"
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

func (cm *cachedMapper) Resolve(ctx context.Context, kind string) (resolveResult, error) {
	var result resolveResult
	err := kretry.Retry(ctx, func(retryCtx context.Context) error {
		gk := schema.GroupKind{Group: "", Kind: kind}
		mapping, err := cm.restMapper.RESTMapping(gk)
		if err != nil {
			if meta.IsNoMatchError(err) {
				cm.Reset()
				mapping, err = cm.restMapper.RESTMapping(gk)
			}
			if err != nil {
				return errors.Wrapf(err, "failed to resolve kind %q", kind)
			}
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
