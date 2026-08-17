package grafana

import (
	"context"
	"fmt"
	"time"

	"emperror.dev/errors"
	"github.com/goccy/go-json"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/checkup"
)

const grafanaCheckTimeout = 10 * time.Second

// Check probes connectivity and RBAC permissions for all configured Grafana
// instances. For each instance it tests every read-only tool. The write tool
// (dashboard write) is not probed to avoid side effects.
func Check(ctx context.Context, configs Configs) checkup.Results {
	if len(configs) == 0 {
		return checkup.Results{{
			Component: "grafana",
			Status:    checkup.StatusError,
			Error:     "no Grafana instances configured",
		}}
	}

	instances := configs.GetInstanceNames()
	var all checkup.Results

	for _, instance := range instances {
		cfg := configs.GetConfig(instance)
		baseCtx, baseCancel := context.WithTimeout(ctx, grafanaCheckTimeout)

		client, err := NewClient(baseCtx, cfg)
		if err != nil {
			all = append(all, clientErrorResults(instance, err)...)
			baseCancel()
			continue
		}

		func() {
			defer baseCancel()
			all = append(all, probeInstance(baseCtx, client, instance)...)
		}()
	}

	return all
}

func clientErrorResults(instance string, err error) checkup.Results {
	errStr := err.Error()
	results := make(checkup.Results, len(allComponentNames()))
	for i, name := range allComponentNames() {
		results[i] = checkup.Result{
			Component: name,
			Instance:  instance,
			Status:    checkup.StatusError,
			Error:     errStr,
		}
	}
	return results
}

func allComponentNames() []string {
	return []string{
		"grafana_instance_list",
		"grafana_dashboard",
		"grafana_dashboard_write",
		"grafana_datasource",
	}
}

func probeInstance(ctx context.Context, client *grafanaClient, instance string) checkup.Results {
	return checkup.Results{
		// grafana_instance_list always ok
		{
			Component: "grafana_instance_list",
			Instance:  instance,
			Status:    checkup.StatusOK,
		},
		probeDashboard(ctx, client, instance),
		// grafana_dashboard_write — limited, no side effects
		{
			Component: dashboardWriteToolName,
			Instance:  instance,
			Status:    checkup.StatusLimited,
			Message:   "write tool, not probed to avoid side effects",
		},
		probeDataSource(ctx, client, instance),
	}
}

func probeDashboard(ctx context.Context, client *grafanaClient, instance string) checkup.Result {
	body, err := client.SearchDashboards(ctx, &searchParams{Limit: 1})
	if err != nil {
		return checkup.Result{
			Component: "grafana_dashboard",
			Instance:  instance,
			Status:    checkup.StatusError,
			Error:     errors.Wrap(err, "failed to search dashboards").Error(),
		}
	}

	var hits []searchHit
	if err := json.Unmarshal(body, &hits); err != nil {
		return checkup.Result{
			Component: "grafana_dashboard",
			Instance:  instance,
			Status:    checkup.StatusError,
			Error:     errors.Wrap(err, "failed to unmarshal search results").Error(),
		}
	}

	msg := fmt.Sprintf("%d dashboards found, RBAC ok", len(hits))

	// If a dashboard is present, probe the describe path (GET by UID) too.
	if len(hits) > 0 && hits[0].UID != "" {
		if _, err := client.GetDashboard(ctx, hits[0].UID); err != nil {
			return checkup.Result{
				Component: "grafana_dashboard",
				Instance:  instance,
				Status:    checkup.StatusError,
				Error:     errors.Wrap(err, "failed to get dashboard").Error(),
			}
		}
		msg = fmt.Sprintf("%d dashboards found, describe ok, RBAC ok", len(hits))
	}

	return checkup.Result{
		Component: "grafana_dashboard",
		Instance:  instance,
		Status:    checkup.StatusOK,
		Message:   msg,
	}
}

func probeDataSource(ctx context.Context, client *grafanaClient, instance string) checkup.Result {
	body, err := client.ListDataSources(ctx)
	if err != nil {
		return checkup.Result{
			Component: "grafana_datasource",
			Instance:  instance,
			Status:    checkup.StatusError,
			Error:     errors.Wrap(err, "failed to list data sources").Error(),
		}
	}

	var sources []dataSource
	if err := json.Unmarshal(body, &sources); err != nil {
		return checkup.Result{
			Component: "grafana_datasource",
			Instance:  instance,
			Status:    checkup.StatusError,
			Error:     errors.Wrap(err, "failed to unmarshal data sources").Error(),
		}
	}

	msg := fmt.Sprintf("%d data sources found, RBAC ok", len(sources))

	// If a data source is present, probe the describe path (GET by UID) too.
	if len(sources) > 0 && sources[0].UID != "" {
		if _, err := client.GetDataSource(ctx, sources[0].UID); err != nil {
			return checkup.Result{
				Component: "grafana_datasource",
				Instance:  instance,
				Status:    checkup.StatusError,
				Error:     errors.Wrap(err, "failed to get data source").Error(),
			}
		}
		msg = fmt.Sprintf("%d data sources found, describe ok, RBAC ok", len(sources))
	}

	return checkup.Result{
		Component: "grafana_datasource",
		Instance:  instance,
		Status:    checkup.StatusOK,
		Message:   msg,
	}
}
