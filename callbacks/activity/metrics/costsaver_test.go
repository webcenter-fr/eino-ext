package metrics

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"

	"github.com/webcenter-fr/eino-ext/callbacks/activity"
)

func TestCostSaverCollector_RecordAnalysis_BumpsRuns(t *testing.T) {
	reg := prometheus.NewRegistry()
	csc, err := NewCostSaverCollector(reg)
	if err != nil {
		t.Fatalf("NewCostSaverCollector: %v", err)
	}

	csc.RecordAnalysis("s1", "coder", &activity.ComplexityAnalysis{
		ComplexityRatio:       0.5,
		HumanTimeSavedSeconds: 300,
		MoneySavedUSD:         5.0,
	})

	if got := gatherValue(t, reg, "cost_saver_runs_total", map[string]string{"agent": "coder"}); got != 1 {
		t.Errorf("cost_saver_runs_total = %v, want 1", got)
	}
}

func TestCostSaverCollector_NilSafe(t *testing.T) {
	var csc *CostSaverCollector
	csc.RecordAnalysis("s1", "coder", &activity.ComplexityAnalysis{})
	csc.RecordFallback("test")
}

func TestCollector_CostSaver_SkipsNoToolSession(t *testing.T) {
	bus, err := activity.NewBus(activity.Config{})
	if err != nil {
		t.Fatalf("NewBus: %v", err)
	}
	t.Cleanup(func() { _ = bus.Close() })

	reg := prometheus.NewRegistry()
	c, err := NewCollector(reg, WithCostSaver(CostSaverConfig{
		Enabled: true,
		AnalyzerConfig: &activity.ComplexityAnalyzerConfig{
			HumanHourlyRate: 50.0,
			BaseTaskTime:    5 * time.Minute,
			// Model nil => CompositeComplexityAnalyzer uses fallback formula.
		},
	}, bus))
	if err != nil {
		t.Fatalf("NewCollector: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go c.Watch(ctx, bus, "s")
	time.Sleep(50 * time.Millisecond) // let Subscribe register

	// No-tool session: one LLM turn, no tools, then session.ended.
	bus.Publish(ctx, activity.Event{SessionID: "s", Agent: "coder", Type: activity.TypeStepStarted, Data: activity.StepStarted{Model: "gpt-5"}})
	bus.Publish(ctx, activity.Event{SessionID: "s", Agent: "coder", Type: activity.TypeStepEnded, Data: activity.StepEnded{Tokens: activity.Tokens{Input: 100, Output: 50}}})
	bus.Publish(ctx, activity.Event{SessionID: "s", Agent: "coder", Type: activity.TypeSessionEnded, Data: activity.SessionEnded{Duration: 2 * time.Second, Steps: 1, Tools: 0}})

	// If the guard were broken, handleSessionEnded would run within tens of ms.
	// Poll 500ms and fail fast if cost_saver_runs_total ever increments.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if got := gatherValue(t, reg, "cost_saver_runs_total", map[string]string{"agent": "coder"}); got > 0 {
			t.Fatalf("cost saver ran for a no-tool session: cost_saver_runs_total = %v", got)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := gatherValue(t, reg, "cost_saver_runs_total", map[string]string{"agent": "coder"}); got != 0 {
		t.Errorf("cost_saver_runs_total = %v, want 0 for no-tool session", got)
	}
}

func TestCollector_CostSaver_RunsForToolSession(t *testing.T) {
	bus, err := activity.NewBus(activity.Config{})
	if err != nil {
		t.Fatalf("NewBus: %v", err)
	}
	t.Cleanup(func() { _ = bus.Close() })

	reg := prometheus.NewRegistry()
	c, err := NewCollector(reg, WithCostSaver(CostSaverConfig{
		Enabled: true,
		AnalyzerConfig: &activity.ComplexityAnalyzerConfig{
			HumanHourlyRate: 50.0,
			BaseTaskTime:    5 * time.Minute,
		},
	}, bus))
	if err != nil {
		t.Fatalf("NewCollector: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go c.Watch(ctx, bus, "s")
	time.Sleep(50 * time.Millisecond)

	// Real session: one tool call, one step (1000 tokens => ratio 0.3 exactly).
	bus.Publish(ctx, activity.Event{SessionID: "s", Agent: "coder", Type: activity.TypeStepStarted, Data: activity.StepStarted{Model: "gpt-5"}})
	bus.Publish(ctx, activity.Event{SessionID: "s", Agent: "coder", Type: activity.TypeToolCalled, Data: activity.ToolCalled{Tool: "opensearch-health"}})
	bus.Publish(ctx, activity.Event{SessionID: "s", Agent: "coder", Type: activity.TypeStepEnded, Data: activity.StepEnded{Tokens: activity.Tokens{Input: 500, Output: 500}}})
	bus.Publish(ctx, activity.Event{SessionID: "s", Agent: "coder", Type: activity.TypeSessionEnded, Data: activity.SessionEnded{Duration: 3 * time.Second, Steps: 1, Tools: 1}})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got := gatherValue(t, reg, "cost_saver_runs_total", map[string]string{"agent": "coder"}); got == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := gatherValue(t, reg, "cost_saver_runs_total", map[string]string{"agent": "coder"}); got != 1 {
		t.Fatalf("cost_saver_runs_total = %v, want 1 for tool session", got)
	}
	// Fallback ratio for 1 tool, 1 step, 1000 tokens = 0.1 + 0.2 + 0 = 0.3
	// => humanTimeSaved = 0.3 * 300s = 90s; moneySaved = 0.3*300*50/3600 = 1.25.
	assert.InDelta(t, 0.3, gatherGaugeValue(t, reg, "cost_saver_complexity_ratio", map[string]string{"session_id": "s", "agent": "coder"}), 1e-9)
	assert.InDelta(t, 90, gatherGaugeValue(t, reg, "cost_saver_human_time_saved_seconds", map[string]string{"session_id": "s", "agent": "coder"}), 1e-9)
	assert.InDelta(t, 1.25, gatherGaugeValue(t, reg, "cost_saver_money_saved_usd", map[string]string{"session_id": "s", "agent": "coder"}), 1e-9)
}

func gatherGaugeValue(t *testing.T, reg *prometheus.Registry, name string, labels map[string]string) float64 {
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
			if matchesLabels(m, labels) {
				if g := m.GetGauge(); g != nil {
					return g.GetValue()
				}
			}
		}
	}
	return 0
}
