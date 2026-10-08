package filter

import (
	"regexp"
	"strings"

	"emperror.dev/errors"
	"github.com/goccy/go-json"
)

// Matcher reports whether a JSON document matches a compiled filter.
type Matcher interface {
	// MatchJSON reports whether the raw JSON bytes match. Regex mode matches the
	// bytes directly; selector mode decodes the bytes to an object first.
	MatchJSON(data []byte) bool
	// MatchObject reports whether the decoded object matches. Selector mode
	// traverses the object directly; regex mode marshals it to JSON first.
	MatchObject(obj map[string]any) bool
}

// CompileMatcher compiles a filter expression.
//
// A trimmed string starting with '{' is a JSON-object selector; any other
// non-empty string is a Go RE2 regex. An empty string matches everything.
func CompileMatcher(pattern string) (Matcher, error) {
	p := strings.TrimSpace(pattern)
	if p == "" {
		return matchAllMatcher{}, nil
	}

	if strings.HasPrefix(p, "{") {
		var m map[string]any
		if err := json.Unmarshal([]byte(p), &m); err != nil {
			return nil, errors.Wrap(err, "filter looks like a JSON object selector but is not valid JSON")
		}
		sel, err := compileSelector(m)
		if err != nil {
			return nil, err
		}
		return selectorMatcher{sel: sel}, nil
	}

	re, err := Compile(p)
	if err != nil {
		return nil, err
	}
	return regexMatcher{re: re}, nil
}

// matchAllMatcher matches every document. It is returned for an empty filter.
type matchAllMatcher struct{}

func (matchAllMatcher) MatchJSON([]byte) bool           { return true }
func (matchAllMatcher) MatchObject(map[string]any) bool { return true }

// regexMatcher matches a Go RE2 regex against the JSON representation.
type regexMatcher struct {
	re *regexp.Regexp
}

func (m regexMatcher) MatchJSON(data []byte) bool {
	return m.re.Match(data)
}

func (m regexMatcher) MatchObject(obj map[string]any) bool {
	data, err := json.Marshal(obj)
	if err != nil {
		return false
	}
	return m.re.Match(data)
}

// selectorMatcher matches a structured JSON-object selector.
type selectorMatcher struct {
	sel *Selector
}

func (m selectorMatcher) MatchJSON(data []byte) bool {
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		return false
	}
	return m.sel.MatchObject(obj)
}

func (m selectorMatcher) MatchObject(obj map[string]any) bool {
	return m.sel.MatchObject(obj)
}
