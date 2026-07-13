package otel

import (
	"context"
	"testing"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/webcenter-fr/eino-ext/callbacks/activity"
	"github.com/webcenter-fr/eino-ext/libs/modelsdev"
	"github.com/webcenter-fr/eino-ext/libs/otelmetrics"
)

func setupRecorder(t *testing.T) (*OTelRecorder, *sdkmetric.ManualReader) {
	t.Helper()
	rdr := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(rdr))
	t.Cleanup(func() { mp.Shutdown(context.Background()) })

	scope, err := otelmetrics.NewScope(context.Background(), &otelmetrics.Config{
		MeterProvider: mp,
		MeterName:     "test-recorder",
	})
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}

	rec, err := NewOTelRecorder(context.Background(), &Config{Scope: scope})
	if err != nil {
		t.Fatalf("NewOTelRecorder: %v", err)
	}
	return rec, rdr
}

func collectMetrics(t *testing.T, rdr *sdkmetric.ManualReader) *metricdata.ResourceMetrics {
	t.Helper()
	rm := &metricdata.ResourceMetrics{}
	if err := rdr.Collect(context.Background(), rm); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	return rm
}

func findMetric(rm *metricdata.ResourceMetrics, name string) metricdata.Metrics {
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == name {
				return m
			}
		}
	}
	return metricdata.Metrics{}
}

func TestObserveStep(t *testing.T) {
	rec, rdr := setupRecorder(t)

	se := activity.StepEnded{
		Cost: 0.05,
		Tokens: activity.Tokens{
			Input:     100,
			Output:    50,
			Reasoning: 25,
			Cache:     activity.CacheTokens{Read: 10},
		},
	}
	rec.ObserveStep("gpt-4", "test-agent", se)

	rm := collectMetrics(t, rdr)

	m := findMetric(rm, "llm.tokens")
	if m.Name != "llm.tokens" {
		t.Fatal("llm.tokens metric not found")
	}
	d, ok := m.Data.(metricdata.Sum[int64])
	if !ok {
		t.Fatalf("expected Sum[int64], got %T", m.Data)
	}
	if len(d.DataPoints) != 4 {
		t.Errorf("expected 4 token datapoints (input/output/reasoning/cache_read), got %d", len(d.DataPoints))
	}
	totalTokens := int64(0)
	for _, dp := range d.DataPoints {
		totalTokens += dp.Value
	}
	if totalTokens != int64(185) {
		t.Errorf("expected 185 total tokens, got %d", totalTokens)
	}

	m2 := findMetric(rm, "llm.cost.usd")
	if m2.Name != "llm.cost.usd" {
		t.Fatal("llm.cost.usd metric not found")
	}
	d2, ok := m2.Data.(metricdata.Sum[float64])
	if !ok {
		t.Fatalf("expected Sum[float64], got %T", m2.Data)
	}
	if len(d2.DataPoints) != 1 {
		t.Errorf("expected 1 cost datapoint, got %d", len(d2.DataPoints))
	}
	if d2.DataPoints[0].Value != 0.05 {
		t.Errorf("expected cost 0.05, got %v", d2.DataPoints[0].Value)
	}
}

func TestObserveBreakdown(t *testing.T) {
	rec, rdr := setupRecorder(t)

	b := modelsdev.CostBreakdown{
		Input:      0.01,
		Output:     0.02,
		CacheRead:  0.005,
		CacheWrite: 0.001,
		Savings:    0.003,
	}
	rec.ObserveBreakdown("gpt-4", "agent", b)

	rm := collectMetrics(t, rdr)

	m := findMetric(rm, "llm.cost.by_component.usd")
	if m.Name != "llm.cost.by_component.usd" {
		t.Fatal("llm.cost.by_component.usd not found")
	}
	d, ok := m.Data.(metricdata.Sum[float64])
	if !ok {
		t.Fatalf("expected Sum[float64], got %T", m.Data)
	}
	if len(d.DataPoints) != 4 {
		t.Errorf("expected 4 component datapoints, got %d", len(d.DataPoints))
	}

	m2 := findMetric(rm, "llm.cost.savings.usd")
	if m2.Name != "llm.cost.savings.usd" {
		t.Fatal("llm.cost.savings.usd not found")
	}
	d2, ok := m2.Data.(metricdata.Sum[float64])
	if !ok {
		t.Fatalf("expected Sum[float64], got %T", m2.Data)
	}
	if len(d2.DataPoints) != 1 {
		t.Errorf("expected 1 savings datapoint, got %d", len(d2.DataPoints))
	}
	if d2.DataPoints[0].Value != 0.003 {
		t.Errorf("expected savings 0.003, got %v", d2.DataPoints[0].Value)
	}
}

