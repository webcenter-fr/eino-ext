package file

import (
	"context"
	"fmt"
	"os"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/checkup"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/fileutil"
)

// Check performs a health check against the configured file tools workdir.
func Check(ctx context.Context, cfg *Config) checkup.Results {
	if cfg == nil || cfg.Workdir == "" {
		return checkup.Results{{
			Component: "file",
			Status:    checkup.StatusError,
			Error:     "Workdir is not configured",
		}}
	}

	if err := fileutil.ValidateRootDir(cfg.Workdir); err != nil {
		return checkup.Results{{
			Component: "file",
			Status:    checkup.StatusError,
			Error:     err.Error(),
		}}
	}

	// Probe: try to create and remove a test directory.
	testDir := fmt.Sprintf("%s/__checkup_test", cfg.Workdir)
	if err := os.MkdirAll(testDir, 0o755); err != nil {
		return checkup.Results{{
			Component: "file",
			Status:    checkup.StatusError,
			Error:     fmt.Sprintf("failed to create test directory in Workdir: %v", err),
		}}
	}
	_ = os.RemoveAll(testDir)

	return checkup.Results{{
		Component: "file",
		Status:    checkup.StatusOK,
		Message:   "Workdir is writable",
	}}
}
