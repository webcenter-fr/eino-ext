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
		"grafana_query",
		"grafana_dashboard_validate",
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
		probeQuery(ctx, client, instance),
		probeDashboardValidate(ctx, client, instance),
	}
}

// errorResult builds a checkup error result for the given component.
func errorResult(component, instance string, err error) checkup.Result {
	return checkup.Result{
		Component: component,
		Instance:  instance,
		Status:    checkup.StatusError,
		Error:     err.Error(),
	}
}

// searchDashboards lists up to one dashboard for probing, wrapping HTTP and
// unmarshal errors with operation context.
func searchDashboards(ctx context.Context, client *grafanaClient) ([]searchHit, error) {
	body, err := client.SearchDashboards(ctx, &searchParams{Limit: 1})
	if err != nil {
		return nil, errors.Wrap(err, "failed to search dashboards")
	}
	var hits []searchHit
	if err := json.Unmarshal(body, &hits); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal search results")
	}
	return hits, nil
}

// listDataSources lists all data sources for probing, wrapping HTTP and
// unmarshal errors with operation context.
func listDataSources(ctx context.Context, client *grafanaClient) ([]dataSource, error) {
	body, err := client.ListDataSources(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to list data sources")
	}
	var sources []dataSource
	if err := json.Unmarshal(body, &sources); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal data sources")
	}
	return sources, nil
}

// firstQueryableDataSource returns the first Prometheus datasource (preferred)
// or Loki datasource, plus a trivial expression to probe it with.
func firstQueryableDataSource(sources []dataSource) (uid, dsType, expr string) {
	for _, ds := range sources {
		if ds.Type == "prometheus" {
			return ds.UID, ds.Type, "up"
		}
	}
	for _, ds := range sources {
		if ds.Type == "loki" {
			return ds.UID, ds.Type, `sum(count_over_time({job=~".+"}[1m]))`
		}
	}
	return "", "", ""
}

func probeDashboard(ctx context.Context, client *grafanaClient, instance string) checkup.Result {
	hits, err := searchDashboards(ctx, client)
	if err != nil {
		return errorResult("grafana_dashboard", instance, err)
	}

	msg := fmt.Sprintf("%d dashboards found, RBAC ok", len(hits))

	// If a dashboard is present, probe the describe path (GET by UID) too.
	if len(hits) > 0 && hits[0].UID != "" {
		if _, err := client.GetDashboard(ctx, hits[0].UID); err != nil {
			return errorResult("grafana_dashboard", instance, errors.Wrap(err, "failed to get dashboard"))
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
	sources, err := listDataSources(ctx, client)
	if err != nil {
		return errorResult("grafana_datasource", instance, err)
	}

	msg := fmt.Sprintf("%d data sources found, RBAC ok", len(sources))

	// If a data source is present, probe the describe path (GET by UID) too.
	if len(sources) > 0 && sources[0].UID != "" {
		if _, err := client.GetDataSource(ctx, sources[0].UID); err != nil {
			return errorResult("grafana_datasource", instance, errors.Wrap(err, "failed to get data source"))
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

func probeQuery(ctx context.Context, client *grafanaClient, instance string) checkup.Result {
	sources, err := listDataSources(ctx, client)
	if err != nil {
		return errorResult(queryToolName, instance, err)
	}

	uid, dsType, expr := firstQueryableDataSource(sources)
	if dsType == "" || uid == "" {
		return checkup.Result{
			Component: queryToolName,
			Instance:  instance,
			Status:    checkup.StatusLimited,
			Message:   "no prometheus or loki datasource configured",
		}
	}

	var qerr error
	switch dsType {
	case "prometheus":
		_, qerr = client.QueryPrometheus(ctx, uid, expr, "instant", time.Now(), time.Time{}, time.Time{}, 0)
	case "loki":
		_, qerr = client.QueryLoki(ctx, uid, expr, "instant", time.Now(), time.Time{}, time.Time{}, 0, 0, "")
	}
	if qerr != nil {
		return errorResult(queryToolName, instance, errors.Wrap(qerr, "failed to execute query"))
	}

	return checkup.Result{
		Component: queryToolName,
		Instance:  instance,
		Status:    checkup.StatusOK,
		Message:   fmt.Sprintf("query ok against %s datasource %q", dsType, uid),
	}
}

func probeDashboardValidate(ctx context.Context, client *grafanaClient, instance string) checkup.Result {
	hits, err := searchDashboards(ctx, client)
	if err != nil {
		return errorResult(dashboardValidateToolName, instance, err)
	}

	if len(hits) == 0 || hits[0].UID == "" {
		return checkup.Result{
			Component: dashboardValidateToolName,
			Instance:  instance,
			Status:    checkup.StatusLimited,
			Message:   "no dashboards configured",
		}
	}

	db, err := client.GetDashboard(ctx, hits[0].UID)
	if err != nil {
		return errorResult(dashboardValidateToolName, instance, errors.Wrap(err, "failed to get dashboard"))
	}

	var dr dashboardResponse
	if err := json.Unmarshal(db, &dr); err != nil {
		return errorResult(dashboardValidateToolName, instance, errors.Wrap(err, "failed to unmarshal dashboard"))
	}

	panels := collectPanels(dr.Dashboard)
	queryCount := 0
	for _, p := range panels {
		queryCount += len(extractPanelQueries(p))
	}

	return checkup.Result{
		Component: dashboardValidateToolName,
		Instance:  instance,
		Status:    checkup.StatusOK,
		Message:   fmt.Sprintf("%d panels, %d queries extracted", len(panels), queryCount),
	}
}
