package otelmetrics

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestNewScope_Defaults(t *testing.T) {
	// Temporarily swap the global provider getter to avoid affecting other tests.
	orig := globalMeterProvider
	globalMeterProvider = func() metric.MeterProvider { return noop.NewMeterProvider() }
	defer func() { globalMeterProvider = orig }()

	s, err := NewScope(context.Background(), nil)
	if err != nil {
		t.Fatalf("NewScope(nil): %v", err)
	}
	if s == nil {
		t.Fatal("expected non-nil Scope")
	}
}

func TestNewScope_InjectedProvider(t *testing.T) {
	mp := noop.NewMeterProvider()
	s, err := NewScope(context.Background(), &Config{
		MeterProvider: mp,
	})
	if err != nil {
		t.Fatalf("NewScope with injected provider: %v", err)
	}
	if s == nil {
		t.Fatal("expected non-nil Scope")
	}
}

func TestNewScope_CustomMeterName(t *testing.T) {
	mp := noop.NewMeterProvider()
	s, err := NewScope(context.Background(), &Config{
		MeterProvider: mp,
		MeterName:     "custom-meter",
	})
	if err != nil {
		t.Fatalf("NewScope with custom meter name: %v", err)
	}
	if s == nil {
		t.Fatal("expected non-nil Scope")
	}
}

func TestCounter_Add(t *testing.T) {
	rdr := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(rdr))
	defer mp.Shutdown(context.Background())

	s, err := NewScope(context.Background(), &Config{
		MeterProvider: mp,
		MeterName:     "test-counter",
	})
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}

	fc, err := s.FloatCounter("test.float.counter", "test float counter", "1")
	if err != nil {
		t.Fatalf("FloatCounter: %v", err)
	}
	ic, err := s.IntCounter("test.int.counter", "test int counter", "{token}")
	if err != nil {
		t.Fatalf("IntCounter: %v", err)
	}

	attrs := Attrs("agent", "test-agent", "model", "gpt-4")
	fc.Add(context.Background(), 3.14, attrs)
	ic.Add(context.Background(), 42, attrs)

	rm := &metricdata.ResourceMetrics{}
	if err := rdr.Collect(context.Background(), rm); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	foundFloat := false
	foundInt := false
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			switch d := m.Data.(type) {
			case metricdata.Sum[float64]:
				if m.Name == "test.float.counter" {
					foundFloat = true
					if len(d.DataPoints) != 1 {
						t.Errorf("expected 1 datapoint for float counter, got %d", len(d.DataPoints))
					}
					if d.DataPoints[0].Value != 3.14 {
						t.Errorf("expected float value 3.14, got %v", d.DataPoints[0].Value)
					}
				}
			case metricdata.Sum[int64]:
				if m.Name == "test.int.counter" {
					foundInt = true
					if len(d.DataPoints) != 1 {
						t.Errorf("expected 1 datapoint for int counter, got %d", len(d.DataPoints))
					}
					if d.DataPoints[0].Value != 42 {
						t.Errorf("expected int value 42, got %v", d.DataPoints[0].Value)
					}
				}
			}
		}
	}
	if !foundFloat {
		t.Error("float counter not found in collected metrics")
	}
	if !foundInt {
		t.Error("int counter not found in collected metrics")
	}
}

func TestHistogram_Record(t *testing.T) {
	rdr := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(rdr))
	defer mp.Shutdown(context.Background())

	s, err := NewScope(context.Background(), &Config{
		MeterProvider: mp,
		MeterName:     "test-histogram",
	})
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}

	h, err := s.Histogram("test.histogram", "test histogram", "USD",
		[]float64{0.1, 1.0, 10.0, 100.0})
	if err != nil {
		t.Fatalf("Histogram: %v", err)
	}

	h.Record(context.Background(), 5.5, Attrs())
	h.Record(context.Background(), 15.0, Attrs())

	rm := &metricdata.ResourceMetrics{}
	if err := rdr.Collect(context.Background(), rm); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	found := false
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == "test.histogram" {
				found = true
				d, ok := m.Data.(metricdata.Histogram[float64])
				if !ok {
					t.Errorf("expected Histogram[float64], got %T", m.Data)
					continue
				}
				if len(d.DataPoints) != 1 {
					t.Errorf("expected 1 datapoint, got %d", len(d.DataPoints))
				}
				if d.DataPoints[0].Count != 2 {
					t.Errorf("expected count=2, got %d", d.DataPoints[0].Count)
				}
			}
		}
	}
	if !found {
		t.Error("histogram not found in collected metrics")
	}
}

