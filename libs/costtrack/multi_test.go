package costtrack

import (
	"testing"

	"github.com/webcenter-fr/eino-ext/callbacks/activity"
	"github.com/webcenter-fr/eino-ext/libs/modelsdev"
)

// callCountRecorder records every call count for testing MultiRecorder fan-out.
type callCountRecorder struct {
	observeStep      int
	observeBreakdown int
	recordTask       int
	recordCompaction int
	recordAnalysis   int
	recordFallback   int
	setRealtimeCost  int
}

func (c *callCountRecorder) ObserveStep(model, agent string, se activity.StepEnded) {
	c.observeStep++
}

func (c *callCountRecorder) ObserveBreakdown(model, agent string, b modelsdev.CostBreakdown) {
	c.observeBreakdown++
}

func (c *callCountRecorder) RecordTask(sessionID, agent string, cost float64, real bool) {
	c.recordTask++
}

func (c *callCountRecorder) RecordCompaction(agent string) {
	c.recordCompaction++
}

func (c *callCountRecorder) RecordAnalysis(sessionID, agent string, a *activity.ComplexityAnalysis) {
	c.recordAnalysis++
}

func (c *callCountRecorder) RecordFallback(reason string) {
	c.recordFallback++
}

func (c *callCountRecorder) SetRealtimeCost(sessionID, agent string, cost float64) {
	c.setRealtimeCost++
}

func TestMultiRecorder_FansOut(t *testing.T) {
	r1 := &callCountRecorder{}
	r2 := &callCountRecorder{}

	mr := NewMultiRecorder(r1, r2)

	se := activity.StepEnded{Cost: 0.1}
	mr.ObserveStep("m", "a", se)
	mr.ObserveBreakdown("m", "a", modelsdev.CostBreakdown{})
	mr.RecordTask("s", "a", 1.0, true)
	mr.RecordCompaction("a")
	mr.RecordAnalysis("s", "a", &activity.ComplexityAnalysis{})
	mr.RecordFallback("err")
	mr.SetRealtimeCost("s", "a", 1.0)

	for name, r := range map[string]*callCountRecorder{"r1": r1, "r2": r2} {
		if r.observeStep != 1 {
			t.Errorf("%s.observeStep = %d, want 1", name, r.observeStep)
		}
		if r.observeBreakdown != 1 {
			t.Errorf("%s.observeBreakdown = %d, want 1", name, r.observeBreakdown)
		}
		if r.recordTask != 1 {
			t.Errorf("%s.recordTask = %d, want 1", name, r.recordTask)
		}
		if r.recordCompaction != 1 {
			t.Errorf("%s.recordCompaction = %d, want 1", name, r.recordCompaction)
		}
		if r.recordAnalysis != 1 {
			t.Errorf("%s.recordAnalysis = %d, want 1", name, r.recordAnalysis)
		}
		if r.recordFallback != 1 {
			t.Errorf("%s.recordFallback = %d, want 1", name, r.recordFallback)
		}
		if r.setRealtimeCost != 1 {
			t.Errorf("%s.setRealtimeCost = %d, want 1", name, r.setRealtimeCost)
		}
	}
}

func TestMultiRecorder_SkipsNil(t *testing.T) {
	r1 := &callCountRecorder{}

	mr := NewMultiRecorder(nil, r1, nil)
	mr.ObserveStep("m", "a", activity.StepEnded{})

	if r1.observeStep != 1 {
		t.Errorf("r1.observeStep = %d, want 1", r1.observeStep)
	}
}

func TestMultiRecorder_NilReceiver(t *testing.T) {
	var mr MultiRecorder
	mr.ObserveStep("m", "a", activity.StepEnded{})
	mr.ObserveBreakdown("m", "a", modelsdev.CostBreakdown{})
	mr.RecordTask("s", "a", 1.0, true)
	mr.RecordCompaction("a")
	mr.RecordAnalysis("s", "a", &activity.ComplexityAnalysis{})
	mr.RecordFallback("err")
	mr.SetRealtimeCost("s", "a", 1.0)
}

func TestMultiRecorder_Empty(t *testing.T) {
	mr := NewMultiRecorder()
	mr.ObserveStep("m", "a", activity.StepEnded{})
}

func TestMultiRecorder_CompileCheck(t *testing.T) {
	var _ Recorder = MultiRecorder(nil)
}
