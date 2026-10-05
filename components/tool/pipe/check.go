package pipe

import (
	"context"
	"fmt"

	"github.com/webcenter-fr/eino-ext/components/tool/shell"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/checkup"
)

// Check performs a health check against the pipe configuration by delegating to
// the underlying shell check and reporting the number of registered tools.
func Check(ctx context.Context, cfg *Config) checkup.Results {
	if cfg == nil {
		return checkup.Results{{
			Component: "pipe_exec",
			Status:    checkup.StatusError,
			Error:     "no pipe config provided",
		}}
	}
	if cfg.Shell == nil {
		return checkup.Results{{
			Component: "pipe_exec",
			Status:    checkup.StatusError,
			Error:     "shell config is required",
		}}
	}

	results := shell.Check(ctx, cfg.Shell)
	results = append(results, checkup.Result{
		Component: "pipe_exec",
		Status:    checkup.StatusOK,
		Message:   fmt.Sprintf("%d tools registered", len(cfg.Tools)),
	})
	return results
}
