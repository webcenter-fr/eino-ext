# eino-ext: make the safety gate authorize from context, not from model arguments

**Target repository:** `github.com/webcenter-fr/eino-ext`
**Implement in the other IDE / repo checkout.** Nothing here is required for the consuming
application to be secure — the app-side fix
(`.kilo/plans/1788336229176-write-approval-hardening.md`) closes the hole independently.
This is library-level defense in depth, so that *every* consumer of the safety middleware is
safe by default instead of only the ones that build their own gate on top.

Version inspected: `v0.0.0-20260902074558-1db9338b3f5e` (module cache).

## Problem

`libs/toolkit/safety/gate.go`:

```go
func ShouldGate(toolName string, writeTools map[string]bool, gp GateParams) error {
    if !writeTools[toolName] { return nil }
    if gp.DryRun            { return nil }
    if !gp.Confirmed        { return ErrGateRequired }
    return nil
}
```

`GateParams` is produced by `ExtractGateParams(rawJSON)` from the **tool-call arguments the
LLM emitted**. `components/middleware/safety/middleware.go:206-219` calls it with nothing
else — no context lookup, no approval record, no authorizer.

The doc comment says *"Write tools with Confirmed=true pass — user has approved the
dry-run."* That is an assumption the library cannot verify. Nothing distinguishes
`confirmed:true` injected by a trusted server-side approval flow from `confirmed:true`
invented by the model. A prompt-injected or non-compliant model executes a real mutation in
one call, and because the pending/preview surface only exists in `PhaseDryRun`, the human
never sees it.

The same weakness is duplicated per tool: every write tool calls
`confirm.RequireConfirmation(params.DryRun, params.Confirmed)` on model-supplied struct
fields (`components/tool/kubernetes/resource_delete.go:106`, `resource_patch.go:77`,
`resource_create.go:81`, `resource_apply.go:63`, `components/tool/shell/shell.go:105,171`,
and the grafana dashboard write tool). So the second layer has the same root cause as the first.

## Design

Move the execute decision to an interface the **host application** implements, resolved from
`context.Context` — a channel the model cannot write to. Default to fail-closed: with no
authorizer configured, write tools may only dry-run.

### 1. `libs/toolkit/safety` — authorization primitive

```go
// ExecutionAuthorizer decides whether a write tool may execute for real.
// Implementations MUST derive their answer from server-side state (an approval
// record, a signed token, an operator policy) and MUST NOT trust anything
// carried in args, which is model-controlled.
type ExecutionAuthorizer interface {
    AuthorizeExecute(ctx context.Context, toolName string, args json.RawMessage) error
}

var ErrExecutionNotAuthorized = errors.New(
    "SAFETY GATE: execution of this write tool was not authorized by the host application")
```

Add a context carrier for tool-level use:
`WithExecutionAuthorized(ctx, toolName) / ExecutionAuthorizedFor(ctx, toolName) bool`.

New gate entry point, leaving `ShouldGate` untouched for compatibility:

```go
func ShouldGateWithAuthorization(
    ctx context.Context, toolName string, writeTools map[string]bool,
    gp GateParams, args json.RawMessage, auth ExecutionAuthorizer,
) error
```

Rules:
- not a write tool → allow
- `DryRun` → allow (previews are always safe once tools genuinely implement dry-run — see §4)
- not `Confirmed` → `ErrGateRequired` (unchanged)
- `Confirmed` and `auth == nil` → `ErrExecutionNotAuthorized` (**fail closed**)
- `Confirmed` and `auth.AuthorizeExecute(...) != nil` → that error
- otherwise → allow

Deprecate `ShouldGate` with a doc comment stating plainly that it trusts model-supplied
input and must not be used as an authorization boundary.

### 2. `components/middleware/safety` — config and preflight

Add to `Config`:

```go
// ExecutionAuthorizer gates real execution of write tools. When nil, write
// tools may only dry-run: a confirmed=true call is rejected.
ExecutionAuthorizer safety.ExecutionAuthorizer

// AllowModelConfirmation restores the pre-hardening behavior where a
// model-supplied confirmed=true is sufficient to execute.
// INSECURE: only for tests and non-production sandboxes.
AllowModelConfirmation bool
```

