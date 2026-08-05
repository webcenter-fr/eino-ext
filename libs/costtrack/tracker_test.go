package costtrack

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/webcenter-fr/eino-ext/callbacks/activity"
	"github.com/webcenter-fr/eino-ext/libs/modelsdev"
)

func testTracker(t *testing.T) (*Tracker, activity.Bus, *atomic.Pointer[modelsdev.Catalog]) {
	t.Helper()
	bus, err := activity.NewBus(activity.Config{})
	if err != nil {
		t.Fatalf("NewBus: %v", err)
	}
	t.Cleanup(func() { bus.Close() })

	holder := new(atomic.Pointer[modelsdev.Catalog])
	holder.Store(&modelsdev.Catalog{})

	tracker, err := NewTracker(context.Background(), &Config{
		Bus:             bus,
		PricingProvider: "anthropic",
		Resolve:         func(gw string) (string, string, bool) { return "anthropic", "claude-opus-4-5", true },
		CatalogHolder:   holder,
	})
	if err != nil {
		t.Fatalf("NewTracker: %v", err)
	}
	return tracker, bus, holder
}

func TestTracker_NewTracker_ValidatesConfig(t *testing.T) {
	_, err := NewTracker(context.Background(), &Config{
		PricingProvider: "",
	})
	if err == nil {
		t.Error("expected error for missing PricingProvider")
	}
}

func TestTracker_NewTracker_Defaults(t *testing.T) {
	bus, err := activity.NewBus(activity.Config{})
	if err != nil {
		t.Fatalf("NewBus: %v", err)
	}
	defer bus.Close()

	holder := new(atomic.Pointer[modelsdev.Catalog])
	holder.Store(&modelsdev.Catalog{})

	tracker, err := NewTracker(context.Background(), &Config{
		Bus:             bus,
		PricingProvider: "anthropic",
		Resolve:         func(gw string) (string, string, bool) { return "", "", false },
		CatalogHolder:   holder,
	})
	if err != nil {
		t.Fatalf("NewTracker: %v", err)
	}
	if tracker.ActivityHandler() == nil {
		t.Error("ActivityHandler is nil")
	}
	if tracker.MetricsRegistry() == nil {
		t.Error("MetricsRegistry is nil")
	}
	if tracker.PrometheusHandler() == nil {
		t.Error("PrometheusHandler is nil")
	}
}

