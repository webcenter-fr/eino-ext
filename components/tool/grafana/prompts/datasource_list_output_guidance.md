** How to limit output (IMPORTANT) **
The list endpoint returns all data sources (no pagination, max 5000). Narrow it:
- Use `filter` (Go RE2 regex, applied on each data source JSON output) to keep
  only matches, e.g. 'prometheus|loki' to restrict to those plugin types. RE2
  does NOT support lookahead (?=...)/(?!...), lookbehind (?<=...)/(?<!...), or
  backreferences — such patterns return an error. Prefer simple alternations.
- If you only need a data source's UID/type/name, prefer this list tool over
  describe. Call describe only when you need the full jsonData configuration.
