package kubernetes

import (
	"context"
	"sort"
	"strings"
	"sync"

	"emperror.dev/errors"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/kretry"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	discovery  discovery.DiscoveryInterface
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
	return &cachedMapper{delegate: delegate, restMapper: expander, discovery: dc}, nil
}

// builtinGroups is the set of API groups whose resources win when a bare kind
// matches several groups, exactly one of which is a built-in Kubernetes group.
var builtinGroups = map[string]bool{
	"":                          true, // core
	"apps":                      true,
	"batch":                     true,
	"networking.k8s.io":         true,
	"rbac.authorization.k8s.io": true,
	"policy":                    true,
	"autoscaling":               true,
	"storage.k8s.io":            true,
	"apiextensions.k8s.io":      true,
	"coordination.k8s.io":       true,
	"discovery.k8s.io":          true,
}

// gvrCandidate is a resolved resource kept for stable ambiguity reporting.
type gvrCandidate struct {
	gvr        schema.GroupVersionResource
	group      string
	version    string
	plural     string
	namespaced bool
}

// resourceMatches reports whether the APIResource matches the given kind string,
// comparing case-insensitively against Kind, Name (plural), SingularName, and
// kubectl shortnames (e.g. "po", "deploy").
func resourceMatches(r metav1.APIResource, kind string) bool {
	k := strings.ToLower(kind)
	if strings.ToLower(r.Kind) == k ||
		strings.ToLower(r.Name) == k ||
		strings.ToLower(r.SingularName) == k {
		return true
	}
	for _, short := range r.ShortNames {
		if strings.ToLower(short) == k {
			return true
		}
	}
	return false
}

// resultFromResource builds a resolveResult from a discovered APIResource.
func resultFromResource(gv schema.GroupVersion, r metav1.APIResource) resolveResult {
	return resolveResult{
		GVR:    schema.GroupVersionResource{Group: gv.Group, Version: gv.Version, Resource: r.Name},
		GVK:    schema.GroupVersionKind{Group: gv.Group, Version: gv.Version, Kind: r.Kind},
		Scoped: r.Namespaced,
	}
}

// preferredCandidates enumerates every non-subresource resource in the server's
// preferred group/versions that matches kind, returning the resolved results
// alongside the candidate metadata used for ambiguity reporting.
func (cm *cachedMapper) preferredCandidates(kind string) ([]resolveResult, []gvrCandidate, error) {
	lists, err := cm.discovery.ServerPreferredResources()
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to discover server resources")
	}

	var results []resolveResult
	var candidates []gvrCandidate
	for _, list := range lists {
		if list == nil {
			continue
		}
		gv, err := schema.ParseGroupVersion(list.GroupVersion)
		if err != nil {
			continue
		}
		for _, r := range list.APIResources {
			if strings.Contains(r.Name, "/") || !resourceMatches(r, kind) {
				continue
			}
			res := resultFromResource(gv, r)
			results = append(results, res)
			candidates = append(candidates, gvrCandidate{
				gvr:        res.GVR,
				group:      gv.Group,
				version:    gv.Version,
				plural:     r.Name,
				namespaced: r.Namespaced,
			})
		}
	}
	return results, candidates, nil
}

// resolve dispatches to the strategy selected by the caller-supplied
// disambiguation hints. apiVersion wins over a dotted kind.
func (cm *cachedMapper) resolve(kind, apiVersion string) (resolveResult, error) {
	if apiVersion != "" {
		return cm.resolveWithAPIVersion(kind, apiVersion)
	}
	if idx := strings.Index(kind, "."); idx > 0 && idx < len(kind)-1 {
		return cm.resolveDotted(kind, idx)
	}
	return cm.resolveBare(kind)
}

// resolveWithAPIVersion finds the resource whose kind string matches within the
// explicitly requested group/version.
func (cm *cachedMapper) resolveWithAPIVersion(kind, apiVersion string) (resolveResult, error) {
	gv, err := schema.ParseGroupVersion(apiVersion)
	if err != nil {
		return resolveResult{}, errors.Wrapf(err, "invalid apiVersion %q", apiVersion)
	}

	resources, err := cm.discovery.ServerResourcesForGroupVersion(gv.String())
	if err != nil || resources == nil {
		return resolveResult{}, errors.Errorf("kind %q not found in %s", kind, gv.String())
	}
	for _, r := range resources.APIResources {
		if strings.Contains(r.Name, "/") {
			continue
		}
		if resourceMatches(r, kind) {
			return resultFromResource(gv, r), nil
		}
	}
	return resolveResult{}, errors.Errorf("kind %q not found in %s", kind, gv.String())
}

// resolveDotted handles the "<name>.<group>" form (e.g. "kafkas.kafka.strimzi.io"
// or "Kafka.kafka.strimzi.io"), using the group's preferred version.
func (cm *cachedMapper) resolveDotted(kind string, idx int) (resolveResult, error) {
	name := kind[:idx]
	group := kind[idx+1:]

	results, candidates, err := cm.preferredCandidates(name)
	if err != nil {
		return resolveResult{}, err
	}
	for i := range results {
		if candidates[i].group == group {
			return results[i], nil
		}
	}
	return resolveResult{}, errors.Errorf("unknown kind %q", kind)
}

// resolveBare resolves a bare kind across every preferred group, preferring a
// unique built-in match and otherwise reporting the ambiguity.
func (cm *cachedMapper) resolveBare(kind string) (resolveResult, error) {
	results, candidates, err := cm.preferredCandidates(kind)
	if err != nil {
		return resolveResult{}, err
	}

	switch len(results) {
	case 0:
		return resolveResult{}, errors.Errorf("unknown kind %q", kind)
	case 1:
		return results[0], nil
	default:
		var builtin []resolveResult
		for _, r := range results {
			if builtinGroups[r.GVR.Group] {
				builtin = append(builtin, r)
			}
		}
		if len(builtin) == 1 {
			return builtin[0], nil
		}
		return resolveResult{}, ambiguousKindError(kind, candidates)
	}
}

// ambiguousKindError builds the actionable error for a bare kind that matches
// several API groups, listing candidates sorted by plural.group.
func ambiguousKindError(kind string, candidates []gvrCandidate) error {
	sorted := make([]gvrCandidate, len(candidates))
	copy(sorted, candidates)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].plural+"."+sorted[i].group < sorted[j].plural+"."+sorted[j].group
	})

	listed := make([]string, 0, len(sorted))
	for _, c := range sorted {
		listed = append(listed, c.plural+"."+c.group+" ("+c.version+")")
	}

	example := sorted[0]
	return errors.Errorf(
		"kind %q is ambiguous: it exists in several API groups: %s. Retry with apiVersion set (e.g. %q) or kind %q.",
		kind, strings.Join(listed, ", "),
		example.group+"/"+example.version,
		example.plural+"."+example.group,
	)
}

// Resolve maps a kind string to a GVR/GVK deterministically. apiVersion (e.g.
// "kafka.strimzi.io/v1beta2" or "v1") disambiguates kinds that exist in several
// API groups; a bare ambiguous kind returns an error listing the candidates.
func (cm *cachedMapper) Resolve(ctx context.Context, kind, apiVersion string) (resolveResult, error) {
	var result resolveResult
	err := kretry.Retry(ctx, func(_ context.Context) error {
		res, err := cm.resolve(kind, apiVersion)
		if meta.IsNoMatchError(err) {
			cm.Reset()
			res, err = cm.resolve(kind, apiVersion)
		}
		if err != nil {
			return err
		}
		result = res
		return nil
	})
	return result, err
}

func (cm *cachedMapper) Reset() {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.delegate.Reset()
}
