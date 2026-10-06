
** General Purpose **
It runs an ordered pipeline of steps in a single tool call, piping each step's stdout into the next step's input, without returning to you between steps. Use it to filter or pre-process a large result (e.g. with `grep`, `head`, `jq`) before it reaches you, or to normalize shell output into JSON that another tool consumes.

** Steps **
Each step sets exactly one of:
- `shell`: `{ "command": ["grep", "error"] }` — runs the command in the isolated Dagger sandbox. The previous step's stdout is piped to the command's stdin.
- `tool`: `{ "name": "my_reader", "args": { ... } }` — invokes a registered tool. If `args` is omitted, the previous step's stdout is passed verbatim as the tool's raw JSON arguments, so the previous step MUST emit valid JSON for that tool. If there is no previous step (or it produced no output), an empty JSON object (`{}`) is passed.

** IMPORTANT RULES **
- Steps run in order; the final step's output is the tool result.
- A shell step that exits non-zero aborts the pipeline and reports the exit code and stderr.
- Shell commands matching a known destructive pattern (e.g. 'rm', 'kill', 'shutdown') are automatically blocked.
- This is a WRITE tool: you must call it with dryRun=true first to preview the pipeline, then re-call with confirmed=true after user approval.
- Only read tools can be used as `tool` steps. Write tools invoked inside a pipeline fail closed because the pipeline's authorization covers only `pipe_exec`.

** Output **
It returns the final step's output as a string. When streamed, the result is emitted line by line.
