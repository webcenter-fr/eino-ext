// Package toolutil provides small shared helpers for tool components:
// consistent "not found" errors for named instances/clusters, sorted map
// key extraction for configuration maps, and JSON unmarshal helpers.
package toolutil

import (
	"context"
	"sort"
	"strings"

	"emperror.dev/errors"
)

// NotFoundError returns a consistent error indicating that a named entity of
// the given kind was not found, listing the known values. Example:
//
//	toolutil.NotFoundError("ArgoCD instance", "prod", []string{"dev", "staging"})
func NotFoundError(kind, name string, known []string) error {
	return errors.Errorf("%s not found: %s. Must be one of: %s", kind, name, strings.Join(known, ", "))
}

// EmptyJSONUnmarshaler returns an eino UnmarshalArguments callback for tools
// whose parameter struct is empty (struct{}). The sonic JSON library that eino
// uses rejects empty argument strings, so a custom unmarshaler is needed to
// handle the case where a no-argument tool receives "" instead of "{}".
//
// The type parameter T must match the function input type that InferTool
// infers from the tool's Invoke method — typically *FooParams where FooParams
// is struct{}. Example:
//
//	InferTool(name, desc, fn,
//	    WithUnmarshalArguments(toolutil.EmptyJSONUnmarshaler[*InstanceListParams]()),
//	)
func EmptyJSONUnmarshaler[T any]() func(ctx context.Context, args string) (any, error) {
	return func(_ context.Context, _ string) (any, error) {
		var zero T
		return zero, nil
	}
}

// SortedKeys returns the keys of a string-keyed map in ascending order.
func SortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
