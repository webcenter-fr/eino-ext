package otelmetrics

import (
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
)

// globalMeterProvider returns the global MeterProvider. Extracted into a
// variable so tests can override it.
var globalMeterProvider = func() metric.MeterProvider {
	return otel.GetMeterProvider()
}
