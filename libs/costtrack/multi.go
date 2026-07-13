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

func (m MultiRecorder) ObserveStep(model, agent string, se activity.StepEnded) {
	for _, r := range m {
		if r != nil {
			r.ObserveStep(model, agent, se)
		}
	}
}

func (m MultiRecorder) ObserveBreakdown(model, agent string, b modelsdev.CostBreakdown) {
	for _, r := range m {
		if r != nil {
			r.ObserveBreakdown(model, agent, b)
		}
	}
}

func (m MultiRecorder) RecordTask(sessionID, agent string, cost float64, real bool) {
	for _, r := range m {
		if r != nil {
			r.RecordTask(sessionID, agent, cost, real)
		}
	}
}

func (m MultiRecorder) RecordCompaction(agent string) {
	for _, r := range m {
		if r != nil {
			r.RecordCompaction(agent)
		}
	}
}

func (m MultiRecorder) RecordAnalysis(sessionID, agent string, a *activity.ComplexityAnalysis) {
	for _, r := range m {
		if r != nil {
			r.RecordAnalysis(sessionID, agent, a)
		}
	}
}

func (m MultiRecorder) RecordFallback(reason string) {
	for _, r := range m {
		if r != nil {
			r.RecordFallback(reason)
		}
	}
}

func (m MultiRecorder) SetRealtimeCost(sessionID, agent string, cost float64) {
	for _, r := range m {
		if r != nil {
			r.SetRealtimeCost(sessionID, agent, cost)
		}
	}
}
