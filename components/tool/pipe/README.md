# pipe

`pipe` is an eino tool that composes an ordered chain of sandboxed shell
commands and registered tools into a **single tool call**. Each step's stdout is
piped into the next step's stdin (shell) or input (tool), so the agent does not
return to the LLM between steps.

It implements both `tool.InvokableTool` and `tool.StreamableTool`.

## Why

Filtering or reshaping a large tool result usually costs an extra LLM
round-trip: call the tool, send the whole result to the model, then call a shell
tool to `grep`/`head`/`jq` it. `pipe` lets the model express that chain once:

```go
pipeTool, err := pipe.NewPipeTool(ctx, &pipe.Config{
	Shell: &shell.Config{Workdir: "/path/to/project"},
	Tools: map[string]tool.InvokableTool{
		"my_reader": readerTool,
	},
})
if err != nil {
	return err
}
defer pipeTool.Close()

result, err := pipeTool.Invoke(ctx, &pipe.Params{
	Steps: []pipe.Step{
		{Tool: &pipe.ToolStep{Name: "my_reader"}},
		{Shell: &pipe.ShellStep{Command: []string{"grep", "error"}}},
		{Shell: &pipe.ShellStep{Command: []string{"head", "-n", "20"}}},
	},
})
```

With the safety middleware (`safetyCfg.ExecutionAuthorizer` and
`safetyCfg.AllowModelConfirmation` are forwarded to the pipe config, so write
tool steps gate on the same host approval mechanism as top-level write tools):

```go
tools, mw, err := pipe.NewAllToolsWithSafety(ctx, cfg, safetyCfg)
```

To allow write tools (e.g. Kubernetes) as `tool` steps:

```go
pipeTool, err := pipe.NewPipeTool(ctx, &pipe.Config{
	Shell:             &shell.Config{Workdir: "/path/to/project"},
	Tools:             allTools,
	WriteToolNames:    kubernetes.WriteToolNames(), // gate these names as steps
	ExecutionAuthorizer: myAuthorizer,              // host approval store
})
```

A listed write tool step must first run with `dryRun:true` in its `args` (the
pipeline result then carries `DRY-RUN RESULT` guidance), and after user approval
be re-called with `confirmed:true`; execution only proceeds when the authorizer
grants it.

## Step DSL

Each `Step` sets **exactly one** of `shell` or `tool`:

- `shell`: `{ "command": ["grep", "error"] }` — runs the command in the Dagger
  sandbox. The previous step's stdout is piped to the command's stdin.
- `tool`: `{ "name": "my_reader", "args": { ... } }` — invokes a tool from
  `Config.Tools`. If `args` is omitted, the previous step's stdout is passed
  verbatim as the tool's raw JSON arguments. When there is no previous step (or
  it produced no output), an empty JSON object (`{}`) is passed instead, so
  tools with all-optional arguments work without explicit `args`.

### JSON contract

When a `shell` step feeds a `tool` step with no explicit `args`, the shell
command **must emit valid JSON** for the target tool. For example, a shell step
running `jq -c '{query: .}'` can produce the arguments for the next tool.

## Behavior

- The pipeline is **buffered**: each step's output is materialized as a string
  before the next step runs. Dagger exposes stdin as a full string, so true
  streaming between steps is not possible.
- A shell step that exits non-zero aborts the pipeline and reports the exit code
  and stderr. On success, stderr is dropped so it does not corrupt the next
  step's stdin.
- `MaxOutputBytes` caps the data flowing between steps; `DefaultTimeout` bounds
  the whole pipeline (and each shell step).
- Output is text-only; binary content may be mangled.

## Security

- `pipe_exec` is **not gated** by the safety middleware: shell steps run in the
  isolated, disposable Dagger sandbox and tool steps invoke read-only registered
  tools, so no user confirmation is required. `dryRun=true` still returns a pure
  preview without executing anything.
- Every shell step runs through `shell.RawExec`, which **always** enforces the
  command blocklist.
- **Write tools as tool steps (opt-in gate).** Tools listed in
  `Config.WriteToolNames` (e.g. `kubernetes.WriteToolNames()`) are gated inside
  the pipeline exactly like the safety middleware gates top-level write tools:
  the step's args must carry `dryRun:true` (preview, marked with guidance) or
  `confirmed:true`, and real execution requires a grant from
  `Config.ExecutionAuthorizer` (otherwise `ErrExecutionNotAuthorized`).
  `AllowModelConfirmation` restores trust of the model-supplied `confirmed:true`
  for tests/sandboxes only. Write tools **not** listed still fail closed via
  their own confirmation check (no grant reaches them).
- Commands are typed `[]string`; the model never constructs a shell-language
  pipeline string, so there is no new injection surface.

## Checkup

```go
results := pipe.Check(ctx, cfg)
```

Delegates to `shell.Check` for the sandbox and reports the number of registered
tools.
