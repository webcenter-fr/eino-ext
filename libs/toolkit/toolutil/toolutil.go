// Package toolutil provides small shared helpers for tool components:
// consistent "not found" errors for named instances/clusters and sorted map
// key extraction for configuration maps.
package toolutil

import (
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

// SortedKeys returns the keys of a string-keyed map in ascending order.
func SortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
