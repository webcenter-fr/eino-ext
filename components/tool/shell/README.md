# `components/tool/shell`

An eino shell tool backed by the Dagger engine. It gives LLM agents a secure,
sandboxed shell inside an OCI base image (golang, node, python, etc.) with
shared caching across agents. It implements both `tool.InvokableTool` and
`tool.StreamableTool`.

## Usage

```go
cfg := &shell.Config{
    Workdir: "/path/to/project",
}
shellTool, err := shell.NewShellTool(ctx, cfg)

// With safety middleware:
tools, mw, err := shell.NewAllToolsWithSafety(ctx, cfg, nil)
```

## Workspace-aware execution

The shell can share the per-session clone workspace used by the GitHub tools:

```go
cfg := &shell.Config{
    Workspace: &workspace.Layout{
        Root:           "/var/lib/eino/workspaces",
        SessionKey:     github.CloneSessionKey,
        RequireSession: true,
    },
}
```

When a call sets `owner` and `repo`, the container mounts
`<root>/<session>/<owner>/<repo>` at `/workspace`. Without `owner`/`repo`, the
tool falls back to `Config.Workdir` so existing hosts keep working.

```json
{
  "command": ["go", "build", "./..."],
  "owner": "my-org",
  "repo": "my-repo"
}
```

### Container writes are throwaway by default

Dagger mounts a snapshot of the host directory. Container writes do **not**
reach the host unless the call sets `persistChanges: true`:

```json
{
  "command": ["go", "mod", "tidy"],
  "owner": "my-org",
  "repo": "my-repo",
  "persistChanges": true
}
```

On persistence, the tool exports `/workspace` to a temporary directory and
syncs the non-`.git` entries back onto the host workspace. Deletions are
propagated. The live `.git` directory is **never** exported back, so container
`git` commands cannot corrupt the refs that `github_commit_push` later pushes.

Without `persistChanges`, the exec is a throwaway sandbox run.

## Parameters

- `command`: the command to execute as an array of strings.
- `stdin`: optional text piped to the command's standard input.
- `profile`: optional profile override.
- `owner` / `repo`: optional session workspace target (must be provided
  together).
- `persistChanges`: export container `/workspace` changes back to the host
  (default false).
- `filterPattern`: optional Go RE2 regex applied to each output line.
- `timeout`: optional duration string (e.g. `30s`, `5m`).
- `dryRun`: return a preview without executing.

## Security

- Command blocklist prevents destructive commands (`rm`, `kill`, etc.) and is
  enforced on `Invoke`, `InvokeAsStream`, and `RawExec`.
- Egress network policy restricts outbound traffic.
- No confirmation gate: commands execute directly in the disposable sandbox
  (`shell_exec` is not a write tool). `dryRun=true` still returns a preview.
- Cached containers are keyed by `{session, profile, baseImage, owner, repo}`,
  re-mount the workspace on every exec, and are evicted when the workspace is
  swept or after `SessionIdleTTL` (default 1h).

## Composability

`RawExec` is a trusted, gate-free primitive that runs a command and returns raw
stdout/stderr/exit code. It always enforces the blocklist and is intended for
compositors such as the pipe tool, not direct end users.

## Manual Dagger verification

The real mount/export path is covered by an opt-in test:

```bash
EINO_DAGGER_E2E=1 go test ./libs/toolkit/dagger -run TestDaggerWorkspaceE2E -v
```

Manual check: write a file on the host, run `go build` in the sandbox, run
`go mod tidy` with `persistChanges: true`, then confirm `.git` is untouched and
`github_status` sees the change.
