package filter

import (
	"strconv"
	"strings"

	"emperror.dev/errors"
	"github.com/goccy/go-json"
)

// segment is one component of a selector path.
type segment struct {
	field string // empty for the bare "[]" form
	array bool   // true for "[]" / "name[]"
}

// condition is a single selector entry: a path and its pre-normalized expected
// values. A condition with a bareKey set is a recursive search for any field
// with that name at any depth.
type condition struct {
	segments []segment // nil/empty => bare key recursive search
	bareKey  string    // set only for recursive bare keys
	expected []string  // pre-normalized expected values (lowercased canonical)
}

// Selector is a compiled JSON-object selector. All conditions must match.
type Selector struct {
	conditions []condition
}

// compileSelector compiles a decoded JSON object into a Selector.
func compileSelector(m map[string]any) (*Selector, error) {
	s := &Selector{conditions: make([]condition, 0, len(m))}
	for key, expected := range m {
		segs, bareKey, err := parseSelectorPath(key)
		if err != nil {
			return nil, err
		}

		expectedValues := make([]string, 0, 1)
		if arr, ok := asSlice(expected); ok {
			for _, v := range arr {
				expectedValues = append(expectedValues, normalizeValue(v))
			}
		} else {
			expectedValues = append(expectedValues, normalizeValue(expected))
		}

		s.conditions = append(s.conditions, condition{
			segments: segs,
			bareKey:  bareKey,
			expected: expectedValues,
		})
	}
	return s, nil
}

// MatchObject reports whether obj satisfies every condition (logical AND).
func (s *Selector) MatchObject(obj map[string]any) bool {
	for _, c := range s.conditions {
		var actual []any
		if c.bareKey != "" {
			actual = findRecursive(obj, c.bareKey)
		} else {
			actual = resolveValues(obj, c.segments)
		}
		if !intersects(actual, c.expected) {
			return false
		}
	}
	return true
}

// parseSelectorPath splits a selector key into path segments. A key with no '.'
// and no '[' is a bare key (recursive search) and is returned via bareKey.
func parseSelectorPath(key string) ([]segment, string, error) {
	if key == "" {
		return nil, "", errors.New("empty path segment")
	}
	if !strings.ContainsAny(key, ".[") {
		return nil, key, nil
	}

	parts := strings.Split(key, ".")
	segs := make([]segment, 0, len(parts))
	for _, part := range parts {
		switch {
		case part == "":
			return nil, "", errors.New("empty path segment")
		case part == "[]":
			segs = append(segs, segment{array: true})
		case strings.HasSuffix(part, "[]"):
			segs = append(segs, segment{field: strings.TrimSuffix(part, "[]"), array: true})
		case strings.ContainsAny(part, "[]"):
			return nil, "", errors.New("array indexes are not supported; use [] to match any array element")
		default:
			segs = append(segs, segment{field: part})
		}
	}
	return segs, "", nil
}

// resolveValues resolves a path against cur and returns the set of values it
// points to. A missing path or a type mismatch yields an empty set.
func resolveValues(cur any, segs []segment) []any {
	if len(segs) == 0 {
		return []any{cur}
	}

	seg := segs[0]
	if seg.array {
		v := cur
		if seg.field != "" {
			obj, ok := cur.(map[string]any)
			if !ok {
				return nil
			}
			v, ok = lookupField(obj, seg.field)
			if !ok {
				return nil
			}
		}
		arr, ok := asSlice(v)
		if !ok {
			return nil
		}
		var out []any
		for _, el := range arr {
			out = append(out, resolveValues(el, segs[1:])...)
		}
		return out
	}

	obj, ok := cur.(map[string]any)
	if !ok {
		return nil
	}
	v, exists := lookupField(obj, seg.field)
	if !exists {
		return nil
	}
	return resolveValues(v, segs[1:])
}

// lookupField returns the value of field in obj, matching the field name
// case-insensitively.
func lookupField(obj map[string]any, field string) (any, bool) {
	if v, ok := obj[field]; ok {
		return v, true
	}
	for k, v := range obj {
		if strings.EqualFold(k, field) {
			return v, true
		}
	}
	return nil, false
}

// findRecursive returns every value whose field name equals key at any depth.
func findRecursive(cur any, key string) []any {
	var out []any
	switch v := cur.(type) {
	case map[string]any:
		for k, val := range v {
			if strings.EqualFold(k, key) {
				out = append(out, val)
			}
			out = append(out, findRecursive(val, key)...)
		}
	case []any:
		for _, el := range v {
			out = append(out, findRecursive(el, key)...)
		}
	}
	return out
}

// normalizeValue converts a value to its canonical, lowercased string form.
func normalizeValue(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case string:
		return strings.ToLower(t)
	case bool:
		if t {
			return "true"
		}
		return "false"
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(t), 'f', -1, 32)
	case int:
		return strconv.FormatInt(int64(t), 10)
	case int64:
		return strconv.FormatInt(t, 10)
	case int32:
		return strconv.FormatInt(int64(t), 10)
	case json.Number:
		return t.String()
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		return strings.ToLower(string(b))
	}
}

// asSlice returns v as a slice when it is a JSON array.
func asSlice(v any) ([]any, bool) {
	s, ok := v.([]any)
	return s, ok
}

// intersects reports whether the normalized actual values contain any of the
// pre-normalized expected values.
func intersects(actual []any, expected []string) bool {
	if len(actual) == 0 || len(expected) == 0 {
		return false
	}
	set := make(map[string]struct{}, len(actual))
	for _, a := range actual {
		set[normalizeValue(a)] = struct{}{}
	}
	for _, e := range expected {
		if _, ok := set[e]; ok {
			return true
		}
	}
	return false
}
