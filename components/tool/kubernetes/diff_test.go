package kubernetes

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestNormalizeForDiff_StripsVolatileFields(t *testing.T) {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Pod",
		"metadata": map[string]any{
			"name":              "p1",
			"managedFields":     []any{map[string]any{"manager": "kubectl"}},
			"resourceVersion":   "12345",
			"generation":        float64(7),
			"uid":               "0f8f8f8f-0000-0000-0000-000000000000",
			"creationTimestamp": "2024-01-01T00:00:00Z",
		},
		"status": map[string]any{"phase": "Running"},
		"spec":   map[string]any{"replicas": float64(1)},
	}}

	out, err := normalizeForDiff(u)
	require.NoError(t, err)

	for _, volatile := range []string{
		"managedFields", "resourceVersion", "generation", "uid", "creationTimestamp", "status",
	} {
		assert.NotContains(t, out, volatile, "%s must be stripped", volatile)
	}
	assert.Contains(t, out, "replicas")
}

func TestResourceDiff_SingleAddition(t *testing.T) {
	before := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata":   map[string]any{"name": "cm1", "namespace": "default"},
		"data":       map[string]any{"key": "value"},
	}}
	after := before.DeepCopy()
	after.SetAnnotations(map[string]string{"added": "true"})

	diff, err := resourceDiff(before, after)
	require.NoError(t, err)
	require.NotEmpty(t, diff)

	// The change adds lines only; nothing is removed.
	plus, minus := 0, 0
	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"):
		case strings.HasPrefix(line, "+"):
			plus++
		case strings.HasPrefix(line, "-"):
			minus++
		}
	}
	assert.GreaterOrEqual(t, plus, 1, "the added annotation must appear as an addition")
	assert.Equal(t, 0, minus, "a pure addition must not remove any line")
}

func TestUnifiedDiff_Truncation(t *testing.T) {
	var beforeB, afterB strings.Builder
	for i := 0; i < 20000; i++ {
		fmt.Fprintf(&beforeB, "line-%d\n", i)
		fmt.Fprintf(&afterB, "changed-%d\n", i)
	}

	diff := unifiedDiff(beforeB.String(), afterB.String())

	assert.True(t, strings.HasSuffix(diff, diffTruncatedMarker), "large diffs must be marked as truncated")
	assert.LessOrEqual(t, len(diff), diffMaxBytes+len(diffTruncatedMarker)+1)
}
