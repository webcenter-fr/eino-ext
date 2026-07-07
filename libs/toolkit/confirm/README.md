# confirm — Confirmation gate for destructive tool operations

`confirm` centralizes the "preview first, then confirm" gate shared by write
tools across the Kubernetes and ArgoCD components, so the wording and semantics
stay consistent.

## Functions

```go
func RequireConfirmation(dryRun, confirmed bool) error
func RequireConfirmationForAction(action string, confirmed bool) error
```

- `RequireConfirmation` — returns an error unless the call is a dry run or has
  been explicitly confirmed. Use it for tools that expose both `dryRun` and
  `confirmed` flags.
- `RequireConfirmationForAction` — returns an action-scoped error when
  `confirmed` is false. Use it for tools that handle the dry-run path separately
  and only need to enforce confirmation before executing.

## Usage

```go
import "github.com/webcenter-fr/eino-ext/libs/toolkit/confirm"

if err := confirm.RequireConfirmation(params.DryRun, params.Confirmed); err != nil {
    return "", err
}
```
