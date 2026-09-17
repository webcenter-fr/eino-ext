package kubernetes

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func rawDescribeJSON(t *testing.T, u *unstructured.Unstructured, exclude []string) map[string]any {
	t.Helper()
	s, err := marshalRawDescribeOutput(u, exclude)
	require.NoError(t, err)
	return mustUnmarshal(t, []byte(s))
}

func TestMarshalRawDescribeOutput_ValidatingWebhookConfiguration(t *testing.T) {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "admissionregistration.k8s.io/v1",
		"kind":       "ValidatingWebhookConfiguration",
		"metadata":   map[string]any{"name": "trust-manager"},
		"webhooks": []any{
			map[string]any{
				"name": "trust-manager.cert-manager.io",
				"clientConfig": map[string]any{
					"service":  map[string]any{"name": "cert-manager", "namespace": "cert-manager", "path": "/validate"},
					"caBundle": "LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0t",
				},
				"rules": []any{map[string]any{"apiGroups": []any{"*"}, "apiVersions": []any{"*"}, "operations": []any{"CREATE", "UPDATE"}, "resources": []any{"certificates"}}},
			},
		},
	}}

	m := rawDescribeJSON(t, u, nil)

	assert.Equal(t, "ValidatingWebhookConfiguration", m["kind"])
	assert.Equal(t, "admissionregistration.k8s.io/v1", m["apiVersion"])

	webhooks, ok := m["webhooks"].([]any)
	require.True(t, ok, "webhooks must be surfaced as an array")
	require.Len(t, webhooks, 1)

	clientConfig := webhooks[0].(map[string]any)["clientConfig"].(map[string]any)
	assert.Equal(t, "LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0t", clientConfig["caBundle"])

	_, hasSpec := m["spec"]
	assert.False(t, hasSpec, "ValidatingWebhookConfiguration has no spec")
}

func TestMarshalRawDescribeOutput_ExcludeFields(t *testing.T) {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "example.com/v1",
		"kind":       "Widget",
		"metadata":   map[string]any{"name": "w1"},
		"spec":       map[string]any{"size": float64(1)},
		"status":     map[string]any{"ready": true},
		"data":       map[string]any{"key": "value"},
		"webhooks":   []any{map[string]any{"name": "wh"}},
	}}

	m := rawDescribeJSON(t, u, []string{"metadata", "spec", "status", "data"})

	for _, field := range []string{"metadata", "spec", "status", "data"} {
		_, ok := m[field]
		assert.False(t, ok, "%s should be excluded", field)
	}
	assert.NotNil(t, m["webhooks"], "non-excludable fields must be preserved")
}

func TestMarshalRawDescribeOutput_InvalidExcludeField(t *testing.T) {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata":   map[string]any{"name": "cm1"},
	}}

	_, err := marshalRawDescribeOutput(u, []string{"bogus"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bogus")
	assert.Contains(t, err.Error(), "metadata, spec, status, data")
}

func TestMarshalRawDescribeOutput_ClusterScopedNoSpec(t *testing.T) {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion":    "scheduling.k8s.io/v1",
		"kind":          "PriorityClass",
		"metadata":      map[string]any{"name": "high-priority"},
		"value":         float64(1000000),
		"globalDefault": false,
	}}

	m := rawDescribeJSON(t, u, nil)

	assert.Equal(t, float64(1000000), m["value"])
	assert.Equal(t, false, m["globalDefault"])
	_, hasSpec := m["spec"]
	assert.False(t, hasSpec)
}

func TestMarshalRawDescribeOutput_PreservesUnknownCRDFields(t *testing.T) {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "example.com/v1",
		"kind":       "Widget",
		"metadata":   map[string]any{"name": "w1"},
		"spec":       map[string]any{"size": float64(3), "nested": map[string]any{"a": "b"}},
		"config":     map[string]any{"enabled": true, "mode": "fast"},
	}}

	m := rawDescribeJSON(t, u, nil)

	assert.Equal(t, map[string]any{"size": float64(3), "nested": map[string]any{"a": "b"}}, m["spec"])
	assert.Equal(t, map[string]any{"enabled": true, "mode": "fast"}, m["config"])
}

func TestMarshalRawDescribeOutput_DoesNotMutateInput(t *testing.T) {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata":   map[string]any{"name": "cm1"},
		"data":       map[string]any{"key": "value"},
	}}

	_ = rawDescribeJSON(t, u, []string{"metadata"})

	_, ok := u.Object["metadata"]
	assert.True(t, ok, "input object must not be mutated by exclusions")
}

func TestMarshalRawDescribeOutput_EmptyExclusions(t *testing.T) {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata":   map[string]any{"name": "cm1"},
		"data":       map[string]any{"key": "value"},
	}}

	m := rawDescribeJSON(t, u, nil)

	assert.Equal(t, "ConfigMap", m["kind"])
	assert.Equal(t, "v1", m["apiVersion"])
	assert.Equal(t, map[string]any{"name": "cm1"}, m["metadata"])
	assert.Equal(t, map[string]any{"key": "value"}, m["data"])
}

func TestRedactSecretData(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want any
	}{
		{
			name: "map values are redacted",
			in:   map[string]any{"a": "x", "b": "y"},
			want: map[string]any{"a": "REDACTED", "b": "REDACTED"},
		},
		{
			name: "non-map is a no-op",
			in:   "not-a-map",
			want: "not-a-map",
		},
		{
			name: "nil is a no-op",
			in:   nil,
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.NotPanics(t, func() { redactSecretData(tt.in) })
			assert.Equal(t, tt.want, tt.in)
		})
	}
}
