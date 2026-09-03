# eino-ext safety authorization — implementation plan

This plan makes the safety gate authorize **real execution of write tools** from
`context.Context` (host-app state) instead of trusting model-supplied
`confirmed:true` arguments. It is the implementation detail for the high-level
design in `.kilo/plans/1788336229176-eino-ext-safety-authorization.md`.

A coder who reads this file plus the repository should be able to implement
everything without further context. Full Go signatures, struct fields, doc
comments, check orderings, edge cases, error handling, tests, and doc changes are
specified below.

---

## 1. Goal and invariant

- **Before:** a model that emits `confirmed:true` on any write tool executes a
  real mutation in one call. Both the middleware gate (`safety.ShouldGate`) and
  the per-tool `confirm.RequireConfirmation*` helpers trust that model-supplied
  field.
- **After:** real execution of a write tool requires the **host application** to
  have authorized it, recorded in the `context.Context`. With no authorizer
  configured, write tools can only **dry-run** (fail closed).
- The model cannot write to `context.Context`, so prompt injection can no longer
  fabricate authorization.

Two independent layers are changed (defense in depth):

1. **Middleware gate** (`components/middleware/safety`) — decides allow/reject for
   every tool call routed through the ADK middleware, for *all* write tools
   (kubernetes, shell, grafana, argocd, github).
2. **Per-tool second layer** (`libs/toolkit/confirm`) — re-checks authorization
   inside each write tool, so a tool invoked directly (outside the middleware)
   is also protected.

---

## 2. Files affected (complete list)

### New files

- `libs/toolkit/safety/authorization.go`
- `libs/toolkit/safety/authorization_test.go`
- `libs/toolkit/confirm/confirm_test.go`

### Modified library files

- `libs/toolkit/safety/gate.go` — add `ShouldGateWithAuthorization`, deprecate `ShouldGate`.
- `libs/toolkit/confirm/confirm.go` — add `RequireConfirmationCtx` and
  `RequireConfirmationForActionCtx`; deprecate `RequireConfirmation` and
  `RequireConfirmationForAction`.

### Modified middleware files

- `components/middleware/safety/config.go` — add `ExecutionAuthorizer` and
  `AllowModelConfirmation` fields.
- `components/middleware/safety/middleware.go` — thread authorization through
  `preflight` and all four `Wrap*ToolCall` variants.
- `components/middleware/safety/middleware_test.go` — update 3 existing tests,
  add new authorization tests for all four wrap variants.

### Modified tool files (per-tool second layer)

Kubernetes:

- `components/tool/kubernetes/resource_create.go`
- `components/tool/kubernetes/resource_patch.go`
- `components/tool/kubernetes/resource_delete.go`
- `components/tool/kubernetes/resource_apply.go`
- `components/tool/kubernetes/pod_exec.go` (both `Invoke` and `InvokeAsStream`)

Shell:

- `components/tool/shell/shell.go` (`Invoke` and `InvokeAsStream`)

Grafana:

- `components/tool/grafana/dashboard_write.go`

ArgoCD:

- `components/tool/argocd/application_create.go`
- `components/tool/argocd/application_delete.go`
- `components/tool/argocd/application_sync.go`

GitHub (15 write tools):

- `components/tool/github/branch_create.go`
- `components/tool/github/release_create.go`
- `components/tool/github/issue_create.go`
- `components/tool/github/issue_comment.go`
- `components/tool/github/pr_create.go`
- `components/tool/github/pr_comment.go`
- `components/tool/github/pr_review.go`
- `components/tool/github/pr_suggest_change.go`
- `components/tool/github/pr_request_reviewers.go`
- `components/tool/github/repo_settings_update.go`
- `components/tool/github/webhook_upsert.go`
- `components/tool/github/file_write.go`
- `components/tool/github/file_delete.go`
- `components/tool/github/file_copy.go`
- `components/tool/github/file_move.go`

### Modified test files (tool tests that pass `confirmed:true`)

- `components/tool/grafana/dashboard_write_test.go`
- `components/tool/grafana/grafana_test.go`
- `components/tool/grafana/security_test.go`
- `components/tool/grafana/integration_test.go`
- `components/tool/argocd/application_test.go`
- `components/tool/github/pr_test.go`
- `components/tool/github/issue_test.go`
- `components/tool/github/repo_test.go`
- `components/tool/github/file_test.go`

### Registry doc-comment changes (§7)

- `components/tool/kubernetes/registry.go` (`WriteToolNames`)
- `components/tool/shell/registry.go` (`WriteToolNames`)
- `components/tool/grafana/registry.go` (`WriteToolNames`)
- `components/tool/argocd/registry.go` (`WriteToolNames`)
- `components/tool/github/registry.go` (`WriteToolNames`)

### Documentation

- `libs/toolkit/safety/README.md`
- `libs/toolkit/confirm/README.md`
- `components/middleware/safety/README.md`
- `BREAKING_CHANGE.md`

---

## 3. Step 1 — `libs/toolkit/safety` primitives

### 3.1 New file `libs/toolkit/safety/authorization.go`

Create this file with the exact content below. It contains the `ExecutionAuthorizer`
interface, the fail-closed sentinel, and the context carrier.

