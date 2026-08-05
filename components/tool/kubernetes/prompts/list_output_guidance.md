** How to limit output (IMPORTANT) **
Always narrow the query to avoid large responses:
- Set `namespace` whenever you know it.
- Use `labelsSelector` (e.g. 'app=nginx,env=prod') to target resources.
- Use `filter` (Go RE2 regex, applied on each resource JSON) to keep only matches.
  RE2 does NOT support lookahead (?=...)/(?!...), lookbehind (?<=...)/(?<!...), or
  backreferences — such patterns return an error. Prefer simple alternations
  (e.g. 'app-.*|web-.*').
- Use `paginate.pageSize` (default 50) and the returned `paginateToken` to page
  through large result sets instead of requesting everything at once.
  The `paginateToken` is returned as the last element of the result list.
