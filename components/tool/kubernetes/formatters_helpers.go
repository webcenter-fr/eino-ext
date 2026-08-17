package kubernetes

import (
	"math"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Nil-safe access helpers for reading fields from unstructured Kubernetes
// objects. Every helper returns a zero value on a nil object, missing key, or
// unexpected type, so formatters never panic on malformed or partial objects.
// Reading a nil map is safe in Go, so the map-based helpers need no explicit
// nil guard.

func uSpec(u *unstructured.Unstructured) map[string]any {
	if u == nil {
		return nil
	}
	spec, _ := u.Object["spec"].(map[string]any)
	return spec
}

func uStatus(u *unstructured.Unstructured) map[string]any {
	if u == nil {
		return nil
	}
	status, _ := u.Object["status"].(map[string]any)
	return status
}

func uString(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

func uBool(m map[string]any, key string) bool {
	b, _ := m[key].(bool)
	return b
}

// uInt32 converts JSON numbers (float64) and Go ints to int32, returning 0 for
// any other type or missing key. Out-of-range values are clamped to the int32
// range to avoid implementation-defined wrap-around (CWE-190) when a malformed
// or hostile CRD supplies an extreme number.
func uInt32(m map[string]any, key string) int32 {
	switch n := m[key].(type) {
	case float64:
		return clampInt32(n)
	case int64:
		return clampInt32(float64(n))
	case int:
		return clampInt32(float64(n))
	default:
		return 0
	}
}

// clampInt32 clamps a float64 to the int32 range. NaN/Inf (which cannot come
// from valid JSON but may appear via Go-typed inputs) map to 0.
func clampInt32(n float64) int32 {
	if math.IsNaN(n) || math.IsInf(n, 0) {
		return 0
	}
	if n > float64(math.MaxInt32) {
		return math.MaxInt32
	}
	if n < float64(math.MinInt32) {
		return math.MinInt32
	}
	return int32(n)
}

func uMap(m map[string]any, key string) map[string]any {
	mm, _ := m[key].(map[string]any)
	return mm
}

func uSlice(m map[string]any, key string) []any {
	s, _ := m[key].([]any)
	return s
}

func uStringSlice(m map[string]any, key string) []string {
	raw, _ := m[key].([]any)
	if raw == nil {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, e := range raw {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func uStringMap(m map[string]any, key string) map[string]string {
	raw := uMap(m, key)
	if raw == nil {
		return nil
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

// uMapSlice extracts key as a []any and keeps only the map[string]any elements,
// skipping non-map entries.
func uMapSlice(m map[string]any, key string) []map[string]any {
	return mapSlice(uSlice(m, key))
}

// mapSlice filters raw to its map[string]any elements.
func mapSlice(raw []any) []map[string]any {
	if raw == nil {
		return nil
	}
	out := make([]map[string]any, 0, len(raw))
	for _, r := range raw {
		if m, ok := r.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}
