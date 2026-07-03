package kubernetes

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
