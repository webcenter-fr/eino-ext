package otelmetrics

import (
	"context"
	"fmt"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// FloatCounter is a labeled float64 counter (USD-style metrics).
// nil-receiver safe.
type FloatCounter struct {
	counter metric.Float64Counter
}

// FloatCounter creates and returns a labeled float64 counter. The caller caches
// the returned instrument; nil Scope is safe (returns no-op).
func (s *Scope) FloatCounter(name, desc, unit string) (*FloatCounter, error) {
	if s == nil {
		return &FloatCounter{counter: nil}, nil
	}
	c, err := s.meter.Float64Counter(name,
		metric.WithDescription(desc),
		metric.WithUnit(unit),
	)
	if err != nil {
		return nil, fmt.Errorf("otelmetrics: failed to create Float64Counter %s: %w", name, err)
	}
	return &FloatCounter{counter: c}, nil
}

// Add increments the counter by value for the given attribute set.
func (fc *FloatCounter) Add(ctx context.Context, value float64, attrs attribute.Set) {
	if fc == nil || fc.counter == nil {
		return
	}
	fc.counter.Add(ctx, value, metric.WithAttributeSet(attrs))
}

// IntCounter is a labeled int64 counter (token/task counts).
// nil-receiver safe.
type IntCounter struct {
	counter metric.Int64Counter
}

// IntCounter creates and returns a labeled int64 counter.
func (s *Scope) IntCounter(name, desc, unit string) (*IntCounter, error) {
	if s == nil {
		return &IntCounter{counter: nil}, nil
	}
	c, err := s.meter.Int64Counter(name,
		metric.WithDescription(desc),
		metric.WithUnit(unit),
	)
	if err != nil {
		return nil, fmt.Errorf("otelmetrics: failed to create Int64Counter %s: %w", name, err)
	}
	return &IntCounter{counter: c}, nil
}

// Add increments the counter by value for the given attribute set.
func (ic *IntCounter) Add(ctx context.Context, value int64, attrs attribute.Set) {
	if ic == nil || ic.counter == nil {
		return
	}
	ic.counter.Add(ctx, value, metric.WithAttributeSet(attrs))
}

// Histogram is a labeled float64 histogram (cost distributions).
// nil-receiver safe.
type Histogram struct {
	histogram metric.Float64Histogram
}

// Histogram creates and returns a labeled float64 histogram.
func (s *Scope) Histogram(name, desc, unit string, buckets []float64) (*Histogram, error) {
	if s == nil {
		return &Histogram{histogram: nil}, nil
	}
	opts := []metric.Float64HistogramOption{
		metric.WithDescription(desc),
		metric.WithUnit(unit),
	}
	if len(buckets) > 0 {
		opts = append(opts, metric.WithExplicitBucketBoundaries(buckets...))
	}
	h, err := s.meter.Float64Histogram(name, opts...)
	if err != nil {
		return nil, fmt.Errorf("otelmetrics: failed to create Float64Histogram %s: %w", name, err)
	}
	return &Histogram{histogram: h}, nil
}

// Record records a value on the histogram for the given attribute set.
func (h *Histogram) Record(ctx context.Context, value float64, attrs attribute.Set) {
	if h == nil || h.histogram == nil {
		return
	}
	h.histogram.Record(ctx, value, metric.WithAttributeSet(attrs))
}

// Gauge is a labeled observable gauge backed by a thread-safe value store.
// Set (name-keyed by attribute set) updates the latest value; the observable
// gauge callback reads it on collect. Reusable for realtime cost + cost-saver
// snapshot metrics.
// nil-receiver safe.
type Gauge struct {
	mu        sync.RWMutex
	floatVals map[attribute.Set]float64
	intVals   map[attribute.Set]int64
}

// Gauge creates and returns a Gauge. It registers an observable gauge on the
// meter that reads the current values on collect.
func (s *Scope) Gauge(name, desc, unit string) (*Gauge, error) {
	if s == nil {
		return &Gauge{}, nil
	}
	g := &Gauge{
		floatVals: make(map[attribute.Set]float64),
		intVals:   make(map[attribute.Set]int64),
	}
	_, err := s.meter.Float64ObservableGauge(name,
		metric.WithDescription(desc),
		metric.WithUnit(unit),
		metric.WithFloat64Callback(func(_ context.Context, o metric.Float64Observer) error {
			g.mu.RLock()
			defer g.mu.RUnlock()
			for attrs, v := range g.floatVals {
				o.Observe(v, metric.WithAttributeSet(attrs))
			}
			return nil
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("otelmetrics: failed to create observable gauge %s: %w", name, err)
	}
	return g, nil
}

// Set records the float value for the given attribute set.
func (g *Gauge) Set(value float64, attrs attribute.Set) {
	if g == nil {
		return
	}
	g.mu.Lock()
	if g.floatVals == nil {
		g.floatVals = make(map[attribute.Set]float64)
	}
	g.floatVals[attrs] = value
	g.mu.Unlock()
}

// FloatValue returns the current float value for the given attribute set.
func (g *Gauge) FloatValue(attrs attribute.Set) (float64, bool) {
	if g == nil {
		return 0, false
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.floatVals == nil {
		return 0, false
	}
	v, ok := g.floatVals[attrs]
	return v, ok
}