```go
package safety

import (
	"context"
	"encoding/json"

	"emperror.dev/errors"
)

// ExecutionAuthorizer decides whether a write tool may execute for real
// (not dry-run). Implementations MUST derive their answer from server-side
// state — an approval record, a signed token, an operator policy — and MUST
// NOT trust anything carried in the tool arguments, which are model-controlled.
//
// It is invoked only for write tools whose GateParams already has
// Confirmed == true (the "execute" step of the dry-run/confirm flow).
type ExecutionAuthorizer interface {
	// AuthorizeExecute returns nil if the tool may execute, or an error
	// describing why it was denied. Implementations must be safe for
	// concurrent use and must not panic on a nil ctx or empty toolName.
	AuthorizeExecute(ctx context.Context, toolName string, args json.RawMessage) error
}

// ErrExecutionNotAuthorized is returned when a write tool attempts real
// execution without host-app authorization. It is the fail-closed sentinel:
// with no ExecutionAuthorizer configured, write tools may only dry-run.
var ErrExecutionNotAuthorized = errors.New(
	"SAFETY GATE: execution of this write tool was not authorized by the host application")

// executionAuthorizedKey is an unexported context key type to avoid collisions.
type executionAuthorizedKey struct{}

var executionAuthorizedCtxKey = executionAuthorizedKey{}

// WithExecutionAuthorized returns a copy of ctx marking toolName as authorized
// to execute for real. The safety middleware sets this automatically when a
// write tool is authorized; programmatic callers who own the authorization
// decision (tests, non-ADK hosts) can set it directly.
//
// A nil ctx or empty toolName is a no-op and returns ctx unchanged.
func WithExecutionAuthorized(ctx context.Context, toolName string) context.Context {
	if ctx == nil || toolName == "" {
		return ctx
	}
	authorized, _ := ctx.Value(executionAuthorizedCtxKey).(map[string]struct{})
	next := make(map[string]struct{}, len(authorized)+1)
	for k := range authorized {
		next[k] = struct{}{}
	}
	next[toolName] = struct{}{}
	return context.WithValue(ctx, executionAuthorizedCtxKey, next)
}

// ExecutionAuthorizedFor reports whether toolName was marked executable via
// WithExecutionAuthorized on ctx (or one of its parents). It fails closed:
// a nil ctx, an empty toolName, or the absence of a grant all return false.
func ExecutionAuthorizedFor(ctx context.Context, toolName string) bool {
	if ctx == nil || toolName == "" {
		return false
	}
	authorized, _ := ctx.Value(executionAuthorizedCtxKey).(map[string]struct{})
	_, ok := authorized[toolName]
	return ok
}
```

### 3.2 Modify `libs/toolkit/safety/gate.go`

Add `"context"` to the import block (alongside `"encoding/json"` and
`"emperror.dev/errors"`).

Deprecate `ShouldGate` by replacing its doc comment with:

```go
// ShouldGate checks whether a tool call must be gated (require dry-run/confirmed flow).
//
// Deprecated: ShouldGate trusts the model-supplied Confirmed field and MUST NOT
// be used as an authorization boundary. Use ShouldGateWithAuthorization instead,
// which requires host-app authorization from context before real execution.
//
// Rules:
//   - Read-only tools (not in writeTools) always pass — no gate required.
//   - Write tools with DryRun=true always pass — dry-run is always allowed.
//   - Write tools with Confirmed=true pass — trusted unconditionally (INSECURE).
//   - Write tools with neither DryRun nor Confirmed are rejected.
//
// Returns nil if the call is allowed, or an error explaining the required flow.
func ShouldGate(toolName string, writeTools map[string]bool, gp GateParams) error {
	// (body unchanged)
}
```

Add the new entry point after `ShouldGate`:

```go
// ShouldGateWithAuthorization checks whether a write tool call may proceed,
// authorizing real execution from the context instead of trusting the
// model-supplied Confirmed field.
//
// Rules, evaluated in order:
//  1. Read-only tool (not in writeTools) -> allow.
//  2. DryRun -> allow (previews are safe; see the WriteToolNames contract).
//  3. Not Confirmed -> ErrGateRequired (the model must dry-run first).
//  4. Confirmed and auth == nil -> ErrExecutionNotAuthorized (fail closed).
//  5. Confirmed and auth.AuthorizeExecute(...) returns non-nil -> that error,
//     wrapped with the tool name for context.
//  6. Otherwise -> allow.
//
// ctx and args are passed through to the authorizer untouched and are only
// dereferenced by the authorizer. writeTools is the set of write-tool names
// (the same map built by the middleware from Config.WriteToolNames).
func ShouldGateWithAuthorization(
	ctx context.Context, toolName string, writeTools map[string]bool,
	gp GateParams, args json.RawMessage, auth ExecutionAuthorizer,
) error {
	if !writeTools[toolName] {
		return nil // read-only tool, no gate required
	}
	if gp.DryRun {
		return nil // dry-run is always allowed
	}
	if !gp.Confirmed {
		return ErrGateRequired
	}
	// Real execution of a write tool: require host authorization.
	if auth == nil {
		return ErrExecutionNotAuthorized
	}
	if err := auth.AuthorizeExecute(ctx, toolName, args); err != nil {
		return errors.Wrapf(err, "execution of write tool %q was not authorized by the host authorizer", toolName)
	}
	return nil
}
```

Notes:

- `errors` here is `emperror.dev/errors` (already imported in gate.go).
- `errors.Wrapf` preserves `errors.Is`/`errors.As` through the chain, so a host
  authorizer that returns `ErrExecutionNotAuthorized` (or its own sentinel)
  remains detectable with stdlib `errors.Is`.

---

## 4. Step 2 — `libs/toolkit/confirm` per-tool helpers

### 4.1 Modify `libs/toolkit/confirm/confirm.go`

Add imports `"context"` and
`"github.com/webcenter-fr/eino-ext/libs/toolkit/safety"` (the current file only
imports `emperror.dev/errors`). There is no import cycle: `safety` does not import
`confirm`.

Deprecate the two existing functions by adding `// Deprecated:` lines to their doc
comments, and add the two context-aware variants. Final file:

