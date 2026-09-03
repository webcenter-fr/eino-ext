# confirm — Confirmation gate for destructive tool operations

`confirm` centralizes the "preview first, then confirm" gate shared by write
tools across the Kubernetes, ArgoCD, Grafana, shell, and GitHub components, so
the wording and semantics stay consistent.

## Functions

```go
func RequireConfirmationCtx(ctx context.Context, toolName string, dryRun, confirmed bool) error
func RequireConfirmationForActionCtx(ctx context.Context, toolName, action string, confirmed bool) error
```

- `RequireConfirmationCtx` — returns an error unless the call is a dry run or
  has been explicitly confirmed. Real execution (`dryRun=false, confirmed=true`)
  additionally requires that the host application authorized execution for
  `toolName` via `safety.WithExecutionAuthorized`; otherwise it fails closed
  with `safety.ErrExecutionNotAuthorized`. Use it for tools that expose both
  `dryRun` and `confirmed` flags.
- `RequireConfirmationForActionCtx` — returns an action-scoped error when
  `confirmed` is false; when `confirmed` is true it applies the same
  `safety.WithExecutionAuthorized` authorization requirement. Use it for tools
  that handle the dry-run path separately and only need to enforce confirmation
  before executing.

The old non-context helpers are deprecated:

- `RequireConfirmation` — trusts the model-supplied confirmed flag and MUST
  NOT be used as an authorization boundary. Use `RequireConfirmationCtx`.
- `RequireConfirmationForAction` — same; use `RequireConfirmationForActionCtx`.

## Usage

```go
import "github.com/webcenter-fr/eino-ext/libs/toolkit/confirm"

if err := confirm.RequireConfirmationCtx(ctx, "kubernetes_resource_create", params.DryRun, params.Confirmed); err != nil {
    return "", err
}
```

Real execution only proceeds when the context carries a grant:

```go
import "github.com/webcenter-fr/eino-ext/libs/toolkit/safety"

// The host application (or the safety middleware) marks the tool authorized:
ctx = safety.WithExecutionAuthorized(ctx, "kubernetes_resource_create")
```
