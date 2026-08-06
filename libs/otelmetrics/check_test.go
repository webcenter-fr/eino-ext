package otelmetrics

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/checkup"
)

func TestCheck_NoopProvider(t *testing.T) {
	s, err := NewScope(context.Background(), &Config{
		MeterProvider: noop.NewMeterProvider(),
	})
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}

	results := Check(context.Background(), s)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status != checkup.StatusLimited {
		t.Errorf("expected status limited for noop provider, got %q", results[0].Status)
	}
}

func TestCheck_NilScope(t *testing.T) {
	results := Check(context.Background(), nil)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status != checkup.StatusError {
		t.Errorf("expected status error for nil scope, got %q", results[0].Status)
	}
}

func TestCheck_OK(t *testing.T) {
	rdr := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(rdr))
	defer func() { _ = mp.Shutdown(context.Background()) }()

	s, err := NewScope(context.Background(), &Config{
		MeterProvider: mp,
	})
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}

	results := Check(context.Background(), s)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status != checkup.StatusOK {
		t.Errorf("expected status ok, got %q with message: %s", results[0].Status, results[0].Message)
	}
}
