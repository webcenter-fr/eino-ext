package costtrack

import (
	"github.com/webcenter-fr/eino-ext/callbacks/activity"
	"github.com/webcenter-fr/eino-ext/libs/modelsdev"
)

// MultiRecorder fans every Recorder call out to all non-nil recorders. Used to
// dual-export (e.g. PrometheusRecorder + OTelRecorder) during migration or when
// a deployment wants both /metrics and OTLP. Nil entries are skipped; a nil
// MultiRecorder is a no-op (nil-receiver safe, matching the Recorder contract).
type MultiRecorder []Recorder

var _ Recorder = MultiRecorder(nil)

// NewMultiRecorder creates a MultiRecorder from the given recorders. Nil entries
// are retained so callers can build a slice conditionally.
func NewMultiRecorder(recorders ...Recorder) MultiRecorder {
	return MultiRecorder(recorders)
}

// ObserveStep fans out the step observation to all non-nil recorders.
func (m MultiRecorder) ObserveStep(model, agent string, se activity.StepEnded) {
	for _, r := range m {
		if r != nil {
			r.ObserveStep(model, agent, se)
		}
	}
}

// ObserveBreakdown fans out the cost breakdown to all non-nil recorders.
func (m MultiRecorder) ObserveBreakdown(model, agent string, b modelsdev.CostBreakdown) {
	for _, r := range m {
		if r != nil {
			r.ObserveBreakdown(model, agent, b)
		}
	}
}

// RecordTask fans out the task record to all non-nil recorders.
func (m MultiRecorder) RecordTask(sessionID, agent string, cost float64, real bool) {
	for _, r := range m {
		if r != nil {
			r.RecordTask(sessionID, agent, cost, real)
		}
	}
}

// RecordCompaction fans out the compaction record to all non-nil recorders.
func (m MultiRecorder) RecordCompaction(agent string) {
	for _, r := range m {
		if r != nil {
			r.RecordCompaction(agent)
		}
	}
}

// RecordAnalysis fans out the analysis record to all non-nil recorders.
func (m MultiRecorder) RecordAnalysis(sessionID, agent string, a *activity.ComplexityAnalysis) {
	for _, r := range m {
		if r != nil {
			r.RecordAnalysis(sessionID, agent, a)
		}
	}
}

// RecordFallback fans out the fallback record to all non-nil recorders.
func (m MultiRecorder) RecordFallback(reason string) {
	for _, r := range m {
		if r != nil {
			r.RecordFallback(reason)
		}
	}
}

// SetRealtimeCost fans out the realtime cost to all non-nil recorders.
func (m MultiRecorder) SetRealtimeCost(sessionID, agent string, cost float64) {
	for _, r := range m {
		if r != nil {
			r.SetRealtimeCost(sessionID, agent, cost)
		}
	}
}
