// Package filter provides regex-based JSON filtering utilities.
package filter

import (
	"regexp"

	"emperror.dev/errors"
	"github.com/goccy/go-json"
)

// Compile compiles a regex pattern string, returning nil if pattern is empty.
func Compile(pattern string) (*regexp.Regexp, error) {
	if pattern == "" {
		return nil, nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, errors.Wrapf(err, "invalid regex filter %q (Go RE2 syntax: lookahead (?=...)/(?!...), lookbehind (?<=...)/(?<!...), and backreferences are NOT supported)", pattern)
	}
	return re, nil
}

// Match checks if the given JSON data matches the filter regex.
func Match(data json.RawMessage, filter *regexp.Regexp) bool {
	if filter == nil {
		return true
	}
	return filter.Match(data)
}
