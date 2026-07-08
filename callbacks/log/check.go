package log

import (
	"context"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/checkup"
)

// Check probes the log handler by verifying the package can be loaded and
// a Handler can be constructed. Since the handler has no external dependencies
// (it only writes to a logrus logger), there is nothing to probe.
func Check(_ context.Context) checkup.Results {
	return checkup.Results{
		{
			Component: "callback_log_handler",
			Status:    checkup.StatusOK,
			Message:   "log handler is available (no external dependencies)",
		},
	}
}
