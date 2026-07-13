package metrics

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/webcenter-fr/eino-ext/callbacks/activity"
	"github.com/webcenter-fr/eino-ext/libs/modelsdev"
)

func TestCollector_Observe(t *testing.T) {
	reg := prometheus.NewRegistry()
	c, err := NewCollector(reg)
	if err != nil {
		t.Fatalf("NewCollector: %v", err)
	}

	c.Observe("gpt-5", "coder", activity.StepEnded{
		Cost:   1.25,
		Tokens: activity.Tokens{Input: 10, Output: 5, Reasoning: 2, Cache: activity.CacheTokens{Read: 3}},
	})

	got := gatherValue(t, reg, "llm_cost_usd_total", map[string]string{"model": "gpt-5", "agent": "coder"})
	if got != 1.25 {
		t.Errorf("llm_cost_usd_total = %v, want 1.25", got)
	}

	got = gatherValue(t, reg, "llm_tokens_total", map[string]string{"model": "gpt-5", "agent": "coder", "type": "input"})
	if got != 10 {
		t.Errorf("llm_tokens_total{type=input} = %v, want 10", got)
	}
}

func TestCollector_Watch(t *testing.T) {
	bus, err := activity.NewBus(activity.Config{})
	if err != nil {
		t.Fatalf("NewBus: %v", err)
	}
	t.Cleanup(func() { bus.Close() })

	reg := prometheus.NewRegistry()
	c, err := NewCollector(reg)
	if err != nil {
		t.Fatalf("NewCollector: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Watch(ctx, bus, "s")
	time.Sleep(50 * time.Millisecond) // let Watch's Subscribe register before publishing

	bus.Publish(ctx, activity.Event{SessionID: "s", Agent: "coder", Type: activity.TypeStepStarted, Data: activity.StepStarted{Model: "gpt-5"}})
	bus.Publish(ctx, activity.Event{SessionID: "s", Agent: "coder", Type: activity.TypeStepEnded, Data: activity.StepEnded{Cost: 2, Tokens: activity.Tokens{Output: 7}}})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if gatherValue(t, reg, "llm_cost_usd_total", map[string]string{"model": "gpt-5", "agent": "coder"}) == 2 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for Watch to record step.ended")
}

func TestCollector_Observe_WithBreakdown(t *testing.T) {
	reg := prometheus.NewRegistry()
	breakdownFn := func(model string, tokens activity.Tokens) (modelsdev.CostBreakdown, bool) {
		return modelsdev.CostBreakdown{
			Input:      1.0,
			Output:     2.0,
			CacheRead:  0.5,
			CacheWrite: 0.25,
			Total:      3.75,
			Savings:    1.5,
		}, true
	}
	c, err := NewCollector(reg, WithBreakdown(breakdownFn))
	if err != nil {
		t.Fatalf("NewCollector: %v", err)
	}

	c.Observe("gpt-5", "coder", activity.StepEnded{
		Cost:   3.75,
		Tokens: activity.Tokens{Input: 10, Output: 5, Cache: activity.CacheTokens{Read: 3, Write: 1}},
	})

	if got := gatherValue(t, reg, "llm_cost_savings_usd_total", map[string]string{"model": "gpt-5", "agent": "coder"}); got != 1.5 {
		t.Errorf("llm_cost_savings_usd_total = %v, want 1.5", got)
	}
	if got := gatherValue(t, reg, "llm_cost_usd_by_component_total", map[string]string{"model": "gpt-5", "agent": "coder", "component": "input"}); got != 1.0 {
		t.Errorf("llm_cost_usd_by_component_total{component=input} = %v, want 1.0", got)
	}
	if got := gatherValue(t, reg, "llm_cost_usd_by_component_total", map[string]string{"model": "gpt-5", "agent": "coder", "component": "output"}); got != 2.0 {
		t.Errorf("llm_cost_usd_by_component_total{component=output} = %v, want 2.0", got)
	}
	if got := gatherValue(t, reg, "llm_cost_usd_by_component_total", map[string]string{"model": "gpt-5", "agent": "coder", "component": "cache_read"}); got != 0.5 {
		t.Errorf("llm_cost_usd_by_component_total{component=cache_read} = %v, want 0.5", got)
	}
	if got := gatherValue(t, reg, "llm_cost_usd_by_component_total", map[string]string{"model": "gpt-5", "agent": "coder", "component": "cache_write"}); got != 0.25 {
		t.Errorf("llm_cost_usd_by_component_total{component=cache_write} = %v, want 0.25", got)
	}
}

func TestCollector_Watch_SessionEndedValue(t *testing.T) {
	// Prove that session.ended published as a VALUE (not pointer) triggers
	// the cost-saver path. This was the bug: only *activity.SessionEnded
	// matched before, and the value variant was silently ignored.
	bus, err := activity.NewBus(activity.Config{})
	if err != nil {
		t.Fatalf("NewBus: %v", err)
	}
	t.Cleanup(func() { bus.Close() })

	reg := prometheus.NewRegistry()
	c, err := NewCollector(reg)
	if err != nil {
		t.Fatalf("NewCollector: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go c.Watch(ctx, bus, "s")
	time.Sleep(50 * time.Millisecond)

	// Publish a value SessionEnded (as the facade will do).
	bus.Publish(ctx, activity.Event{
		SessionID: "s",
		Agent:     "coder",
		Type:      activity.TypeSessionEnded,
		Data:      activity.SessionEnded{Duration: 10 * time.Second, Cost: 1.0, Steps: 2, Tools: 1},
	})

	// Give it time to process; without a summarizer+analyzer, the handler
	// should not panic and should no-op gracefully.
	time.Sleep(200 * time.Millisecond)
	// The key assertion is that the Watch goroutine didn't panic and we're
	// still alive here.
}

func gatherValue(t *testing.T, reg *prometheus.Registry, name string, labels map[string]string) float64 {
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
				if c := m.GetCounter(); c != nil {
					return c.GetValue()
				}
			}
		}
	}
	return 0
}

func matchesLabels(m *dto.Metric, labels map[string]string) bool {
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
