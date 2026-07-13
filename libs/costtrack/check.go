package costtrack

import (
	"context"
	"fmt"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/checkup"
)

// Check probes the Tracker's readiness: it verifies that the Prometheus
// registry gathers successfully and that the catalog resolves at least one
// model. Returns limited (not error) when the catalog has no priced models.
func (t *Tracker) Check(_ context.Context) checkup.Results {
	var results checkup.Results

	if gatherer, ok := t.reg.(prometheus.Gatherer); ok {
		mfs, err := gatherer.Gather()
		if err != nil {
			results = append(results, checkup.Result{
				Component: "costtrack",
				Instance:  "prometheus",
				Status:    checkup.StatusError,
				Error:     fmt.Sprintf("gather failed: %v", err),
			})
		} else {
			results = append(results, checkup.Result{
				Component: "costtrack",
				Instance:  "prometheus",
				Status:    checkup.StatusOK,
				Message:   fmt.Sprintf("%d metric families gathered", len(mfs)),
			})
		}
	}

	cat := t.catalogHolder.Load()
	if cat == nil {
		results = append(results, checkup.Result{
			Component: "costtrack",
			Instance:  "catalog",
			Status:    checkup.StatusLimited,
			Message:   "catalog not loaded",
		})
		return results
	}

	results = append(results, checkup.Result{
		Component: "costtrack",
		Instance:  "catalog",
		Status:    checkup.StatusOK,
		Message:   fmt.Sprintf("catalog fresh=%v, loadErr=%v", cat.Fresh, cat.LoadErr),
	})

	return results
}