```go
// Package confirm provides shared helpers for gating destructive tool
// operations behind an explicit confirmation flag and, for real execution, a
// host-app authorization carried in the context.
package confirm

import (
	"context"

	"emperror.dev/errors"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/safety"
)

// RequireConfirmation returns an error when a mutating operation has neither
// been requested as a dry run nor explicitly confirmed. It centralizes the
// canonical "preview first, then confirm" message used by write tools.
//
// Deprecated: RequireConfirmation trusts the model-supplied confirmed flag and
// MUST NOT be used as an authorization boundary. Use RequireConfirmationCtx,
// which additionally requires safety.ExecutionAuthorizedFor on the context.
func RequireConfirmation(dryRun, confirmed bool) error {
	if !dryRun && !confirmed {
		return errors.New("confirmed must be true to execute (set dryRun=true first to preview)")
	}
	return nil
}

// RequireConfirmationCtx behaves like RequireConfirmation but, when executing
// (dryRun == false and confirmed == true), additionally requires that the host
// application authorized execution for toolName via safety.WithExecutionAuthorized.
//
// Rules, in order:
//   - dryRun -> nil (previews are always allowed).
//   - not confirmed -> the canonical "confirmed must be true" error.
//   - confirmed but safety.ExecutionAuthorizedFor(ctx, toolName) == false ->
//     safety.ErrExecutionNotAuthorized (fail closed).
//   - otherwise -> nil.
func RequireConfirmationCtx(ctx context.Context, toolName string, dryRun, confirmed bool) error {
	if dryRun {
		return nil
	}
	if !confirmed {
		return errors.New("confirmed must be true to execute (set dryRun=true first to preview)")
	}
	if !safety.ExecutionAuthorizedFor(ctx, toolName) {
		return safety.ErrExecutionNotAuthorized
	}
	return nil
}

// RequireConfirmationForAction returns an action-scoped confirmation error when
// confirmed is false. It is intended for tools that handle the dry-run path
// separately and only need to enforce confirmation before executing.
//
// Deprecated: RequireConfirmationForAction trusts the model-supplied confirmed
// flag and MUST NOT be used as an authorization boundary. Use
// RequireConfirmationForActionCtx instead.
func RequireConfirmationForAction(action string, confirmed bool) error {
	if !confirmed {
		return errors.Errorf("%s aborted: Confirmed must be true. Use DryRun first to preview, then set Confirmed=true to proceed.", action)
	}
	return nil
}

// RequireConfirmationForActionCtx behaves like RequireConfirmationForAction but,
// when confirmed == true, additionally requires that the host application
// authorized execution for toolName via safety.WithExecutionAuthorized.
//
// Rules, in order:
//   - not confirmed -> the action-scoped "Confirmed must be true" error.
//   - confirmed but safety.ExecutionAuthorizedFor(ctx, toolName) == false ->
//     safety.ErrExecutionNotAuthorized (fail closed).
//   - otherwise -> nil.
func RequireConfirmationForActionCtx(ctx context.Context, toolName, action string, confirmed bool) error {
	if !confirmed {
		return errors.Errorf("%s aborted: Confirmed must be true. Use DryRun first to preview, then set Confirmed=true to proceed.", action)
	}
	if !safety.ExecutionAuthorizedFor(ctx, toolName) {
		return safety.ErrExecutionNotAuthorized
	}
	return nil
}
```

---

## 5. Step 3 — `components/middleware/safety`

### 5.1 Modify `config.go`

Add two fields to `Config` (after `Policy`, before `CheckOwnership`), and update
the package doc comment's gate bullet to mention authorization. New fields:

```go
	// ExecutionAuthorizer gates real execution of write tools. When nil, write
	// tools may only dry-run: a confirmed=true call is rejected with
	// safety.ErrExecutionNotAuthorized. Implementations must derive the decision
	// from server-side state and must not trust model-supplied arguments.
	ExecutionAuthorizer safety.ExecutionAuthorizer `json:"-"`

	// AllowModelConfirmation restores the pre-hardening behavior where a
	// model-supplied confirmed=true is sufficient to execute a write tool.
	// INSECURE: only for tests and non-production sandboxes.
	AllowModelConfirmation bool `json:"allowModelConfirmation" jsonschema:"description=Insecure escape hatch that trusts model-supplied confirmed=true for write tools (tests/sandboxes only)"`
```

No `validate` tags are required (interface + bool). `New` already calls
`validate.Struct(&c)` — no change to the constructor needed.

### 5.2 Modify `middleware.go`

#### 5.2.1 `preflight` signature and body

Change `preflight` to return the (possibly authorization-marked) context:

```go
func (m *Middleware) preflight(ctx context.Context, toolName, callID, args string, isWrite bool) (context.Context, safety.Phase, error)
```

Replace the gate section (currently lines 205–219) so it reads:

```go
	// Gate check (write tools only).
	gp, gpErr := safety.ExtractGateParams(args)
	if gpErr != nil {
		m.auditReject(ctx, toolName, callID, args, gpErr)
		return ctx, "", gpErr
	}

	var gateErr error
	if m.cfg.AllowModelConfirmation {
		// Legacy, insecure path: trust model-supplied confirmed=true.
		gateErr = safety.ShouldGate(toolName, m.writeTools, gp)
	} else {
		gateErr = safety.ShouldGateWithAuthorization(ctx, toolName, m.writeTools, gp, json.RawMessage(args), m.cfg.ExecutionAuthorizer)
	}
	if gateErr != nil {
		m.auditReject(ctx, toolName, callID, args, gateErr)
		return ctx, "", gateErr
	}

	if gp.DryRun {
		return ctx, safety.PhaseDryRun, nil
	}

	// Real execution authorized: mark the context so the per-tool second layer
	// (confirm.RequireConfirmationCtx / RequireConfirmationForActionCtx) sees it.
	// This runs for BOTH the authorizer path and the AllowModelConfirmation path,
	// so the per-tool layer never rejects a call the middleware already allowed.
	return safety.WithExecutionAuthorized(ctx, toolName), safety.PhaseExecute, nil
```

