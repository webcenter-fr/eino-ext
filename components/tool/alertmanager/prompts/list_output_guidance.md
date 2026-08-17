** How to limit output (IMPORTANT) **
Always narrow the query to avoid large responses:
- Use `filter` (Go RE2 regex, applied on each result JSON) to keep only matches.
  RE2 does NOT support lookahead (?=...)/(?!...), lookbehind (?<=...)/(?<!...), or
  backreferences — such patterns return an error. Prefer simple alternations
  (e.g. 'node_cpu.*|node_memory.*').
- Use `state` to filter alerts by firing/pending/inactive.
- Use `paginate.pageSize` (default 20) and the returned `paginateToken` to page
  through large result sets instead of requesting everything at once.
  The `paginateToken` is returned as the last element of the result list.
