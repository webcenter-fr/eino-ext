package prometheus

import (
	"strings"

	"emperror.dev/errors"
	"github.com/goccy/go-json"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
)

const listOutputGuidance = `
** How to limit output (IMPORTANT) **
Always narrow the query to avoid large responses:
- Use ` + "`filter`" + ` (Go RE2 regex, applied on each result JSON) to keep only matches.
- Use ` + "`state`" + ` to filter alerts by firing/pending/inactive.
- Use ` + "`paginate.pageSize`" + ` (default 20) and the returned ` + "`paginateToken`" + ` to page
  through large result sets instead of requesting everything at once.
  The ` + "`paginateToken`" + ` is returned as the last element of the result list.
`

const describeOutputGuidance = `
** How to limit output (IMPORTANT) **
Use ` + "`filter`" + ` (Go RE2 regex applied on alert label JSON) to narrow down to specific alerts.
`

func MustMarshal(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func instanceNotFoundError(instance string, known []string) error {
	return errors.Errorf("Prometheus instance not found: %s. Instance must be one of: %s", instance, strings.Join(known, ", "))
}

func validateParams(v any) error {
	return validate.Struct(v)
}
