package kubernetes

import (
	"regexp"

	"emperror.dev/errors"
	"github.com/goccy/go-json"
)

// listOutputGuidance is a shared guidance block appended to the description of all
// list tools (generic and resource list). It instructs the model to narrow queries
// to avoid large responses that blow up the context window.
const listOutputGuidance = `
** How to limit output (IMPORTANT) **
Always narrow the query to avoid large responses:
- Set ` + "`namespace`" + ` whenever you know it.
- Use ` + "`labelsSelector`" + ` (e.g. 'app=nginx,env=prod') to target resources.
- Use ` + "`filter`" + ` (Go RE2 regex, applied on each resource JSON) to keep only matches.
- Use ` + "`paginate.pageSize`" + ` (default 50) and the returned ` + "`paginateToken`" + ` to page
  through large result sets instead of requesting everything at once.
  The ` + "`paginateToken`" + ` is returned as the last element of the result list.
`

// describeOutputGuidance is a shared guidance block appended to the description of all
// describe tools. It instructs the model to narrow the output to avoid large responses.
const describeOutputGuidance = `
** How to limit output (IMPORTANT) **
Use ` + "`excludeFieldsOutput`" + ` to drop large sections you do not need (any of
'metadata', 'spec', 'status', 'data') instead of fetching the full resource.
`

// CompileFilter compiles the given regex pattern using Go RE2 syntax. It returns a
// nil regexp (without error) when the pattern is empty, which callers must treat as
// "match everything". An invalid pattern returns a wrapped error instead of panicking.
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

// MustMarshal is a helper function that marshals a value to JSON and panics if an error occurs. It is useful for quickly converting data structures to JSON without having to handle errors in the calling code.
func MustMarshal(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// MustUnmarshal is a helper function that unmarshals JSON data into a specified Go data structure and panics if an error occurs. It is useful for quickly converting JSON data to Go structures without having to handle errors in the calling code.
func MustUnmarshal(data []byte, v any) {
	if err := json.Unmarshal(data, v); err != nil {
		panic(err)
	}
}