In `preflight` (`middleware.go:178-220`) replace the `ShouldGate` call with
`ShouldGateWithAuthorization`, short-circuiting to the legacy path only when
`AllowModelConfirmation` is true. Emit the existing `PhaseRejected` audit event on denial so
the rejection is observable, and include the reason.

When the call is authorized, set `safety.WithExecutionAuthorized(ctx, toolName)` on the
context passed to the endpoint, so the per-tool checks in §3 can see it.

### 3. `libs/toolkit/confirm` — per-tool second layer

Add `RequireConfirmationCtx(ctx, toolName, dryRun, confirmed) error`: behaves like
`RequireConfirmation` but additionally requires `safety.ExecutionAuthorizedFor(ctx, toolName)`
when executing. Migrate the write tools to it:

- `components/tool/kubernetes/resource_create.go:81`, `resource_patch.go:77`,
  `resource_delete.go:106`, `resource_apply.go:63`, `pod_exec.go` (both invoke and stream paths)
- `components/tool/shell/shell.go:105` and `:171`
- the grafana dashboard write tool
- any argocd / github write tools using the same helper

Provide a documented escape hatch for direct tool use outside the middleware (tests,
programmatic callers): `safety.WithExecutionAuthorized` is exported, so a caller that owns
the decision can grant it explicitly. Keep `RequireConfirmation` for compatibility, marked
deprecated.

### 4. Audit dry-run correctness

The gate's "dry-run is always safe" rule is only true if every write tool actually
implements dry-run. Verified good today for kubernetes, shell and grafana. Add a guard so it
stays true:

- A test asserting that for every name in each registry's `WriteToolNames()`, invoking the
  tool with `dryRun:true` performs no mutation.
- Document in `WriteToolNames()` that listing a tool there is a contract: it must honour
  `dryRun` without side effects.

(The consuming application hit exactly this: its own local `write_file` tool was registered
as a write tool but had no dry-run and wrote during "preview". A library-level contract test
would not have caught a host-defined tool, but the documented contract makes the requirement
explicit.)

## Migration and compatibility

This is a **breaking behavior change**: consumers that relied on a model-supplied
`confirmed:true` executing will start getting `ErrExecutionNotAuthorized`.

- Release under a new minor with a prominent `CHANGELOG` / `README` security note.
- Migration for existing consumers: implement `ExecutionAuthorizer` backed by their approval
  store, or set `AllowModelConfirmation: true` to opt back into the old, insecure behavior.
- The consuming app in `rancher-doc-chat-api-k8s` will supply an authorizer that checks its
  `ApprovedWriteFromContext` pending-write record; after that it can drop
  `AllowModelConfirmation` entirely. Its app-side layers remain valid either way, so
  upgrade order does not matter.

## Validation

- `ShouldGateWithAuthorization` table test covering all six rules, including the
  nil-authorizer fail-closed case.
- Middleware test: write tool, `confirmed:true`, no authorizer → rejected, `PhaseRejected`
  audit event emitted, endpoint never invoked. Assert for all four `Wrap*ToolCall` variants
  (streaming matters: `pod_exec` is a `StreamableTool`).
- Middleware test: authorizer returning nil → executes, and the endpoint's context reports
  `ExecutionAuthorizedFor(toolName) == true`.
- Middleware test: `AllowModelConfirmation: true` reproduces legacy behavior exactly.
- Per-tool test: calling a write tool directly (no middleware) with `confirmed:true` and an
  unauthorized context is refused.
- Dry-run no-side-effect test per registry, per §4.
- Full existing suite must pass; expect updates to
  `components/middleware/safety/middleware_test.go:173`
  (`TestWrapInvokableToolCallWriteConfirmed`) and to the many tool tests that pass
  `Confirmed: true` directly — those should adopt `safety.WithExecutionAuthorized` rather
  than flipping `AllowModelConfirmation`, so they keep exercising the real path.

## Non-goals

- Deciding *how* a host authorizes (UI button, signed token, policy engine) — that is the
  host's concern, which is the point of the interface.
- Changing CEL policy evaluation, audit sinks, or the `Phase` vocabulary.
- Removing `ShouldGate` / `RequireConfirmation` in this change; deprecate now, remove in a
  later major.
