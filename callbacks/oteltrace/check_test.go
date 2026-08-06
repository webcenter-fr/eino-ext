package oteltrace

import (
	"context"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/checkup"
)

func TestCheck_OK(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exp),
	)
	defer func() { _ = tp.Shutdown(context.Background()) }()

	h, err := NewHandler(context.Background(), &Config{
		TracerProvider: tp,
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	results := Check(context.Background(), h)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status != checkup.StatusOK {
		t.Errorf("expected status ok, got %q with message: %s", results[0].Status, results[0].Message)
	}
}

func TestCheck_Noop(t *testing.T) {
	h, err := NewHandler(context.Background(), &Config{
		TracerProvider: noop.NewTracerProvider(),
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	results := Check(context.Background(), h)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status != checkup.StatusLimited {
		t.Errorf("expected status limited, got %q with message: %s", results[0].Status, results[0].Message)
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
