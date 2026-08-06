// Package otelmetrics is a thin, reusable wrapper over the OpenTelemetry
// global MeterProvider. It is the single "global metric scope" for eino-ext:
// any component can record metrics here and they flow to the host app's
// configured exporter (OTLP, stdout, prometheus bridge). The default provider
// is metric/global.MeterProvider(); an explicit provider can be injected via
// Config for tests.
package otelmetrics

import (
	"context"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
)

// Config for NewScope. Defaults: MeterName="eino-ext", MeterProvider=global.
type Config struct {
	MeterProvider metric.MeterProvider `json:"-"`
	MeterName     string               `json:"meterName" validate:"omitempty"`
}

// Scope is a configured metric scope. All instrument helpers are nil-receiver
// safe (no-op on a nil Scope) so callers degrade safely.
type Scope struct {
	meter    metric.Meter
	provider metric.MeterProvider
}

// NewScope applies defaults (MeterName="eino-ext", provider=global) then
// validates via libs/toolkit/validate.Struct. ctx threads to future client
// creation; provider resolution is synchronous.
func NewScope(ctx context.Context, cfg *Config) (*Scope, error) {
	if cfg == nil {
		cfg = &Config{}
	}
	if cfg.MeterName == "" {
		cfg.MeterName = "eino-ext"
	}
	if cfg.MeterProvider == nil {
		cfg.MeterProvider = globalMeterProvider()
	}
	if err := validate.Struct(cfg); err != nil {
		return nil, err
	}
	meter := cfg.MeterProvider.Meter(cfg.MeterName)
	return &Scope{
		meter:    meter,
		provider: cfg.MeterProvider,
	}, nil
}

// Meter exposes the underlying metric.Meter (or a noop meter on nil).
func (s *Scope) Meter() metric.Meter {
	if s == nil {
		return noop.Meter{}
	}
	return s.meter
}

// Provider returns the MeterProvider configured for this scope.
func (s *Scope) Provider() metric.MeterProvider {
	if s == nil {
		return noop.NewMeterProvider()
	}
	return s.provider
}

// validateScope checks that s is non-nil and returns an error otherwise.
// Used as a manual guard by downstream packages that embed Scope in their
// Config (pointer fields cannot use validate:"required").
