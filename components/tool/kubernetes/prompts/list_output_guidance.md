** How to limit output (IMPORTANT) **
Always narrow the query to avoid large responses:
- Set `namespace` whenever you know it.
- Use `labelsSelector` (e.g. 'app=nginx,env=prod') to target resources.
- Use `filter` to keep only matching resources. It accepts either a JSON object
  selector (e.g. '{"status.conditions[].reason":"NotSupported"}') — all keys must
  match (AND), `[]` matches any array element, an array value means IN, matching
  is type-coerced and case-insensitive, and a bare key matches any field with that
  name at any depth — or a Go RE2 regex applied on the raw resource JSON. RE2 does
  NOT support lookahead (?=...)/(?!...), lookbehind (?<=...)/(?<!...), or
  backreferences — such patterns return an error. Prefer the selector for
  field-value filters; prefer simple alternations (e.g. 'app-.*|web-.*') for regex.
- Use `paginate.pageSize` (default 50) and the returned `paginateToken` to page
  through large result sets instead of requesting everything at once.
  The `paginateToken` is returned as the last element of the result list.
