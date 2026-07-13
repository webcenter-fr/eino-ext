package otel

import (
	"context"
	"testing"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	"github.com/webcenter-fr/eino-ext/libs/otelmetrics"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/checkup"
)

func TestCheck_OK(t *testing.T) {
	rdr := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(rdr))
	defer mp.Shutdown(context.Background())

	scope, err := otelmetrics.NewScope(context.Background(), &otelmetrics.Config{
		MeterProvider: mp,
	})
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}

	rec, err := NewOTelRecorder(context.Background(), &Config{Scope: scope})
	if err != nil {
		t.Fatalf("NewOTelRecorder: %v", err)
	}

	results := Check(context.Background(), rec)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status != checkup.StatusOK {
		t.Errorf("expected status ok, got %q with message: %s", results[0].Status, results[0].Message)
	}
}

func TestCheck_Nil(t *testing.T) {
	results := Check(context.Background(), nil)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status != checkup.StatusError {
		t.Errorf("expected status error, got %q", results[0].Status)
	}
}