The policy-denial branch and the `!isWrite` branch must also return `ctx`
instead of nothing:

- policy deny: `return ctx, "", err`
- `!isWrite`: `return ctx, safety.PhaseRead, nil`

(`parseArgs`/policy evaluation at the top of `preflight` is unchanged otherwise.)

#### 5.2.2 The four `Wrap*` variants

Each wrapper must capture the returned context and pass it to the endpoint. The
pattern is identical in all four. For `WrapInvokableToolCall`:

```go
	return func(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
		args := argumentsInJSON

		execCtx, phase, err := m.preflight(ctx, toolName, callID, args, isWrite)
		if err != nil {
			return "", err
		}

		result, err := endpoint(execCtx, argumentsInJSON, opts...)
		m.auditResult(execCtx, toolName, callID, phase, args, result, err)
		...
	}, nil
```

Apply the same change to:

- `WrapStreamableToolCall`: `execCtx, phase, err := m.preflight(...)`; call
  `endpoint(execCtx, ...)`; on error `m.auditResult(execCtx, ...)`; and pass
  `execCtx` (not `ctx`) to `wrapStreamAudit(...)`.
- `WrapEnhancedInvokableToolCall`: same as the invokable case, using
  `toolArg`/`*schema.ToolResult`.
- `WrapEnhancedStreamableToolCall`: same as the streamable case, passing `execCtx`
  to `wrapEnhancedStreamAudit(...)`.

Passing `execCtx` to the audit helpers is safe: `execCtx` derives from `ctx` via
`context.WithValue`, so the session ID set by `activity.WithSession` is preserved.

No other middleware logic changes. `auditReject` already emits a
`PhaseRejected` event with `Error: err.Error()`, which satisfies the "emit
PhaseRejected on denial and include the reason" requirement — the authorization
denial error flows through the same `auditReject` path as a gate error.

---

## 6. Step 4 — migrate write tools to the ctx-aware helpers

For each tool below: replace the `confirm.RequireConfirmation(...)` /
`confirm.RequireConfirmationForAction(...)` call with the ctx-aware variant, using
the tool's exact eino name (the same string passed to `utils.InferTool` /
`utils.InferStreamTool`). The tool already receives `ctx context.Context` as the
first argument of `Invoke` / `InvokeAsStream`.

### 6.1 Kubernetes

| File | Tool name | Replacement |
|---|---|---|
| `resource_create.go` (line 81) | `kubernetes_resource_create` | `confirm.RequireConfirmationCtx(ctx, "kubernetes_resource_create", params.DryRun, params.Confirmed)` |
| `resource_patch.go` (line 77) | `kubernetes_resource_patch` | `confirm.RequireConfirmationCtx(ctx, "kubernetes_resource_patch", params.DryRun, params.Confirmed)` |
| `resource_delete.go` (line 106) | `kubernetes_resource_delete` | `confirm.RequireConfirmationCtx(ctx, "kubernetes_resource_delete", params.DryRun, params.Confirmed)` |
| `resource_apply.go` (line 63) | `kubernetes_resource_apply` | `confirm.RequireConfirmationCtx(ctx, "kubernetes_resource_apply", params.DryRun, params.Confirmed)` |

These four already import `confirm`; keep the import (the function name changes
only).

**`pod_exec.go`** — currently does **not** use `confirm`; it inlines its own
check. Migrate both paths to the shared helper so the authorization second layer
applies uniformly:

- `Invoke` (lines 174–181): keep the `if params.DryRun { return t.dryRunPreview(params), nil }`
  early return, then replace

  ```go
  if !params.Confirmed {
      return "", errors.New("confirmed must be true to execute a command in a pod (set dryRun=true first to preview)")
  }
  ```

  with

  ```go
  if err := confirm.RequireConfirmationCtx(ctx, "kubernetes_pod_exec", params.DryRun, params.Confirmed); err != nil {
      return "", err
  }
  ```

- `InvokeAsStream` (lines 266–276): same replacement (keep the dry-run early
  return; replace the inline `!params.Confirmed` check), returning `nil, err`.

Add the import `"github.com/webcenter-fr/eino-ext/libs/toolkit/confirm"` to
`pod_exec.go`. Note: the error message for an unconfirmed exec changes from the
pod-specific sentence to the canonical
`"confirmed must be true to execute (set dryRun=true first to preview)"` — this is
intentional (consistent UX) and no test asserts the old wording.

### 6.2 Shell

`components/tool/shell/shell.go` — tool name `shell_exec`:

- `Invoke` (line 105): replace
  `confirm.RequireConfirmation(params.DryRun, params.Confirmed)` with
  `confirm.RequireConfirmationCtx(ctx, "shell_exec", params.DryRun, params.Confirmed)`.
- `InvokeAsStream` (line 171): same replacement, returning `nil, err`.

The import `confirm` is already present.

### 6.3 Grafana

`components/tool/grafana/dashboard_write.go` — tool name `grafana_dashboard_write`
(the `dashboardWriteToolName` const in `base.go`):

- Line 118: replace
  `confirm.RequireConfirmation(params.DryRun, params.Confirmed)` with
  `confirm.RequireConfirmationCtx(ctx, dashboardWriteToolName, params.DryRun, params.Confirmed)`.

Use the `dashboardWriteToolName` constant (already in package scope) rather than
the literal string, so the name stays in sync with the registry.

### 6.4 ArgoCD

