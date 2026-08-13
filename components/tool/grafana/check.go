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
// (dashboard build) is not probed to avoid side effects.
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
		"grafana_dashboard_search",
		"grafana_dashboard_describe",
		"grafana_dashboard_build",
		"grafana_datasource_list",
		"grafana_datasource_describe",
	}
}

func probeInstance(ctx context.Context, client *grafanaClient, instance string) checkup.Results {
	var results checkup.Results

	// grafana_instance_list always ok
	results = append(results, checkup.Result{
		Component: "grafana_instance_list",
		Instance:  instance,
		Status:    checkup.StatusOK,
	})

	// grafana_dashboard_search
	searchResult, firstUID, err := probeSearch(ctx, client, instance)
	results = append(results, searchResult)

	if err == nil && firstUID != "" {
		// grafana_dashboard_describe
		results = append(results, probeDescribe(ctx, client, instance, firstUID))
	} else if err == nil {
		results = append(results, checkup.Result{
			Component: "grafana_dashboard_describe",
			Instance:  instance,
			Status:    checkup.StatusLimited,
			Message:   "no dashboards to test describe",
		})
	} else {
		results = append(results, checkup.Result{
			Component: "grafana_dashboard_describe",
			Instance:  instance,
			Status:    checkup.StatusError,
			Error:     "dependency failed",
		})
	}

	// grafana_dashboard_build — limited, no side effects
	results = append(results, checkup.Result{
		Component: "grafana_dashboard_build",
		Instance:  instance,
		Status:    checkup.StatusLimited,
		Message:   "write tool, not probed to avoid side effects",
	})

	// grafana_datasource_list
	listResult, firstDSUID, err := probeDataSourceList(ctx, client, instance)
	results = append(results, listResult)

	if err == nil && firstDSUID != "" {
		results = append(results, probeDataSourceDescribe(ctx, client, instance, firstDSUID))
	} else if err == nil {
		results = append(results, checkup.Result{
			Component: "grafana_datasource_describe",
			Instance:  instance,
			Status:    checkup.StatusLimited,
			Message:   "no data sources to test describe",
		})
	} else {
		results = append(results, checkup.Result{
			Component: "grafana_datasource_describe",
			Instance:  instance,
			Status:    checkup.StatusError,
			Error:     "dependency failed",
		})
	}

	return results
}

func probeSearch(ctx context.Context, client *grafanaClient, instance string) (checkup.Result, string, error) {
	body, err := client.SearchDashboards(ctx, &searchParams{Limit: 1})
	if err != nil {
		return checkup.Result{
			Component: "grafana_dashboard_search",
			Instance:  instance,
			Status:    checkup.StatusError,
			Error:     errors.Wrap(err, "failed to search dashboards").Error(),
		}, "", err
	}

	var hits []searchHit
	if err := json.Unmarshal(body, &hits); err != nil {
		return checkup.Result{
			Component: "grafana_dashboard_search",
			Instance:  instance,
			Status:    checkup.StatusError,
			Error:     errors.Wrap(err, "failed to unmarshal search results").Error(),
		}, "", err
	}

	msg := fmt.Sprintf("%d dashboards found, RBAC ok", len(hits))

	firstUID := ""
	if len(hits) > 0 {
		firstUID = hits[0].UID
	}

	return checkup.Result{
		Component: "grafana_dashboard_search",
		Instance:  instance,
		Status:    checkup.StatusOK,
		Message:   msg,
	}, firstUID, nil
}

func probeDescribe(ctx context.Context, client *grafanaClient, instance, uid string) checkup.Result {
	body, err := client.GetDashboard(ctx, uid)
	if err != nil {
		return checkup.Result{
			Component: "grafana_dashboard_describe",
			Instance:  instance,
			Status:    checkup.StatusError,
			Error:     errors.Wrap(err, "failed to get dashboard").Error(),
		}
	}

	var dr dashboardResponse
	if err := json.Unmarshal(body, &dr); err != nil {
		return checkup.Result{
			Component: "grafana_dashboard_describe",
			Instance:  instance,
			Status:    checkup.StatusError,
			Error:     errors.Wrap(err, "failed to unmarshal dashboard").Error(),
		}
	}

	title := ""
	if dr.Dashboard != nil {
		if t, ok := dr.Dashboard["title"].(string); ok {
			title = t
		}
	}

	return checkup.Result{
		Component: "grafana_dashboard_describe",
		Instance:  instance,
		Status:    checkup.StatusOK,
		Message:   fmt.Sprintf("described dashboard %q, RBAC ok", title),
	}
}

func probeDataSourceList(ctx context.Context, client *grafanaClient, instance string) (checkup.Result, string, error) {
	body, err := client.ListDataSources(ctx)
	if err != nil {
		return checkup.Result{
			Component: "grafana_datasource_list",
			Instance:  instance,
			Status:    checkup.StatusError,
			Error:     errors.Wrap(err, "failed to list data sources").Error(),
		}, "", err
	}

	var sources []dataSource
	if err := json.Unmarshal(body, &sources); err != nil {
		return checkup.Result{
			Component: "grafana_datasource_list",
			Instance:  instance,
			Status:    checkup.StatusError,
			Error:     errors.Wrap(err, "failed to unmarshal data sources").Error(),
		}, "", err
	}

	firstUID := ""
	if len(sources) > 0 {
		firstUID = sources[0].UID
	}

	return checkup.Result{
		Component: "grafana_datasource_list",
		Instance:  instance,
		Status:    checkup.StatusOK,
		Message:   fmt.Sprintf("%d data sources found, RBAC ok", len(sources)),
	}, firstUID, nil
}

func probeDataSourceDescribe(ctx context.Context, client *grafanaClient, instance, uid string) checkup.Result {
	body, err := client.GetDataSource(ctx, uid)
	if err != nil {
		return checkup.Result{
			Component: "grafana_datasource_describe",
			Instance:  instance,
			Status:    checkup.StatusError,
			Error:     errors.Wrap(err, "failed to get data source").Error(),
		}
	}

	var ds dataSource
	if err := json.Unmarshal(body, &ds); err != nil {
		return checkup.Result{
			Component: "grafana_datasource_describe",
			Instance:  instance,
			Status:    checkup.StatusError,
			Error:     errors.Wrap(err, "failed to unmarshal data source").Error(),
		}
	}

	return checkup.Result{
		Component: "grafana_datasource_describe",
		Instance:  instance,
		Status:    checkup.StatusOK,
		Message:   fmt.Sprintf("described data source %q (type %s), RBAC ok", ds.Name, ds.Type),
	}
}
