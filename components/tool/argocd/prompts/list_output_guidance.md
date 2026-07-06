** How to limit output (IMPORTANT) **
Always narrow the query to avoid large responses:
- Set `project` whenever you know it.
- Use `selector` (e.g. 'app=nginx,env=prod') to target applications.
- Use `filter` (Go RE2 regex, applied on each resource JSON) to keep only matches.