| File | Tool name | Replacement |
|---|---|---|
| `application_create.go` (line 96) | `argocd_application_create` | `confirm.RequireConfirmationCtx(ctx, "argocd_application_create", params.DryRun, params.Confirmed)` |
| `application_delete.go` (line 71) | `argocd_application_delete` | `confirm.RequireConfirmationCtx(ctx, "argocd_application_delete", params.DryRun, params.Confirmed)` |
| `application_sync.go` (line 51) | `argocd_application_sync` | `confirm.RequireConfirmationCtx(ctx, "argocd_application_sync", params.DryRun, params.Confirmed)` |

Note ordering: in `application_create.go` and `application_delete.go` the dry-run
path returns earlier, so `params.DryRun` is `false` at the check; that is fine —
`RequireConfirmationCtx` handles `dryRun` explicitly. In `application_sync.go` the
call already sits before the dry-run branch.

### 6.5 GitHub (15 write tools)

Each of these currently calls `confirm.RequireConfirmationForAction("...", params.Confirmed)`.
Replace with `confirm.RequireConfirmationForActionCtx(ctx, "<tool name>", "<action>", params.Confirmed)`.

| File | Tool name | Action string |
|---|---|---|
| `branch_create.go` (line 61) | `github_branch_create` | `create branch` |
| `release_create.go` (line 61) | `github_release_create` | `create release` |
| `issue_create.go` (line 51) | `github_issue_create` | `create issue` |
| `issue_comment.go` (line 49) | `github_issue_comment` | `add comment` |
| `pr_create.go` (line 52) | `github_pr_create` | `create pull request` |
| `pr_comment.go` (line 49) | `github_pr_comment` | `add comment` |
| `pr_review.go` (line 54) | `github_pr_review` | `submit review` |
| `pr_suggest_change.go` (line 57) | `github_pr_suggest_change` | `post suggestion` |
| `pr_request_reviewers.go` (line 49) | `github_pr_request_reviewers` | `request reviewers` |
| `repo_settings_update.go` (line 58) | `github_repo_settings_update` | `update repository settings` |
| `webhook_upsert.go` (line 65) | `github_webhook_upsert` | `create/update webhook` |
| `file_write.go` (line 85) | `github_file_write` | `write file` |
| `file_delete.go` (line 110) | `github_file_delete` | `delete file` |
| `file_copy.go` (line 85) | `github_file_copy` | `copy file` |
| `file_move.go` (line 87) | `github_file_move` | `move file` |

Preserve the exact existing action string (it is user-visible in error messages).

### 6.6 NOT migrated (documented, out of scope)

`repo_clone.go` and `repo_pull.go` call `RequireConfirmationForAction` but are
classified **read-only** in `github/registry.go` (`readOnlyConstructors`), so they
are **not** in `WriteToolNames()`. The middleware therefore never authorizes them
and never marks their context. Migrating them to `RequireConfirmationForActionCtx`
would reject every call. Leave them on the now-deprecated
`RequireConfirmationForAction` (behavior unchanged) and record this inconsistency
as a follow-up. See §10 "Out of scope".

---

## 7. Step 5 — `WriteToolNames()` contract documentation

Add the following contract sentence to the doc comment of `WriteToolNames()` in
each of the five registries. The function bodies are unchanged.

```go
// WriteToolNames returns the tool names of all X write tools.
// These names can be passed to the safety middleware's Config.WriteToolNames.
//
// Contract: every name listed here MUST honor dryRun=true as a no-side-effect
// preview. The safety gate treats dry-run as always-safe, so a tool that mutates
// during dry-run would let an unconfirmed model call bypass the gate.
func WriteToolNames() []string {
```

Files:

- `components/tool/kubernetes/registry.go`
- `components/tool/shell/registry.go`
- `components/tool/grafana/registry.go`
- `components/tool/argocd/registry.go`
- `components/tool/github/registry.go`

---

## 8. Step 6 — tests

### 8.1 `libs/toolkit/safety/authorization_test.go` (new)

Use stdlib `errors` (`errors.Is`) and `encoding/json`.

`TestShouldGateWithAuthorization` — table test, `writeTools := map[string]bool{"write": true}`:

| Case | args | expected |
|---|---|---|
| read tool | toolName `"read"` | `err == nil` |
| write dry-run | `GateParams{DryRun:true}` | `err == nil` |
| write both dryRun+confirmed | `GateParams{DryRun:true, Confirmed:true}` | `err == nil` (dry-run precedence) |
| write neither | `GateParams{}` | `errors.Is(err, ErrGateRequired)` |
| write confirmed, auth nil | `GateParams{Confirmed:true}`, `auth == nil` | `errors.Is(err, ErrExecutionNotAuthorized)` |
| write confirmed, auth allows | `auth` returns `nil` | `err == nil` |
| write confirmed, auth denies | `auth` returns sentinel `ErrExecutionNotAuthorized` | `errors.Is(err, ErrExecutionNotAuthorized)` |
| write confirmed, auth denies custom | `auth` returns `errors.New("denied-by-policy")` | `err != nil` and `strings.Contains(err.Error(), "denied-by-policy")` and `strings.Contains(err.Error(), "write")` |
| unknown tool name | toolName `"unknown"`, `GateParams{}` | `err == nil` (treated read-only) |

Use a tiny fake authorizer:

```go
type fakeAuthorizer struct{ fn func(ctx context.Context, toolName string, args json.RawMessage) error }
func (f fakeAuthorizer) AuthorizeExecute(ctx context.Context, toolName string, args json.RawMessage) error {
	return f.fn(ctx, toolName, args)
}
```

`TestWithExecutionAuthorizedAndFor` — assert:

