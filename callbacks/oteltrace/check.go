package oteltrace

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/trace"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/checkup"
)

// Check probes the trace handler's readiness by verifying that the tracer
// provider is non-noop. Returns limited (not error) when the provider is the
// noop provider (no exporter configured).
func Check(ctx context.Context, h *Handler) checkup.Results {
	var results checkup.Results

	if h == nil {
		results = append(results, checkup.Result{
			Component: "oteltrace_handler",
			Status:    checkup.StatusError,
			Error:     "handler is nil",
		})
		return results
	}

	// Check if the tracer is a noop by trying to start a span and checking if it's recording.
	_, span := h.tracer.Start(ctx, "checkup.probe")
	defer span.End()

	if !span.IsRecording() {
		results = append(results, checkup.Result{
			Component: "oteltrace_handler",
			Status:    checkup.StatusLimited,
			Message:   "tracer provider is noop (no OTel exporter configured)",
		})
		return results
	}

	results = append(results, checkup.Result{
		Component: "oteltrace_handler",
		Status:    checkup.StatusOK,
		Message:   fmt.Sprintf("tracer is recording (name=%s)", h.cfg.TracerName),
	})
	return results
}

// Ensure noop trace provider is detected.
var _ trace.TracerProvider = trace.NewNoopTracerProvider()
