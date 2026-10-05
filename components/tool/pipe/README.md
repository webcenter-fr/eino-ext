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
	Confirmed: true,
	Steps: []pipe.Step{
		{Tool: &pipe.ToolStep{Name: "my_reader"}},
		{Shell: &pipe.ShellStep{Command: []string{"grep", "error"}}},
		{Shell: &pipe.ShellStep{Command: []string{"head", "-n", "20"}}},
	},
})
```

With the safety middleware:

```go
tools, mw, err := pipe.NewAllToolsWithSafety(ctx, cfg, nil)
```

## Step DSL

Each `Step` sets **exactly one** of `shell` or `tool`:

- `shell`: `{ "command": ["grep", "error"] }` — runs the command in the Dagger
  sandbox. The previous step's stdout is piped to the command's stdin.
- `tool`: `{ "name": "my_reader", "args": { ... } }` — invokes a tool from
  `Config.Tools`. If `args` is omitted, the previous step's stdout is passed
  verbatim as the tool's raw JSON arguments.

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

- `pipe_exec` is a **write tool**: call it with `dryRun=true` to preview, then
  `confirmed=true` after approval. The safety middleware gates it, and `Invoke`
  re-checks with `confirm.RequireConfirmationCtx` as defense in depth.
- Every shell step runs through `shell.RawExec`, which **always** enforces the
  command blocklist, independent of confirmation.
- Only **read tools** can be used as `tool` steps. A write tool invoked inside a
  pipeline fails closed: authorization is tool-name scoped to `pipe_exec`, so the
  inner write tool's own confirmation check finds no grant.
- Commands are typed `[]string`; the model never constructs a shell-language
  pipeline string, so there is no new injection surface.

## Checkup

```go
results := pipe.Check(ctx, cfg)
```

Delegates to `shell.Check` for the sandbox and reports the number of registered
tools.