- `ExecutionAuthorizedFor(context.Background(), "t")` is false before any grant.
- After `ctx := WithExecutionAuthorized(context.Background(), "t")`,
  `ExecutionAuthorizedFor(ctx, "t")` is true and `ExecutionAuthorizedFor(ctx, "other")` is false.
- `ExecutionAuthorizedFor(nil, "t")` is false (no panic).
- `WithExecutionAuthorized(nil, "t")` returns nil (no panic).
- `ExecutionAuthorizedFor(ctx, "")` is false; `WithExecutionAuthorized(ctx, "")` returns ctx unchanged.
- Two grants accumulate: `ctx = WithExecutionAuthorized(ctx, "a"); ctx = WithExecutionAuthorized(ctx, "b")` — both `a` and `b` report true.

### 8.2 `libs/toolkit/confirm/confirm_test.go` (new)

`TestRequireConfirmationCtx`:

- `(dryRun=true, confirmed=false, unauthorized ctx)` → nil.
- `(dryRun=false, confirmed=false)` → non-nil error containing `"confirmed must be true"`.
- `(dryRun=false, confirmed=true, authorized ctx)` → nil.
- `(dryRun=false, confirmed=true, unauthorized ctx)` → `errors.Is(err, safety.ErrExecutionNotAuthorized)`.
- `(dryRun=false, confirmed=true, nil ctx)` → `errors.Is(err, safety.ErrExecutionNotAuthorized)`.

`TestRequireConfirmationForActionCtx`:

- `confirmed=false` → error containing `"Confirmed must be true"`.
- `confirmed=true, authorized ctx` → nil.
- `confirmed=true, unauthorized ctx` → `errors.Is(err, safety.ErrExecutionNotAuthorized)`.

Construct authorized contexts via
`safety.WithExecutionAuthorized(context.Background(), toolName)`.

### 8.3 `components/middleware/safety/middleware_test.go` (modify + add)

Add a package-level helper authorizer:

```go
type stubAuthorizer struct{ err error }
func (s stubAuthorizer) AuthorizeExecute(context.Context, string, json.RawMessage) error { return s.err }
```

(import `encoding/json`.)

**Modify existing tests (3):**

1. `TestWrapInvokableToolCallWriteConfirmed` (line 173): add
   `ExecutionAuthorizer: stubAuthorizer{}` to the Config. Inside the endpoint,
   assert `safety.ExecutionAuthorizedFor(ctx, "write_tool")` is true. Keep the
   `PhaseExecute` audit assertion.
2. `TestWrapStreamableToolCallStreamError` (line 536): its `{"confirmed":true}`
   call would now be rejected. Add `ExecutionAuthorizer: stubAuthorizer{}` to the
   Config so the endpoint still runs. (This test is about stream error
   propagation, not the gate.)
3. `TestWrapEnhancedStreamableToolCallStreamError` (line 599): same as #2.

**Add new tests (one per `Wrap*` variant = 4 total, each with the same four sub-cases):**

For `WrapInvokableToolCall`, `WrapStreamableToolCall`,
`WrapEnhancedInvokableToolCall`, `WrapEnhancedStreamableToolCall`, add a test
`TestWrap<Variant>WriteConfirmedAuthorization` that covers:

- **no authorizer** → `confirmed:true` call returns
  `errors.Is(err, safety.ErrExecutionNotAuthorized)`; the endpoint is **not**
  invoked; a `PhaseRejected` audit event is emitted (for streaming variants,
  `wrapped(...)` returns a non-nil error directly, i.e. no stream is produced).
- **authorizer denies** (`stubAuthorizer{err: errors.New("denied")}`) → endpoint
  not invoked; `PhaseRejected`; error contains `"denied"`.
- **authorizer allows** (`stubAuthorizer{}`) → endpoint invoked; the endpoint's
  `ctx` reports `safety.ExecutionAuthorizedFor(ctx, "write_tool") == true`;
  `PhaseExecute` audit event (for streaming, consume the stream to EOF first).
- **AllowModelConfirmation** (Config with `AllowModelConfirmation: true`, no
  authorizer) → endpoint invoked (legacy behavior); endpoint ctx reports
  authorized; `PhaseExecute` audit.

For the streaming variants, the "endpoint not invoked" cases assert the wrapped
call returns `err != nil` immediately (the gate runs before the endpoint).

Use a `safety.ChannelSink` to read audit events exactly as the existing tests do.

### 8.4 Tool tests — wrap the context for `confirmed:true` paths

Because the authorization check now runs **early** in each tool (right after
param validation, before the operation and before later error paths), every test
that passes `confirmed:true` (or `"confirmed": true` JSON) and expects the tool to
proceed past confirmation must run with an authorized context. Do **not** flip
`AllowModelConfirmation` in tool tests — wrap the context instead, so the real
path stays exercised.

Mechanical pattern per test package:

```go
import safety "github.com/webcenter-fr/eino-ext/libs/toolkit/safety"

authorizedCtx := safety.WithExecutionAuthorized(context.Background(), "<tool name>")
```

Then replace `context.Background()` / the existing `ctx` at the call site with
`authorizedCtx` for every `confirmed:true` invocation.

**Grafana** (tool name `grafana_dashboard_write`):

- `dashboard_write_test.go`: ~40 struct-literal `Confirmed: true` sites that call
  `tool.Invoke(context.Background(), &DashboardWriteParams{...})`. Introduce an
  `authorizedCtx` and use it at those call sites.
- `grafana_test.go` `TestDashboardWrite`: wrap the `ctx` for the `create
  confirmed`, `update existing`, `delete confirmed`, `delete protected by uid`,
  `delete nonexistent`, `update protected by uid`, `missing title`, `invalid
  json`, and `unknown instance` subtests (all use `confirmed:true`).
