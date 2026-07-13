package agentattr

import (
	"context"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/checkup"
)

// Check probes the agentattr middleware. Since agentattr is a pure middleware
// with no external dependencies, it always returns a single OK result.
func Check(_ context.Context, _ *Config) checkup.Results {
	return checkup.Results{{
		Component: "agentattr",
		Status:    checkup.StatusOK,
		Message:   "pure middleware, no external dependencies to probe",
	}}
}
