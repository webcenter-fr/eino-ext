package otelmetrics

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/metric/noop"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/checkup"
)

// Check probes the scope's readiness: it verifies that the MeterProvider is
// non-noop and a test counter can be created. Returns limited (not error) when
// the provider is the noop provider (no exporter configured).
func Check(ctx context.Context, s *Scope) checkup.Results {
	var results checkup.Results

	if s == nil {
		results = append(results, checkup.Result{
			Component: "otelmetrics",
			Status:    checkup.StatusError,
			Error:     "scope is nil",
		})
		return results
	}

	mp := s.Provider()
	if mp == nil {
		results = append(results, checkup.Result{
			Component: "otelmetrics",
			Status:    checkup.StatusError,
			Error:     "MeterProvider is nil",
		})
		return results
	}

	if _, ok := mp.(noop.MeterProvider); ok {
		results = append(results, checkup.Result{
			Component: "otelmetrics",
			Status:    checkup.StatusLimited,
			Message:   "MeterProvider is the noop provider (no OTel exporter configured)",
		})
		return results
	}

	c, err := s.FloatCounter("checkup.test.counter", "checkup probe", "1")
	if err != nil {
		results = append(results, checkup.Result{
			Component: "otelmetrics",
			Status:    checkup.StatusError,
			Error:     fmt.Sprintf("failed to create test counter: %v", err),
		})
		return results
	}
	if c == nil {
		results = append(results, checkup.Result{
			Component: "otelmetrics",
			Status:    checkup.StatusError,
			Error:     "created counter is nil",
		})
		return results
	}

	results = append(results, checkup.Result{
		Component: "otelmetrics",
		Status:    checkup.StatusOK,
		Message:   "scope is ready and test counter created",
	})
	return results
}
