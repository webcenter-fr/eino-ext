package kubernetes

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func projectionFixture() map[string]any {
	return map[string]any{
		"apiVersion": "v1",
		"kind":       "Widget",
		"metadata":   map[string]any{"name": "w1", "namespace": "default"},
		"spec": map[string]any{
			"kafka": map[string]any{
				"storage":   "10Gi",
				"resources": map[string]any{"cpu": "1"},
			},
		},
		"status": map[string]any{"ready": true},
	}
}

func TestProjectFields_NestedPath(t *testing.T) {
	out := projectFields(projectionFixture(), []string{"spec.kafka.storage"})

	expected := map[string]any{
		"spec": map[string]any{
			"kafka": map[string]any{"storage": "10Gi"},
		},
	}
	assert.Equal(t, expected, out)
}

func TestProjectFields_MissingPathOmitted(t *testing.T) {
	out := projectFields(projectionFixture(), []string{"nope", "spec.nope"})

	_, hasTopLevel := out["nope"]
	assert.False(t, hasTopLevel, "a missing top-level path is omitted")

	spec, ok := out["spec"].(map[string]any)
	assert.True(t, ok)
	_, hasNested := spec["nope"]
	assert.False(t, hasNested, "a missing nested path is omitted")
}

func TestProjectFields_ThroughNonMapOmitted(t *testing.T) {
	obj := map[string]any{
		"spec": "not-a-map",
		"list": []any{map[string]any{"a": float64(1)}},
	}

	out := projectFields(obj, []string{"spec.deep", "list.0.a"})

	assert.Empty(t, out, "paths through a scalar or array are omitted")
}

func TestProjectFields_EmptyFields(t *testing.T) {
	out := projectFields(projectionFixture(), nil)
	assert.Empty(t, out)
}

func TestProjectFields_NilMetadata(t *testing.T) {
	obj := map[string]any{"metadata": nil}

	assert.NotPanics(t, func() {
		out := projectFields(obj, []string{"metadata.name"})
		assert.Empty(t, out)
	})
}