func TestTracker_Snapshot_StepEnded(t *testing.T) {
	tracker, bus, _ := testTracker(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go tracker.Watch(ctx, "s1")

	time.Sleep(50 * time.Millisecond)

	bus.Publish(ctx, activity.Event{SessionID: "s1", Agent: "coder", Type: activity.TypeStepStarted, Data: activity.StepStarted{Model: "claude-opus-4-5"}})
	bus.Publish(ctx, activity.Event{SessionID: "s1", Agent: "coder", Type: activity.TypeStepEnded, Data: activity.StepEnded{
		Cost:   10.0,
		Tokens: activity.Tokens{Input: 1000000, Output: 500000, Cache: activity.CacheTokens{Read: 200000, Write: 100000}},
	}})

	deadline := time.Now().Add(2 * time.Second)
	var snap Snapshot
	for time.Now().Before(deadline) {
		snap = tracker.Snapshot("s1")
		if snap.Totals.Steps > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if snap.Totals.Steps != 1 {
		t.Fatalf("Totals.Steps = %d, want 1", snap.Totals.Steps)
	}
	if snap.Totals.Cost != 10.0 {
		t.Errorf("Totals.Cost = %v, want 10.0", snap.Totals.Cost)
	}
	if snap.Totals.Tokens.Input != 1000000 {
		t.Errorf("Totals.Tokens.Input = %d, want 1000000", snap.Totals.Tokens.Input)
	}
}

func TestTracker_Snapshot_Estimated(t *testing.T) {
	tracker, bus, _ := testTracker(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go tracker.Watch(ctx, "s1")

	time.Sleep(50 * time.Millisecond)

	bus.Publish(ctx, activity.Event{SessionID: "s1", Agent: "coder", Type: activity.TypeStepStarted, Data: activity.StepStarted{Model: "claude-opus-4-5"}})
	bus.Publish(ctx, activity.Event{SessionID: "s1", Agent: "coder", Type: activity.TypeStepEnded, Data: activity.StepEnded{
		Cost:      1.0,
		Tokens:    activity.Tokens{Input: 100, Output: 50},
		Estimated: true,
	}})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snap := tracker.Snapshot("s1")
		if snap.Estimated {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("Snapshot.Estimated never became true")
}

func TestTracker_Compaction(t *testing.T) {
	tracker, bus, _ := testTracker(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go tracker.Watch(ctx, "s1")

	time.Sleep(50 * time.Millisecond)

	bus.Publish(ctx, activity.Event{SessionID: "s1", Agent: "coder", Type: activity.TypeCompactionEnded, Data: activity.CompactionEnded{Text: "summary"}})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snap := tracker.Snapshot("s1")
		if snap.Compactions == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("Snapshot.Compactions never became 1")
}

func TestTracker_FakeRecorder(t *testing.T) {
	bus, err := activity.NewBus(activity.Config{})
	if err != nil {
		t.Fatalf("NewBus: %v", err)
	}
	defer bus.Close()

	holder := new(atomic.Pointer[modelsdev.Catalog])
	holder.Store(&modelsdev.Catalog{})

	fr := &fakeRecorder{}

	tracker, err := NewTracker(context.Background(), &Config{
		Bus:             bus,
		PricingProvider: "anthropic",
		Resolve:         func(gw string) (string, string, bool) { return "", "", false },
		CatalogHolder:   holder,
		Recorder:        fr,
	})
	if err != nil {
		t.Fatalf("NewTracker: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go tracker.Watch(ctx, "s1")

	time.Sleep(50 * time.Millisecond)

	bus.Publish(ctx, activity.Event{SessionID: "s1", Agent: "coder", Type: activity.TypeStepStarted, Data: activity.StepStarted{Model: "gpt-5"}})
	bus.Publish(ctx, activity.Event{SessionID: "s1", Agent: "coder", Type: activity.TypeStepEnded, Data: activity.StepEnded{
		Cost:   1.5,
		Tokens: activity.Tokens{Input: 100, Output: 50},
	}})
	bus.Publish(ctx, activity.Event{SessionID: "s1", Agent: "coder", Type: activity.TypeToolCalled, Data: activity.ToolCalled{Tool: "test"}})
	bus.Publish(ctx, activity.Event{SessionID: "s1", Type: activity.Type("answer.ended")})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		fr.mu.Lock()
		steps := fr.observeStepCalls
		tasks := fr.recordTaskCalls
		fr.mu.Unlock()
		if steps > 0 && tasks > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("fake recorder never received expected calls")
}

func TestTracker_HTTPEndpoint(t *testing.T) {
	tracker, _, _ := testTracker(t)
	mux := http.NewServeMux()
	tracker.Mount(mux)

	// /metrics should return something
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("/metrics status = %d, want 200", rec.Code)
	}

	// /cost/usage?session=unknown should return 404
	req = httptest.NewRequest(http.MethodGet, "/cost/usage?session=unknown", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("/cost/usage?session=unknown status = %d, want 404", rec.Code)
	}

	// /cost/usage (no session) should return 200
	req = httptest.NewRequest(http.MethodGet, "/cost/usage", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("/cost/usage status = %d, want 200", rec.Code)
	}
}

func TestTracker_ConcurrentStepEnded(t *testing.T) {
	tracker, bus, _ := testTracker(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go tracker.Watch(ctx, "s1")

	time.Sleep(50 * time.Millisecond)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			bus.Publish(ctx, activity.Event{SessionID: "s1", Agent: "coder", Type: activity.TypeStepEnded, Data: activity.StepEnded{
				Cost:   1.0,
				Tokens: activity.Tokens{Input: 100, Output: 50},
			}})
		}(i)
	}
	wg.Wait()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snap := tracker.Snapshot("s1")
		if snap.Totals.Steps == 10 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("expected 10 steps, got %d", tracker.Snapshot("s1").Totals.Steps)
}

func gatherMetric(t *testing.T, reg *prometheus.Registry, name string, labels map[string]string) float64 {
	t.Helper()
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.GetMetric() {
			if matchLabels(m, labels) {
				if c := m.GetCounter(); c != nil {
					return c.GetValue()
				}
				if g := m.GetGauge(); g != nil {
					return g.GetValue()
				}
			}
		}
	}
	return 0
}

func matchLabels(m *dto.Metric, labels map[string]string) bool {
	got := make(map[string]string, len(m.GetLabel()))
	for _, l := range m.GetLabel() {
		got[l.GetName()] = l.GetValue()
	}
	for k, v := range labels {
		if got[k] != v {
			return false
		}
	}
	return true
}

// fakeRecorder is an in-memory recorder for testing the Recorder seam.
type fakeRecorder struct {
	mu               sync.Mutex
	observeStepCalls int
	recordTaskCalls  int
}

func (fr *fakeRecorder) ObserveStep(model, agent string, se activity.StepEnded) {
	fr.mu.Lock()
	fr.observeStepCalls++
	fr.mu.Unlock()
}

func (fr *fakeRecorder) ObserveBreakdown(model, agent string, b modelsdev.CostBreakdown) {}

func (fr *fakeRecorder) RecordTask(sessionID, agent string, cost float64, real bool) {
	fr.mu.Lock()
	fr.recordTaskCalls++
	fr.mu.Unlock()
}

func (fr *fakeRecorder) RecordCompaction(agent string) {}

func (fr *fakeRecorder) RecordAnalysis(sessionID, agent string, a *activity.ComplexityAnalysis) {}

func (fr *fakeRecorder) RecordFallback(reason string) {}

func (fr *fakeRecorder) SetRealtimeCost(sessionID, agent string, cost float64) {}

func TestTracker_NoToolSession_NoHumanSavings(t *testing.T) {
	bus, err := activity.NewBus(activity.Config{})
	if err != nil {
		t.Fatalf("NewBus: %v", err)
	}
	t.Cleanup(func() { bus.Close() })

	holder := new(atomic.Pointer[modelsdev.Catalog])
	holder.Store(&modelsdev.Catalog{})

	reg := prometheus.NewRegistry()
	tracker, err := NewTracker(context.Background(), &Config{
		Bus:             bus,
		PricingProvider: "anthropic",
		Resolve:         func(gw string) (string, string, bool) { return "", "", false },
		CatalogHolder:   holder,
		Registry:        reg,
		Savings: activity.ComplexityAnalyzerConfig{
			HumanHourlyRate: 50.0,
			BaseTaskTime:    5 * time.Minute,
		},
	})
	if err != nil {
		t.Fatalf("NewTracker: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go tracker.Watch(ctx, "s1")
	time.Sleep(50 * time.Millisecond)

	// No-tool session: one LLM turn, then a terminal event (answer.ended)
	// triggers the facade's synthetic session.ended.
	bus.Publish(ctx, activity.Event{SessionID: "s1", Agent: "coder", Type: activity.TypeStepStarted, Data: activity.StepStarted{Model: "claude-opus-4-5"}})
	bus.Publish(ctx, activity.Event{SessionID: "s1", Agent: "coder", Type: activity.TypeStepEnded, Data: activity.StepEnded{Tokens: activity.Tokens{Input: 100, Output: 50}}})
	bus.Publish(ctx, activity.Event{SessionID: "s1", Agent: "coder", Type: activity.Type("answer.ended")})

	// human_savings_usd_total must stay 0; fail fast if it ever increments.
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if got := gatherMetric(t, reg, "human_savings_usd_total", map[string]string{"agent": "coder"}); got > 0 {
			t.Fatalf("human_savings_usd_total incremented for a no-tool session: %v", got)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := gatherMetric(t, reg, "cost_saver_runs_total", map[string]string{"agent": "coder"}); got != 0 {
		t.Errorf("cost_saver_runs_total = %v, want 0 for no-tool session", got)
	}
}

func TestTracker_ToolSession_RecordsHumanSavings(t *testing.T) {
	bus, err := activity.NewBus(activity.Config{})
	if err != nil {
		t.Fatalf("NewBus: %v", err)
	}
	t.Cleanup(func() { bus.Close() })

	holder := new(atomic.Pointer[modelsdev.Catalog])
	holder.Store(&modelsdev.Catalog{})

	reg := prometheus.NewRegistry()
	tracker, err := NewTracker(context.Background(), &Config{
		Bus:             bus,
		PricingProvider: "anthropic",
		Resolve:         func(gw string) (string, string, bool) { return "", "", false },
		CatalogHolder:   holder,
		Registry:        reg,
		Savings: activity.ComplexityAnalyzerConfig{
			HumanHourlyRate: 50.0,
			BaseTaskTime:    5 * time.Minute,
		},
	})
	if err != nil {
		t.Fatalf("NewTracker: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go tracker.Watch(ctx, "s1")
	time.Sleep(50 * time.Millisecond)

	// Real session: one tool call, 1000 tokens => fallback ratio 0.3,
	// moneySaved = 1.25.
	bus.Publish(ctx, activity.Event{SessionID: "s1", Agent: "coder", Type: activity.TypeStepStarted, Data: activity.StepStarted{Model: "claude-opus-4-5"}})
	bus.Publish(ctx, activity.Event{SessionID: "s1", Agent: "coder", Type: activity.TypeToolCalled, Data: activity.ToolCalled{Tool: "opensearch-health"}})
	bus.Publish(ctx, activity.Event{SessionID: "s1", Agent: "coder", Type: activity.TypeStepEnded, Data: activity.StepEnded{Tokens: activity.Tokens{Input: 500, Output: 500}}})
	bus.Publish(ctx, activity.Event{SessionID: "s1", Agent: "coder", Type: activity.Type("answer.ended")})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got := gatherMetric(t, reg, "human_savings_usd_total", map[string]string{"agent": "coder"}); got > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := gatherMetric(t, reg, "human_savings_usd_total", map[string]string{"agent": "coder"}); math.Abs(got-1.25) > 1e-9 {
		t.Errorf("human_savings_usd_total = %v, want 1.25 for tool session", got)
	}
	if got := gatherMetric(t, reg, "cost_saver_runs_total", map[string]string{"agent": "coder"}); got != 1 {
		t.Errorf("cost_saver_runs_total = %v, want 1 for tool session", got)
	}
}

func TestPrometheusRecorder_HumanSavings(t *testing.T) {
	bus, err := activity.NewBus(activity.Config{})
	if err != nil {
		t.Fatalf("NewBus: %v", err)
	}
	defer bus.Close()

	holder := new(atomic.Pointer[modelsdev.Catalog])
	holder.Store(&modelsdev.Catalog{})

	reg := prometheus.NewRegistry()
	cfg := &Config{
		Bus:             bus,
		PricingProvider: "anthropic",
		Resolve:         func(gw string) (string, string, bool) { return "", "", false },
		CatalogHolder:   holder,
		Registry:        reg,
	}
	pricer := modelsdev.CatalogPricer{
		Catalog: holder.Load(),
		Resolve: cfg.Resolve,
	}

	pr, err := newPrometheusRecorder(cfg, pricer)
	if err != nil {
		t.Fatalf("newPrometheusRecorder: %v", err)
	}

	pr.RecordAnalysis("s1", "coder", &activity.ComplexityAnalysis{
		ComplexityRatio:       0.5,
		HumanTimeSavedSeconds: 300,
		MoneySavedUSD:         5.0,
	})

	got := gatherMetric(t, reg, "human_savings_usd_total", map[string]string{"agent": "coder"})
	if got != 5.0 {
		t.Errorf("human_savings_usd_total = %v, want 5.0", got)
	}

	// nil analysis should no-op
	pr.RecordAnalysis("s1", "coder", nil)
}
