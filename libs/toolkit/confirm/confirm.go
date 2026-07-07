// Package confirm provides shared helpers for gating destructive tool
// operations behind an explicit confirmation flag.
package confirm

import "emperror.dev/errors"

// RequireConfirmation returns an error when a mutating operation has neither
// been requested as a dry run nor explicitly confirmed. It centralizes the
// canonical "preview first, then confirm" message used by write tools.
func RequireConfirmation(dryRun, confirmed bool) error {
	if !dryRun && !confirmed {
		return errors.New("confirmed must be true to execute (set dryRun=true first to preview)")
	}
	return nil
}

// RequireConfirmationForAction returns an action-scoped confirmation error when
// confirmed is false. It is intended for tools that handle the dry-run path
// separately and only need to enforce confirmation before executing.
func RequireConfirmationForAction(action string, confirmed bool) error {
	if !confirmed {
		return errors.Errorf("%s aborted: Confirmed must be true. Use DryRun first to preview, then set Confirmed=true to proceed.", action)
	}
	return nil
}
