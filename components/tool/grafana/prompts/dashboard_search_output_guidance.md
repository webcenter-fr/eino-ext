** How to limit output (IMPORTANT) **
Always narrow the query to avoid large responses:
- Use `query` to search by title (e.g. 'production', 'kubernetes').
- Use `tags` to filter by dashboard tags (e.g. ['prod', 'infra']).
- Use `type` to restrict to 'dash-db' (dashboards only) or 'dash-folder'.
- Use `folderUIDs` to search within specific folders.
- Use `filter` (Go RE2 regex, applied on each dashboard JSON output) to keep
  only matches. RE2 does NOT support lookahead (?=...)/(?!...), lookbehind
  (?<=...)/(?<!...), or backreferences — such patterns return an error. Prefer
  simple alternations (e.g. 'prod|staging').
- Use `paginate.pageSize` (default 100, max 5000) and `paginate.page` to page
  through large result sets.
