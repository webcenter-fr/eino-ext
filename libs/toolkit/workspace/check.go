package workspace

import (
	"context"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/checkup"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/fileutil"
)

// Check probes a workspace layout: the root must be a safe host directory and
// the session namespace must resolve (or fail closed as configured).
func Check(ctx context.Context, layout *Layout) checkup.Results {
	if layout == nil {
		return checkup.Results{{
			Component: "workspace",
			Status:    checkup.StatusError,
			Error:     "no workspace layout provided",
		}}
	}

	if err := fileutil.ValidateRootDir(layout.Root); err != nil {
		return checkup.Results{{
			Component: "workspace",
			Status:    checkup.StatusError,
			Error:     err.Error(),
		}}
	}

	dir, err := layout.SessionDir(ctx)
	if err != nil {
		return checkup.Results{{
			Component: "workspace",
			Status:    checkup.StatusError,
			Error:     err.Error(),
		}}
	}

	return checkup.Results{{
		Component: "workspace",
		Status:    checkup.StatusOK,
		Message:   "workspace root is valid; session namespace: " + dir,
	}}
}
