package argocd

import (
	"fmt"
	"regexp"
	"strings"

	"emperror.dev/errors"
	"github.com/go-playground/validator/v10"
	"github.com/goccy/go-json"
)

const listOutputGuidance = `
** How to limit output (IMPORTANT) **
Always narrow the query to avoid large responses:
- Set ` + "`project`" + ` whenever you know it.
- Use ` + "`selector`" + ` (e.g. 'app=nginx,env=prod') to target applications.
- Use ` + "`filter`" + ` (Go RE2 regex, applied on each resource JSON) to keep only matches.
`

const describeOutputGuidance = `
** How to limit output (IMPORTANT) **
Use ` + "`excludeFieldsOutput`" + ` to drop large sections you do not need (any of
'metadata', 'spec', 'status') instead of fetching the full resource.
`

func CompileFilter(pattern string) (*regexp.Regexp, error) {
	if pattern == "" {
		return nil, nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, errors.Wrapf(err, "invalid regex filter %q (Go RE2 syntax)", pattern)
	}
	return re, nil
}

func IsMatch(o json.RawMessage, filter *regexp.Regexp) bool {
	if filter == nil {
		return true
	}
	return filter.Match(o)
}

func MustMarshal(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func validateParams(v any) error {
	validator := validator.New()
	if err := validator.Struct(v); err != nil {
		return errors.Wrap(err, fmt.Sprintf("invalid parameters for %T", v))
	}
	return nil
}

func instanceNotFoundError(instance string, known []string) error {
	return errors.Errorf("ArgoCD instance not found: %s. Instance must be one of: %s", instance, strings.Join(known, ", "))
}
