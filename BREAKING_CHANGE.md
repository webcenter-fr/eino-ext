# Breaking Changes

## libs/toolkit/egress: working proxy, stricter policy validation

- `NewProxy(pol)` is now `NewProxy(ctx, pol)`. Nothing in this repository called
  it; external callers add `ctx`.
- `Policy.AllowHosts` is now validated as RFC1123 hostnames. Wildcards
  (`*.github.com`), ports (`example.com:443`), and trailing dots are now errors.
  They previously "worked" only in the sense that they silently never matched
  and denied all traffic to the intended host.
- `Policy.Allows` now also denies IPv6 ULA/link-local, unspecified, and CGNAT
  addresses when `AllowLocalNetwork=false`. Configurations relying on reaching
  an IPv6 ULA destination must set `AllowLocalNetwork=true` or add an explicit
  `AllowCIDRs` entry.
- `Proxy.EnvVars` now returns a populated `NO_PROXY` instead of `""`.
- Behavioral note (not a compile break): `WithEgressPolicy` now actually
  enforces. Containers that previously had unrestricted egress despite a policy
  will now be filtered. This is the intended fix; review allowlists before
  upgrading.

## MCP platform extracted into `github.com/webcenter-fr/go-mcp-platform`

`libs/mcp`, `components/mcp`, `cmd/mcp-server`, `server/approval`,
`libs/toolkit/{logging,secretref}`, `helm/`, and both Dockerfiles moved to
[`github.com/webcenter-fr/go-mcp-platform`](https://github.com/webcenter-fr/go-mcp-platform).
eino-ext is now a pure eino library.

Migrate imports mechanically:

| Old import path | New import path |
|---|---|
| `github.com/webcenter-fr/eino-ext/libs/mcp/serverconfig` | `github.com/webcenter-fr/go-mcp-platform/internal/serverconfig` |
| `github.com/webcenter-fr/eino-ext/libs/mcp/approval` | `github.com/webcenter-fr/go-mcp-platform/pkg/mcp/approval` |
| `github.com/webcenter-fr/eino-ext/libs/mcp` | `github.com/webcenter-fr/go-mcp-platform/pkg/mcp` |
| `github.com/webcenter-fr/eino-ext/components/mcp` | `github.com/webcenter-fr/go-mcp-platform/pkg/servers` |
| `github.com/webcenter-fr/eino-ext/server/approval/store` | `github.com/webcenter-fr/go-mcp-platform/internal/approval/store` |
| `github.com/webcenter-fr/eino-ext/server/approval` | `github.com/webcenter-fr/go-mcp-platform/internal/approval` |
| `github.com/webcenter-fr/eino-ext/libs/toolkit/logging` | `github.com/webcenter-fr/go-mcp-platform/internal/logging` |
| `github.com/webcenter-fr/eino-ext/libs/toolkit/secretref` | `github.com/webcenter-fr/go-mcp-platform/internal/secretref` |

The platform also renames its runtime contract: `EINO_MCP_*` ->
`MCP_PLATFORM_*`, `EINO_APPROVAL_*` -> `MCP_APPROVAL_*`, the config mount path
`/etc/eino-ext` -> `/etc/go-mcp-platform`, the MCP server identity
`eino-ext-mcp` -> `go-mcp-platform-mcp`, the Helm chart `eino-ext` ->
`go-mcp-platform`, and the image repositories `webcenter-fr/eino-ext/{mcp,approval}`
-> `webcenter-fr/go-mcp-platform/{mcp,approval}`.

## tool/github: `github_file_write` is local-only; use `github_commit_push` to persist

`github_file_write` no longer checks out a branch, stages, commits, or pushes.
It only writes the file to the local clone and returns `{path, bytes}`. The
`branch`, `baseBranch`, and `commitMessage` parameters are removed.

Migrate a write-then-publish flow to:

1. `github_file_write` / `github_file_edit` / `github_file_delete` /
   `github_file_copy` / `github_file_move` — modify the local clone.
2. `github_commit_push` — the single gated action that stages, commits, and
   pushes (requires `confirmed=true` plus host authorization; `dryRun=true`
   returns a rich diffstat).

New local-only tools: `github_file_edit`, `github_branch_checkout`,
`github_status`, and `github_diff`. `github_commit_push` is added to
`WriteToolNames()`.

## tool/github: search/list return a pagination wrapper

`github_file_search` and `github_file_list` now return
`{results:[...], hasMore, nextOffset}` instead of a bare JSON array. Add the
optional `offset` parameter (default 0) and use `nextOffset` to page.

`github_file_list.maxDepth` is now a pointer in Go (`*int`) so that an omitted
value defaults to 1 while an explicit `0` means unlimited depth, matching the
documented behaviour.

## tool/shell: workspace-aware containers and opt-in persistence

`shell.Config` gains `Workspace *workspace.Layout` (root + session key +
`RequireSession`) and `SessionIdleTTL`. `Workdir` is now
`required_without=Workspace` and remains the fallback for non-session/non-repo
execs.

`shell.Params` (and `RawExecParams`) gain optional `owner`, `repo`, and
`persistChanges`. When `owner`/`repo` are set, the exec runs in the session
workspace `<root>/<session>/<owner>/<repo>` shared with the GitHub clone tools.
Container writes are throwaway unless `persistChanges=true`, which exports
`/workspace` back to the host **excluding `.git`**.

## libs/toolkit/dagger: explicit workspace mount and export

`Container` accepts `WithWorkspace(hostPath)` (and `WithWorkdir` is retained as
an alias). `Client.ExportWorkspace(ctx, container, hostPath)` exports the
container `/workspace` to a host path via a temporary directory, excluding
`.git` and propagating deletions.

## libs/toolkit/workspace: new shared package

The session workspace layout, path validation, touch/GC, and stat-and-exist
helpers now live in `libs/toolkit/workspace`. The exported `github` GC API
(`CloneSessionDir`, `TouchCloneSession`, `StartCloneGC`) is unchanged and now
delegates to it.

## tool/shell and tool/pipe: no confirmation, not write tools anymore

`shell_exec` and `pipe_exec` are no longer gated by the safety middleware:
their `WriteToolNames()` now returns an empty list and both tools execute
directly without user confirmation, because they run in the isolated Dagger
sandbox and never mutate production systems directly.

- Both parameter structs lose the `Confirmed` field; calls that passed
  `confirmed:true` should drop it. The `dryRun=true` preview is still available.
- `WriteToolNames()` now returns an empty list for both packages.
  `profilesupervisor` and `NewAllToolsWithSafety` auto-population rely on it,
  so shell sub-agents are no longer gated either.
- The command blocklist is still enforced on every execution, and write tools
  used as `pipe` tool steps still fail closed by default. Opt in to a per-step
  gate via `pipe.Config.WriteToolNames` + `pipe.Config.ExecutionAuthorizer`
  (mirrors the safety middleware's dry-run/confirmed + host-authorization flow);
  `NewAllToolsWithSafety` forwards the middleware's authorizer to the pipe.
- Note: the Dagger sandbox still has unrestricted network egress — the
  `NetworkPolicy`/`AllowLocalNetwork` controls are currently declared but not
  enforced at exec time (pre-existing). The removed gate was a confirmation
  prompt, not a technical restriction: the model can still call out to
  reachable APIs from within the sandbox.

## tool/file: write tools now require host authorization to execute

The four file write tools (`file_write`, `file_delete`, `file_copy`,
`file_move`) expose `dryRun`/`confirmed` and require
`safety.WithExecutionAuthorized(ctx, "<tool>")` for real execution, matching
every other `WriteToolNames()` registry. Direct callers must now set
`confirmed:true` plus a grant.

## tool/file and tool/github: session directory naming changed

Session directories now use a 16-hex-char SHA-256 prefix
(`fileutil.SessionDirName`) instead of `SanitizePathSegment`, so existing
on-disk session directories move; `RequireSession` (default false) makes a
missing session fail closed.

## libs/toolkit/fileutil: `CopyDir` signature changed

Added a trailing `maxBytes int64` parameter (pass `0` for unlimited).

## tool/github: `github_repo_clone` / `github_repo_pull` no longer require `confirmed`

They are plain reads; the `Confirmed` parameter is removed. Callers that passed
`confirmed:true` should drop it (a `dryRun` preview remains available).

## tool/kubernetes: `kubernetes_describe` no longer returns `metadata.managedFields`

The field is stripped from describe output by default (`excludeFieldsOutput` is
unchanged and still top-level-only).

## libs/toolkit: deprecated helpers removed

`safety.ShouldGate`, `confirm.RequireConfirmation`, and
`confirm.RequireConfirmationForAction` are deleted. Use
`safety.ShouldGateWithAuthorization`, `confirm.RequireConfirmationCtx`, and
`confirm.RequireConfirmationForActionCtx` respectively.
(`Config.AllowModelConfirmation` is retained.)

## safety middleware: write tools now require host authorization to execute

The safety middleware and every write tool now authorize real execution from
`context.Context` (an `ExecutionAuthorizer`) instead of trusting the
model-supplied `confirmed:true` argument. With no authorizer configured, write
tools may only dry-run; a `confirmed:true` call returns
`safety.ErrExecutionNotAuthorized`.

To migrate, implement `safety.ExecutionAuthorizer` backed by your approval store
and set `safety.Config.ExecutionAuthorizer`, or set
`Config.AllowModelConfirmation: true` to opt back into the previous (insecure)
behavior.

## memory/file: Return signature changed

`NewFileMemory` and `GetDefaultMemory` now return `(memory.Memory, error)` instead of `memory.Memory`. Previously, errors (invalid config, directory creation failure) were silently swallowed and `nil` was returned. Callers must now handle the error.

```go
// Before
mem := file.GetDefaultMemory()

// After
mem, err := file.GetDefaultMemory()
```

## tool/argocd: Config field renamed

`Config.Url` renamed to `Config.URL`. The JSON tag is unchanged (`json:"url"`), so JSON serialization is unaffected. Only Go struct literals and field accessors need updating.

```go
// Before
cfg := argocd.Config{Url: "https://..."}

// After
cfg := argocd.Config{URL: "https://..."}
```

## tool/argocd: RepositoryListOutput field renamed

`RepositoryListOutput.Url` renamed to `RepositoryListOutput.URL`. JSON tag preserved (`json:"url"`).

## tool/argocd: MustMarshal removed

The locally duplicated `MustMarshal` helper was removed. Use `github.com/webcenter-fr/eino-ext/libs/toolkit/marshal.MustMarshal` instead.

## tool/kubernetes: ResourceDescribeOutput removed

`ResourceDescribeOutput` struct removed. Use `DescribeOutput` from the same package instead. Both types were identical.

## libs/pricer: Package deleted

The `github.com/webcenter-fr/eino-ext/libs/pricer` package was deleted (duplicate of `callbacks/activity` token/pricer types). Use `callbacks/activity` directly.

## libs/toolkit/guidance: Package deleted

The `github.com/webcenter-fr/eino-ext/libs/toolkit/guidance` package was deleted (unused). Components that previously used it now embed Markdown prompts via `//go:embed`.

## libs/toolkit/validate: StructName removed

`validate.StructName` was removed. It was unused and not needed by the `validate.Struct` workflow.

## libs/toolkit/marshal: MustUnmarshal removed

`marshal.MustUnmarshal` was removed. It was an unused re-export from `encoding/json`.

## libs/toolkit/filter: MatchString removed

`filter.MatchString` was removed. Use `filter.Match(data, re)` with a pre-compiled `*regexp.Regexp` instead.