- `security_test.go` (line 157) and `integration_test.go` (confirmed cases): same
  wrapping.

Do **not** change dry-run subtests (they pass `dryRun:true` only) or the
`no confirmation` subtest (it asserts the `confirmed must be true` error, which is
unchanged).

**ArgoCD** (`application_test.go`):

- `TestApplicationSync` → `safety.WithExecutionAuthorized(ctx, "argocd_application_sync")`.
- `TestApplicationCreate` → `... , "argocd_application_create"`.
- `TestApplicationDelete` → `... , "argocd_application_delete"`.

**GitHub**:

- `pr_test.go` → wrap per tool: `github_pr_create`, `github_pr_comment`,
  `github_pr_review`, `github_pr_suggest_change`, `github_pr_request_reviewers`.
- `issue_test.go` → `github_issue_create`, `github_issue_comment`.
- `repo_test.go` → `github_webhook_upsert`, `github_repo_settings_update`,
  `github_release_create`, `github_branch_create`.
- `file_test.go` → `github_file_write`, `github_file_delete`, `github_file_copy`,
  `github_file_move`.
- `repo_pull_test.go` → **no change** (`repo_pull` is not migrated).

### 8.5 New per-tool "direct invocation unauthorized" tests

Each asserts the second layer alone: calling the tool directly (no middleware)
with `confirmed:true` and an **unauthorized** context is refused, and with an
**authorized** context proceeds.

- `components/tool/grafana/dashboard_write_test.go`:
  `TestDashboardWriteUnauthorizedContext` — `tool.Invoke(context.Background(),
  &DashboardWriteParams{Instance:"test", Operation:"create", Dashboard:..., Confirmed:true})`
  returns `errors.Is(err, safety.ErrExecutionNotAuthorized)` (use the existing
  `newDashboardWriteTool` helper). The authorization check fires before any HTTP
  request, so the handler is not hit.
- `components/tool/argocd/application_test.go`:
  `TestApplicationSyncUnauthorizedContext` — `syncTool.InvokableRun(ctx,
  {"confirmed":true})` with plain `ctx` returns
  `errors.Is(err, safety.ErrExecutionNotAuthorized)`.
- `components/tool/github/file_test.go`:
  `TestFileWriteUnauthorizedContext` — `fileWriteTool.InvokableRun(ctx,
  {"confirmed":true, ...valid fields})` returns
  `errors.Is(err, safety.ErrExecutionNotAuthorized)`.
- `components/tool/kubernetes` (new small test, e.g. in a new
  `resource_create_authorization_test.go` or `tool_test.go`):
  `TestResourceCreateUnauthorizedContext` — construct
  `NewResourceCreateTool(ctx, minimalConfigs)` (no cluster needed); call
  `Invoke(context.Background(), &ResourceCreateParams{...valid, Confirmed:true})`
  and assert `errors.Is(err, safety.ErrExecutionNotAuthorized)`. The check fires
  after `validate.Struct` and before any cluster resolution, so no envtest is
  required. (If constructing a Configs without a live client is not possible,
  place this assertion against `confirm.RequireConfirmationCtx` directly — but
  prefer the tool-level test.)
- Shell: the execute path needs a Dagger engine, so no direct tool-level
  execute test. Coverage comes from the `confirm` package tests and the
  middleware tests. Add only the dry-run contract test (§8.6).

### 8.6 Dry-run no-mutation contract tests (per registry, §7 contract)

Each asserts that invoking every name in the registry's `WriteToolNames()` with
`dryRun:true` performs no mutation. Practical per registry:

- **Grafana** (`dashboard_write_test.go`): `TestDryRunNoMutation` — use
  `httptest` handler that `t.Fatalf`s if any `POST /api/dashboards/db` or
  `DELETE /api/dashboards/uid/...` arrives; invoke create/update/delete with
  `dryRun:true` and assert the preview is returned and no mutating request
  occurred. (The existing suite already has GET handlers for update/delete dry-run.)
- **ArgoCD** (`application_test.go`): `TestDryRunNoMutation` — extend the mock to
  record methods; assert `create`/`delete` dry-run issue no `POST`/`DELETE`, and
  `sync` dry-run sends a sync request with `dryRun:true` (server-side dry-run).
- **Shell** (`shell_test.go`): `TestDryRunNoMutation` — use a zero-value
  `&Tool{}` (nil blocklist); `Invoke(ctx, &Params{Command: []string{"echo","x"}, DryRun:true})`
  returns a preview containing `"dryRun": true` and performs no Dagger/session
  work (it returns before `resolveProfile`).
- **Kubernetes** (`tool_test.go`, envtest-backed): `TestDryRunNoMutation` — use
  the existing `t.k8sClient`/envtest; create a ConfigMap with `dryRun:true`
  (server-side dry-run) and assert the ConfigMap does **not** exist afterward;
  for delete, seed a ConfigMap, delete with `dryRun:true`, assert it still exists.
  This is the heaviest contract test and may be marked `//go:build`-gated or
  kept in the existing envtest suite. If envtest is unavailable in CI, document
  that the create/delete dry-run paths are already covered by the server-side
  `DryRunAll` option and rely on the code-level guarantee + grafana/argocd tests.
- **GitHub** (`file_test.go`): `TestDryRunNoMutation` — `fileWriteTool.InvokableRun(ctx,
  {dryRun:true, path, branch, commitMessage})` returns a preview and does not
  create a clone dir or write a file (the dry-run branch returns before
  `ensureCloneExists`).

---

## 9. Step 7 — documentation

### 9.1 `libs/toolkit/safety/README.md`

Add an **Authorization** section documenting:

