package filter

import (
	"regexp"

	"emperror.dev/errors"
	"github.com/goccy/go-json"
)

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

func Match(data json.RawMessage, filter *regexp.Regexp) bool {
	if filter == nil {
		return true
	}
	return filter.Match(data)
}
