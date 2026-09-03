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
