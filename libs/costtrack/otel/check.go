package otel

import (
	"context"
	"fmt"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/checkup"
)

// Check probes the OTelRecorder's readiness by verifying that the underlying
// scope is healthy and instruments exist. Returns limited (not error) when the
// global provider is noop.
func Check(ctx context.Context, rec *OTelRecorder) checkup.Results {
	var results checkup.Results

	if rec == nil {
		results = append(results, checkup.Result{
			Component: "costtrack_otel_recorder",
			Status:    checkup.StatusError,
			Error:     "recorder is nil",
		})
		return results
	}

	if rec.scope == nil {
		results = append(results, checkup.Result{
			Component: "costtrack_otel_recorder",
			Status:    checkup.StatusError,
			Error:     "scope is nil",
		})
		return results
	}

	required := map[string]bool{
		"tokens":            rec.tokens != nil,
		"cost":              rec.cost != nil,
		"savings":           rec.savings != nil,
		"costByComponent":   rec.costByComponent != nil,
		"tasksTotal":        rec.tasksTotal != nil,
		"taskCostHist":      rec.taskCostHist != nil,
		"compactions":       rec.compactions != nil,
		"realtimeCost":      rec.realtimeCost != nil,
		"complexityRatio":   rec.complexityRatio != nil,
		"humanTimeSaved":    rec.humanTimeSaved != nil,
		"moneySaved":        rec.moneySaved != nil,
		"fallbackCount":     rec.fallbackCount != nil,
		"costSaverRuns":     rec.costSaverRuns != nil,
	}

	for name, ok := range required {
		if !ok {
			results = append(results, checkup.Result{
				Component: "costtrack_otel_recorder",
				Status:    checkup.StatusError,
				Error:     fmt.Sprintf("instrument %s is nil", name),
			})
		}
	}
	if len(results) > 0 {
		return results
	}

	results = append(results, checkup.Result{
		Component: "costtrack_otel_recorder",
		Status:    checkup.StatusOK,
		Message:   "all instruments ready",
	})
	return results
}