func TestGauge_Set(t *testing.T) {
	rdr := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(rdr))
	defer mp.Shutdown(context.Background())

	s, err := NewScope(context.Background(), &Config{
		MeterProvider: mp,
		MeterName:     "test-gauge",
	})
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}

	g, err := s.Gauge("test.gauge", "test gauge", "USD")
	if err != nil {
		t.Fatalf("Gauge: %v", err)
	}

	attrs := Attrs("session_id", "sess-1", "agent", "test")
	g.Set(123.45, attrs)

	rm := &metricdata.ResourceMetrics{}
	if err := rdr.Collect(context.Background(), rm); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	found := false
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == "test.gauge" {
				found = true
				d, ok := m.Data.(metricdata.Gauge[float64])
				if !ok {
					t.Errorf("expected Gauge[float64], got %T", m.Data)
					continue
				}
				if len(d.DataPoints) != 1 {
					t.Errorf("expected 1 datapoint, got %d", len(d.DataPoints))
				}
				if d.DataPoints[0].Value != 123.45 {
					t.Errorf("expected value 123.45, got %v", d.DataPoints[0].Value)
				}
			}
		}
	}
	if !found {
		t.Error("gauge not found in collected metrics")
	}
}

func TestNilScope_Safe(t *testing.T) {
	var s *Scope = nil

	fc, err := s.FloatCounter("x", "x", "x")
	if err != nil || fc == nil {
		t.Fatal("nil Scope FloatCounter should return a non-nil, no-op counter")
	}
	fc.Add(context.Background(), 1.0, Attrs())

	ic, err := s.IntCounter("x", "x", "x")
	if err != nil || ic == nil {
		t.Fatal("nil Scope IntCounter should return a non-nil, no-op counter")
	}
	ic.Add(context.Background(), 1, Attrs())

	h, err := s.Histogram("x", "x", "x", nil)
	if err != nil || h == nil {
		t.Fatal("nil Scope Histogram should return a non-nil, no-op histogram")
	}
	h.Record(context.Background(), 1.0, Attrs())

	g, err := s.Gauge("x", "x", "x")
	if err != nil || g == nil {
		t.Fatal("nil Scope Gauge should return a non-nil, no-op gauge")
	}
	g.Set(1.0, Attrs())

	if s.Meter() == nil {
		t.Fatal("nil Scope Meter() should return a non-nil noop meter")
	}
}

func TestNilReceiver_Safe(t *testing.T) {
	var fc *FloatCounter
	fc.Add(context.Background(), 1.0, Attrs())

	var ic *IntCounter
	ic.Add(context.Background(), 1, Attrs())

	var h *Histogram
	h.Record(context.Background(), 1.0, Attrs())

	var g *Gauge
	g.Set(1.0, Attrs())
	if v, ok := g.FloatValue(Attrs()); ok || v != 0 {
		t.Fatal("nil Gauge.FloatValue should return 0, false")
	}
}

func TestAttrs(t *testing.T) {
	tests := []struct {
		name string
		kv   []string
		want int
	}{
		{"even pairs", []string{"k1", "v1", "k2", "v2"}, 2},
		{"odd pairs truncated", []string{"k1", "v1", "k2"}, 1},
		{"empty", nil, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := Attrs(tt.kv...)
			if s.Len() != tt.want {
				t.Errorf("expected %d attributes, got %d", tt.want, s.Len())
			}
		})
	}
}
