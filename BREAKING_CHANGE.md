# Breaking Changes

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
