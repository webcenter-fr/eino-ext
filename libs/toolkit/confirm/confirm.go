// Package confirm provides shared helpers for gating destructive tool
// operations behind an explicit confirmation flag and, for real execution, a
// host-app authorization carried in the context.
package confirm

import (
	"context"

	"emperror.dev/errors"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/safety"
)

// RequireConfirmationCtx returns nil for a dry run; otherwise it requires
// confirmed to be true and, for real execution, that the host application
// authorized execution for toolName via safety.WithExecutionAuthorized.
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

// RequireConfirmationForActionCtx returns an action-scoped error when confirmed
// is false; when confirmed is true it additionally requires that the host
// application authorized execution for toolName via
// safety.WithExecutionAuthorized.
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