- `ExecutionAuthorizer` interface and `AuthorizeExecute`.
- `ErrExecutionNotAuthorized` and the fail-closed default.
- `WithExecutionAuthorized` / `ExecutionAuthorizedFor`.
- Replace the `ShouldGate` reference in the **Gate** section with
  `ShouldGateWithAuthorization` (keep `ShouldGate` marked deprecated).

### 9.2 `libs/toolkit/confirm/README.md`

Document `RequireConfirmationCtx` and `RequireConfirmationForActionCtx`; mark the
old functions deprecated; note the new functions require
`safety.WithExecutionAuthorized` on the context for real execution.

### 9.3 `components/middleware/safety/README.md`

- Update the **Gate** description: with no `ExecutionAuthorizer`, write tools may
  only dry-run.
- Add `ExecutionAuthorizer` and `AllowModelConfirmation` to the configuration
  table, and a short migration snippet showing a host implementing
  `ExecutionAuthorizer`.
- Add a prominent note that the default is fail-closed and that
  `AllowModelConfirmation: true` is an insecure escape hatch.

### 9.4 `BREAKING_CHANGE.md`

Add a new top section (above the existing entries):

```md
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
```

---

## 10. Edge cases (explicit behavior)

| Case | Behavior |
|---|---|
| Read-only tool (not in `writeTools`) | `ShouldGateWithAuthorization` returns nil; middleware phase `PhaseRead`; context not marked. |
| Unknown tool name (not in `writeTools`) | Treated as read-only → allowed. |
| `dryRun:true` (write tool) | Always allowed; context not marked; phase `PhaseDryRun`; per-tool `RequireConfirmationCtx` returns nil without authorization. |
| `dryRun:true` + `confirmed:true` | Allowed (dry-run precedence, rule 2 before rule 3). |
| `confirmed:false`, no dryRun | `ErrGateRequired` (middleware) / `"confirmed must be true…"` (per-tool). |
| `confirmed:true`, no authorizer | `ErrExecutionNotAuthorized` (fail closed) at both layers. |
| `confirmed:true`, authorizer returns error | That error wrapped with the tool name; `errors.Is` preserved. |
| `AllowModelConfirmation:true` | Legacy `ShouldGate` used; context still marked authorized on execute so the per-tool layer does not reject. |
| nil `ctx` in `WithExecutionAuthorized` | No-op, returns nil. |
| nil `ctx` in `ExecutionAuthorizedFor` | Returns false. |
| empty `toolName` | `ExecutionAuthorizedFor` returns false; `WithExecutionAuthorized` no-op. |
| nil `ctx` passed to `ShouldGateWithAuthorization` with an authorizer | Passed through to the authorizer; authorizers must not panic (documented contract). The middleware never passes nil. |
| Direct tool invocation (outside middleware), `confirmed:true`, no grant | Refused at the per-tool layer with `ErrExecutionNotAuthorized`. Callers who own the decision call `safety.WithExecutionAuthorized(ctx, toolName)` first. |
| Streaming tool (`pod_exec`, `shell`) | Same gate in `preflight` before the endpoint; streamed `InvokeAsStream` re-checks authorization in the tool. |

---

## 11. Error handling summary

- Use `emperror.dev/errors` for new wrapped errors (matches repo convention).
- `ErrExecutionNotAuthorized` and `ErrGateRequired` are sentinels compared with
  stdlib `errors.Is`.
- `ShouldGateWithAuthorization` returns `ErrExecutionNotAuthorized` directly for
  the nil-authorizer case and wraps the authorizer's error with the tool name
  otherwise.
- Middleware denial flows through `auditReject`, producing a `PhaseRejected`
  audit event whose `Error` field is `err.Error()` and `PolicyPass` is true
  (policy already passed). Policy denial continues to emit `PolicyPass: false`.
- No panics; nil contexts fail closed.

---

## 12. Implementation order (bottom-up)

1. `libs/toolkit/safety`: create `authorization.go`; edit `gate.go`; add
   `authorization_test.go`. Run `go test ./libs/toolkit/safety/...`.
2. `libs/toolkit/confirm`: edit `confirm.go`; add `confirm_test.go`. Run
   `go test ./libs/toolkit/confirm/...`.
3. `components/middleware/safety`: edit `config.go` and `middleware.go`; update
   and extend `middleware_test.go`. Run
   `go test ./components/middleware/safety/...`.
4. Migrate tools: kubernetes → shell → grafana → argocd → github (§6).
5. Add `WriteToolNames()` contract doc comments (§7).
6. Update tool tests for authorized contexts (§8.4) and add new per-tool and
   dry-run contract tests (§8.5, §8.6).
7. Update documentation (§9) and `BREAKING_CHANGE.md`.
8. Final: `go build ./...`, `go vet ./...`, `go test ./...`.

---

## 13. Validation

- `go build ./... && go vet ./... && go test ./...` pass.
- New sentinel and context-carrier unit tests pass.
- Middleware authorization matrix passes for all four `Wrap*` variants.
- Existing tool suites pass with the authorized-context updates; no test relies
  on `AllowModelConfirmation`.

## 14. Out of scope / follow-ups (explicit)

- `github/repo_clone.go` and `github/repo_pull.go` are classified read-only in the
  registry but still call the (now deprecated) `RequireConfirmationForAction`.
  They are deliberately not migrated (see §6.6) because they are not gated by the
  middleware. Follow-up: decide whether they should be write tools (they mutate
  local clone state) or drop their confirmation gate.
- Deciding *how* a host authorizes (UI button, signed token, policy engine) is the
  host's concern, not this change.
- `ShouldGate`, `RequireConfirmation`, and `RequireConfirmationForAction` are
  deprecated now, not removed; removal is deferred to a future major.
