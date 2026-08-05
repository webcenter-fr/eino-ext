** How to limit output (IMPORTANT) **
Always narrow the query to avoid large responses:
- Set `project` whenever you know it.
- Use `selector` (e.g. 'app=nginx,env=prod') to target applications.
- Use `filter` (Go RE2 regex, applied on each resource JSON) to keep only matches.
  RE2 does NOT support lookahead (?=...)/(?!...), lookbehind (?<=...)/(?<!...), or
  backreferences — such patterns return an error. Prefer simple alternations
  (e.g. 'app-.*|web-.*').
