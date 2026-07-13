package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"

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
