package kubernetes

import (
	"emperror.dev/errors"
	"github.com/pmezard/go-difflib/difflib"
	"go.yaml.in/yaml/v3"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// diffMaxBytes caps the unified diff returned to the LLM.
const diffMaxBytes = 64 * 1024

// diffTruncatedMarker is appended when a diff exceeds diffMaxBytes.
const diffTruncatedMarker = "... diff truncated"

// normalizeForDiff returns a YAML rendering of obj with volatile fields removed:
// metadata.managedFields, metadata.resourceVersion, metadata.generation,
// metadata.uid, metadata.creationTimestamp, and status.
func normalizeForDiff(obj *unstructured.Unstructured) (string, error) {
	o := obj.DeepCopy().Object
	unstructured.RemoveNestedField(o, "metadata", "managedFields")
	unstructured.RemoveNestedField(o, "metadata", "resourceVersion")
	unstructured.RemoveNestedField(o, "metadata", "generation")
	unstructured.RemoveNestedField(o, "metadata", "uid")
	unstructured.RemoveNestedField(o, "metadata", "creationTimestamp")
	delete(o, "status")
	data, err := yaml.Marshal(o)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal object to YAML for diff")
	}
	return string(data), nil
}

// unifiedDiff returns a unified diff (3 lines context) from before to after,
// truncated to diffMaxBytes with a trailing diffTruncatedMarker.
func unifiedDiff(before, after string) string {
	diff, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A:        difflib.SplitLines(before),
		B:        difflib.SplitLines(after),
		FromFile: "current",
		ToFile:   "after",
		Context:  3,
	})
	if err != nil {
		return ""
	}
	if len(diff) > diffMaxBytes {
		diff = diff[:diffMaxBytes] + "\n" + diffTruncatedMarker
	}
	return diff
}

// resourceDiff computes the normalized unified diff between two unstructured objects.
func resourceDiff(before, after *unstructured.Unstructured) (string, error) {
	b, err := normalizeForDiff(before)
	if err != nil {
		return "", err
	}
	a, err := normalizeForDiff(after)
	if err != nil {
		return "", err
	}
	return unifiedDiff(b, a), nil
}
