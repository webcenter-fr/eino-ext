package kubernetes

import "strings"

// projectFields returns a new object containing only the given dot paths (with
// their parent keys). Missing paths are omitted. Array indexes are not supported.
func projectFields(obj map[string]any, fields []string) map[string]any {
	out := map[string]any{}
	for _, path := range fields {
		parts := strings.Split(path, ".")
		src := obj
		dst := out
		for i, part := range parts {
			v, exists := src[part]
			if !exists {
				break
			}
			if i == len(parts)-1 {
				dst[part] = v
				break
			}
			child, isMap := v.(map[string]any)
			if !isMap {
				break
			}
			next, isMap := dst[part].(map[string]any)
			if !isMap {
				next = map[string]any{}
				dst[part] = next
			}
			dst = next
			src = child
		}
	}
	return out
}
