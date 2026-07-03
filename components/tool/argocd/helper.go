package argocd

import (
	"strings"

	"emperror.dev/errors"
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

// MustMarshal marshals v to JSON. Panics on error.
// This is safe to use here because Go structs cannot fail JSON serialization in practice.
func MustMarshal(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// instanceNotFoundError returns an error indicating the requested instance is unknown.
func instanceNotFoundError(instance string, known []string) error {
	return errors.Errorf("ArgoCD instance not found: %s. Instance must be one of: %s", instance, strings.Join(known, ", "))
}