func TestRecordTask(t *testing.T) {
	rec, rdr := setupRecorder(t)

	rec.RecordTask("sess-1", "agent", 1.5, true)
	rec.RecordTask("sess-2", "agent", 2.0, false)

	rm := collectMetrics(t, rdr)

	m := findMetric(rm, "agent.tasks")
	if m.Name != "agent.tasks" {
		t.Fatal("agent.tasks not found")
	}
	d, ok := m.Data.(metricdata.Sum[int64])
	if !ok {
		t.Fatalf("expected Sum[int64], got %T", m.Data)
	}
	if len(d.DataPoints) != 2 {
		t.Errorf("expected 2 task datapoints, got %d", len(d.DataPoints))
	}
	totalTasks := int64(0)
	for _, dp := range d.DataPoints {
		totalTasks += dp.Value
	}
	if totalTasks != 2 {
		t.Errorf("expected 2 total tasks, got %d", totalTasks)
	}

	m2 := findMetric(rm, "agent.task.cost.usd")
	if m2.Name != "agent.task.cost.usd" {
		t.Fatal("agent.task.cost.usd not found")
	}
	hd, ok := m2.Data.(metricdata.Histogram[float64])
	if !ok {
		t.Fatalf("expected Histogram[float64], got %T", m2.Data)
	}
	if len(hd.DataPoints) != 1 {
		t.Errorf("expected 1 histogram datapoint, got %d", len(hd.DataPoints))
	}
	if hd.DataPoints[0].Count != 2 {
		t.Errorf("expected histogram count=2, got %d", hd.DataPoints[0].Count)
	}
	if hd.DataPoints[0].Sum != 3.5 {
		t.Errorf("expected histogram sum=3.5, got %v", hd.DataPoints[0].Sum)
	}
}

func TestRecordCompaction(t *testing.T) {
	rec, rdr := setupRecorder(t)

	rec.RecordCompaction("agent-a")
	rec.RecordCompaction("agent-a")
	rec.RecordCompaction("agent-b")

	rm := collectMetrics(t, rdr)

	m := findMetric(rm, "llm.compactions")
	if m.Name != "llm.compactions" {
		t.Fatal("llm.compactions not found")
	}
	d, ok := m.Data.(metricdata.Sum[int64])
	if !ok {
		t.Fatalf("expected Sum[int64], got %T", m.Data)
	}
	totalCompactions := int64(0)
	for _, dp := range d.DataPoints {
		totalCompactions += dp.Value
	}
	if totalCompactions != 3 {
		t.Errorf("expected 3 total compactions, got %d", totalCompactions)
	}
}

func TestRecordFallback(t *testing.T) {
	rec, rdr := setupRecorder(t)

	rec.RecordFallback("analysis_error")
	rec.RecordFallback("analysis_error")

	rm := collectMetrics(t, rdr)

	m := findMetric(rm, "cost_saver.fallback.count")
	if m.Name != "cost_saver.fallback.count" {
		t.Fatal("cost_saver.fallback.count not found")
	}
	d, ok := m.Data.(metricdata.Sum[int64])
	if !ok {
		t.Fatalf("expected Sum[int64], got %T", m.Data)
	}
	totalFallbacks := int64(0)
	for _, dp := range d.DataPoints {
		totalFallbacks += dp.Value
	}
	if totalFallbacks != 2 {
		t.Errorf("expected 2 fallbacks, got %d", totalFallbacks)
	}
}

