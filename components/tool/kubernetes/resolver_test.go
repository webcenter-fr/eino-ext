package kubernetes

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery/fake"
	clienttesting "k8s.io/client-go/testing"
)

// resolverFakeDiscovery wraps client-go's FakeDiscovery to provide the
// ServerPreferredResources behavior the resolver relies on (the upstream fake
// returns nil for that method).
type resolverFakeDiscovery struct {
	*fake.FakeDiscovery
}

func (f *resolverFakeDiscovery) ServerPreferredResources() ([]*metav1.APIResourceList, error) {
	return f.Resources, nil
}

// resolverResources returns a discovery fixture with two CRDs sharing the kind
// "Kafka" in different groups, a CRD of kind "Event" in another group, and the
// core Pod/Event kinds.
func resolverResources() []*metav1.APIResourceList {
	return []*metav1.APIResourceList{
		{
			GroupVersion: "v1",
			APIResources: []metav1.APIResource{
				{Name: "pods", SingularName: "pod", ShortNames: []string{"po"}, Namespaced: true, Kind: "Pod"},
				{Name: "events", SingularName: "event", Namespaced: true, Kind: "Event"},
			},
		},
		{
			GroupVersion: "kafka.strimzi.io/v1beta2",
			APIResources: []metav1.APIResource{
				{Name: "kafkas", SingularName: "kafka", Namespaced: true, Kind: "Kafka"},
				{Name: "kafkas/status", Namespaced: true, Kind: "Kafka"},
			},
		},
		{
			GroupVersion: "discover.k8s.harmonie-mutuelle.fr/v2",
			APIResources: []metav1.APIResource{
				{Name: "kafkas", SingularName: "kafka", Namespaced: true, Kind: "Kafka"},
			},
		},
		{
			GroupVersion: "events.example.com/v1",
			APIResources: []metav1.APIResource{
				{Name: "events", SingularName: "event", Namespaced: true, Kind: "Event"},
			},
		},
	}
}

func newResolverTestMapper(resources []*metav1.APIResourceList) *cachedMapper {
	return &cachedMapper{
		discovery: &resolverFakeDiscovery{
			FakeDiscovery: &fake.FakeDiscovery{Fake: &clienttesting.Fake{Resources: resources}},
		},
	}
}

func TestResolve_AmbiguousKind(t *testing.T) {
	cm := newResolverTestMapper(resolverResources())

	_, err := cm.Resolve(context.Background(), "Kafka", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ambiguous")
	assert.Contains(t, err.Error(), "kafkas.kafka.strimzi.io")
	assert.Contains(t, err.Error(), "kafkas.discover.k8s.harmonie-mutuelle.fr")
}

func TestResolve_WithAPIVersion(t *testing.T) {
	cm := newResolverTestMapper(resolverResources())

	res, err := cm.Resolve(context.Background(), "Kafka", "kafka.strimzi.io/v1beta2")
	require.NoError(t, err)
	assert.Equal(t, "kafka.strimzi.io", res.GVR.Group)
	assert.Equal(t, "v1beta2", res.GVR.Version)
	assert.Equal(t, "kafkas", res.GVR.Resource)
	assert.Equal(t, "Kafka", res.GVK.Kind)
	assert.True(t, res.Scoped)
}

func TestResolve_DottedKind(t *testing.T) {
	cm := newResolverTestMapper(resolverResources())

	byPlural, err := cm.Resolve(context.Background(), "kafkas.kafka.strimzi.io", "")
	require.NoError(t, err)

	byKind, err := cm.Resolve(context.Background(), "Kafka.kafka.strimzi.io", "")
	require.NoError(t, err)

	assert.Equal(t, byPlural, byKind)
	assert.Equal(t, "kafka.strimzi.io", byPlural.GVR.Group)
	assert.Equal(t, "kafkas", byPlural.GVR.Resource)
}

func TestResolve_CoreKind(t *testing.T) {
	cm := newResolverTestMapper(resolverResources())

	res, err := cm.Resolve(context.Background(), "Pod", "")
	require.NoError(t, err)
	assert.Equal(t, "", res.GVR.Group)
	assert.Equal(t, "v1", res.GVR.Version)
	assert.Equal(t, "pods", res.GVR.Resource)
	assert.Equal(t, "Pod", res.GVK.Kind)
}

func TestResolve_BuiltinGroupPreference(t *testing.T) {
	cm := newResolverTestMapper(resolverResources())

	res, err := cm.Resolve(context.Background(), "Event", "")
	require.NoError(t, err)
	assert.Equal(t, "", res.GVR.Group)
	assert.Equal(t, "v1", res.GVR.Version)
	assert.Equal(t, "events", res.GVR.Resource)
}

func TestResolve_UnknownKind(t *testing.T) {
	cm := newResolverTestMapper(resolverResources())

	_, err := cm.Resolve(context.Background(), "NoSuchKind", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown kind")
}

func TestResolve_APIVersionNotFound(t *testing.T) {
	cm := newResolverTestMapper(resolverResources())

	_, err := cm.Resolve(context.Background(), "Kafka", "example.com/v1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `kind "Kafka" not found in example.com/v1`)
}

func TestResolve_SubresourceIgnored(t *testing.T) {
	cm := newResolverTestMapper(resolverResources())

	res, err := cm.Resolve(context.Background(), "Kafka", "kafka.strimzi.io/v1beta2")
	require.NoError(t, err)
	assert.Equal(t, "kafkas", res.GVR.Resource, "subresources must not be selected")
}

func TestResolve_Shortname(t *testing.T) {
	cm := newResolverTestMapper(resolverResources())

	res, err := cm.Resolve(context.Background(), "po", "")
	require.NoError(t, err)
	assert.Equal(t, "", res.GVR.Group)
	assert.Equal(t, "pods", res.GVR.Resource)
}
