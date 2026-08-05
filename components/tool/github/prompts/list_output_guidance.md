** How to limit output (IMPORTANT) **
Always narrow the query to avoid large responses:
- Set `state` (open/closed/all) to filter by issue/PR status.
- Set `labels` to filter by labels.
- Set `perPage` to a reasonable limit (max 100).
- Use `filter` (Go RE2 regex, applied on each item JSON) to keep only matches.
  RE2 does NOT support lookahead (?=...)/(?!...), lookbehind (?<=...)/(?<!...), or
  backreferences — such patterns return an error. Prefer simple alternations
  (e.g. 'bug|fix').