func TestRecordAnalysis(t *testing.T) {
	rec, rdr := setupRecorder(t)

	a := &activity.ComplexityAnalysis{
		ComplexityRatio:       0.75,
		HumanTimeSavedSeconds: 300,
		MoneySavedUSD:         25.0,
	}
	rec.RecordAnalysis("sess-1", "agent", a)

	rm := collectMetrics(t, rdr)

	m := findMetric(rm, "cost_saver.complexity_ratio")
	if m.Name != "cost_saver.complexity_ratio" {
		t.Fatal("cost_saver.complexity_ratio not found")
	}
	gd, ok := m.Data.(metricdata.Gauge[float64])
	if !ok {
		t.Fatalf("expected Gauge[float64], got %T", m.Data)
	}
	if len(gd.DataPoints) != 1 {
		t.Errorf("expected 1 gauge datapoint, got %d", len(gd.DataPoints))
	}
	if gd.DataPoints[0].Value != 0.75 {
		t.Errorf("expected complexity_ratio 0.75, got %v", gd.DataPoints[0].Value)
	}

	m = findMetric(rm, "cost_saver.human_time_saved")
	if m.Name != "cost_saver.human_time_saved" {
		t.Fatal("cost_saver.human_time_saved not found")
	}
	m = findMetric(rm, "cost_saver.money_saved.usd")
	if m.Name != "cost_saver.money_saved.usd" {
		t.Fatal("cost_saver.money_saved.usd not found")
	}
	m = findMetric(rm, "cost_saver.runs")
	if m.Name != "cost_saver.runs" {
		t.Fatal("cost_saver.runs not found")
	}
	d, ok := m.Data.(metricdata.Sum[int64])
	if !ok {
		t.Fatalf("expected Sum[int64] for runs, got %T", m.Data)
	}
	if d.DataPoints[0].Value != 1 {
		t.Errorf("expected runs count=1, got %d", d.DataPoints[0].Value)
	}
}

func TestSetRealtimeCost(t *testing.T) {
	rec, rdr := setupRecorder(t)

	rec.SetRealtimeCost("sess-1", "agent", 12.34)

	rm := collectMetrics(t, rdr)

	m := findMetric(rm, "llm.realtime.cost.usd")
	if m.Name != "llm.realtime.cost.usd" {
		t.Fatal("llm.realtime.cost.usd not found")
	}
	gd, ok := m.Data.(metricdata.Gauge[float64])
	if !ok {
		t.Fatalf("expected Gauge[float64], got %T", m.Data)
	}
	if len(gd.DataPoints) != 1 {
		t.Errorf("expected 1 datapoint, got %d", len(gd.DataPoints))
	}
	if gd.DataPoints[0].Value != 12.34 {
		t.Errorf("expected value 12.34, got %v", gd.DataPoints[0].Value)
	}
}

func TestNilRecorder(t *testing.T) {
	var r *OTelRecorder

	se := activity.StepEnded{Cost: 1.0, Tokens: activity.Tokens{Input: 10}}
	r.ObserveStep("m", "a", se)

	b := modelsdev.CostBreakdown{Total: 0.1}
	r.ObserveBreakdown("m", "a", b)

	r.RecordTask("s", "a", 1.0, true)
	r.RecordCompaction("a")
	r.RecordAnalysis("s", "a", &activity.ComplexityAnalysis{})
	r.RecordFallback("err")
	r.SetRealtimeCost("s", "a", 1.0)
}

func TestConfigMissingScope(t *testing.T) {
	_, err := NewOTelRecorder(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for nil Config with nil Scope")
	}
}

func TestConfigExplicitScopeRequired(t *testing.T) {
	_, err := NewOTelRecorder(context.Background(), &Config{})
	if err == nil {
		t.Fatal("expected error for Config with nil Scope")
	}
}
