package metrics

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/webcenter-fr/eino-ext/callbacks/activity"
)

// CostSaverCollector records metrics for LLM-based cost savings analysis.
// It exposes gauges for complexity ratio, human time saved, and money saved.
type CostSaverCollector struct {
	complexityRatio       *prometheus.GaugeVec
	humanTimeSavedSeconds *prometheus.GaugeVec
	moneySavedUSD         *prometheus.GaugeVec
	fallbackCount         *prometheus.CounterVec
	runs                  *prometheus.CounterVec
}

// NewCostSaverCollector creates a CostSaverCollector and registers its metrics on reg.
func NewCostSaverCollector(reg prometheus.Registerer) (*CostSaverCollector, error) {
	c := &CostSaverCollector{
		complexityRatio: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cost_saver_complexity_ratio",
			Help: "Complexity ratio of the session (0.0-1.0) as computed by LLM analyzer.",
		}, []string{"session_id", "agent"}),
		humanTimeSavedSeconds: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cost_saver_human_time_saved_seconds",
			Help: "Estimated human time saved in seconds.",
		}, []string{"session_id", "agent"}),
		moneySavedUSD: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cost_saver_money_saved_usd",
			Help: "Estimated money saved in USD based on human time and hourly rate.",
		}, []string{"session_id", "agent"}),
		fallbackCount: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cost_saver_fallback_count_total",
			Help: "Count of fallback to simple formula when LLM analysis failed.",
		}, []string{"reason"}),
		runs: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cost_saver_runs_total",
			Help: "Total number of cost saver analysis runs completed.",
		}, []string{"agent"}),
	}
	if err := reg.Register(c.complexityRatio); err != nil {
		return nil, err
	}
	if err := reg.Register(c.humanTimeSavedSeconds); err != nil {
		return nil, err
	}
	if err := reg.Register(c.moneySavedUSD); err != nil {
		return nil, err
	}
	if err := reg.Register(c.fallbackCount); err != nil {
		return nil, err
	}
	if err := reg.Register(c.runs); err != nil {
		return nil, err
	}
	return c, nil
}

// RecordAnalysis records the complexity analysis results for a session.
func (c *CostSaverCollector) RecordAnalysis(sessionID, agent string, analysis *activity.ComplexityAnalysis) {
	if c == nil {
		return
	}
	c.complexityRatio.WithLabelValues(sessionID, agent).Set(analysis.ComplexityRatio)
	c.humanTimeSavedSeconds.WithLabelValues(sessionID, agent).Set(analysis.HumanTimeSavedSeconds)
	c.moneySavedUSD.WithLabelValues(sessionID, agent).Set(analysis.MoneySavedUSD)
	c.runs.WithLabelValues(agent).Inc()
}

// RecordFallback increments the fallback counter for a given reason.
func (c *CostSaverCollector) RecordFallback(reason string) {
	if c == nil {
		return
	}
	c.fallbackCount.WithLabelValues(reason).Inc()
}
