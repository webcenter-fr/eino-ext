package prometheus

import (
	"context"
	"fmt"
	"time"

	"emperror.dev/errors"
	promapi "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/checkup"
)

const promCheckTimeout = 10 * time.Second

func Check(ctx context.Context, configs Configs) checkup.Results {
	if len(configs) == 0 {
		return checkup.Results{{
			Component: "prometheus",
			Status:    checkup.StatusError,
			Error:     "no Prometheus instances configured",
		}}
	}

	instances := configs.GetInstanceNames()
	var all checkup.Results

	for _, instance := range instances {
		cfg := configs.GetConfig(instance)
		baseCtx, baseCancel := context.WithTimeout(ctx, promCheckTimeout)

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
	return checkup.Results{
		{Component: "prometheus_alert_list", Instance: instance, Status: checkup.StatusError, Error: errStr},
		{Component: "prometheus_alert_describe", Instance: instance, Status: checkup.StatusError, Error: errStr},
		{Component: "prometheus_metric_query", Instance: instance, Status: checkup.StatusError, Error: errStr},
		{Component: "prometheus_metric_range", Instance: instance, Status: checkup.StatusError, Error: errStr},
	}
}

func probeInstance(ctx context.Context, client promapi.API, instance string) checkup.Results {
	var results checkup.Results

	ar, alerts, err := probeAlertList(ctx, client, instance)
	results = append(results, ar)
	if err == nil && len(alerts) > 0 {
		alertname := string(alerts[0].Labels["alertname"])
		if alertname == "" {
			results = append(results, checkup.Result{
				Component: "prometheus_alert_describe",
				Instance:  instance,
				Status:    checkup.StatusLimited,
				Message:   "first alert has no alertname label",
			})
		} else {
			results = append(results, probeAlertDescribe(instance, alertname, alerts))
		}
	} else if err == nil {
		results = append(results, checkup.Result{
			Component: "prometheus_alert_describe",
			Instance:  instance,
			Status:    checkup.StatusLimited,
			Message:   "no alerts to test describe",
		})
	} else {
		results = append(results, checkup.Result{
			Component: "prometheus_alert_describe",
			Instance:  instance,
			Status:    checkup.StatusError,
			Error:     "dependency failed",
		})
	}

	results = append(results, probeMetricQuery(ctx, client, instance))
	results = append(results, probeMetricRange(ctx, client, instance))

	return results
}

func probeAlertList(ctx context.Context, client promapi.API, instance string) (checkup.Result, []promapi.Alert, error) {
	alertsResult, err := client.Alerts(ctx)
	if err != nil {
		return checkup.Result{
			Component: "prometheus_alert_list",
			Instance:  instance,
			Status:    checkup.StatusError,
			Error:     errors.Wrap(err, "failed to list alerts").Error(),
		}, nil, err
	}
	msg := fmt.Sprintf("%d alerts found, RBAC ok", len(alertsResult.Alerts))
	return checkup.Result{
		Component: "prometheus_alert_list",
		Instance:  instance,
		Status:    checkup.StatusOK,
		Message:   msg,
	}, alertsResult.Alerts, nil
}

func probeAlertDescribe(instance, alertname string, alerts []promapi.Alert) checkup.Result {
	found := false
	for _, a := range alerts {
		if string(a.Labels["alertname"]) == alertname {
			found = true
			break
		}
	}
	if !found {
		return checkup.Result{
			Component: "prometheus_alert_describe",
			Instance:  instance,
			Status:    checkup.StatusLimited,
			Message:   fmt.Sprintf("alert %q no longer present", alertname),
		}
	}
	return checkup.Result{
		Component: "prometheus_alert_describe",
		Instance:  instance,
		Status:    checkup.StatusOK,
		Message:   fmt.Sprintf("described alert %q, RBAC ok", alertname),
	}
}

func probeMetricQuery(ctx context.Context, client promapi.API, instance string) checkup.Result {
	_, _, err := client.Query(ctx, "up", time.Time{})
	if err != nil {
		return checkup.Result{
			Component: "prometheus_metric_query",
			Instance:  instance,
			Status:    checkup.StatusError,
			Error:     errors.Wrap(err, "failed to execute metric query").Error(),
		}
	}
	return checkup.Result{
		Component: "prometheus_metric_query",
		Instance:  instance,
		Status:    checkup.StatusOK,
		Message:   "query 'up' succeeded, RBAC ok",
	}
}

func probeMetricRange(ctx context.Context, client promapi.API, instance string) checkup.Result {
	now := time.Now()
	r := promapi.Range{
		Start: now.Add(-5 * time.Minute),
		End:   now,
		Step:  1 * time.Minute,
	}
	_, _, err := client.QueryRange(ctx, "up", r)
	if err != nil {
		return checkup.Result{
			Component: "prometheus_metric_range",
			Instance:  instance,
			Status:    checkup.StatusError,
			Error:     errors.Wrap(err, "failed to execute range query").Error(),
		}
	}
	return checkup.Result{
		Component: "prometheus_metric_range",
		Instance:  instance,
		Status:    checkup.StatusOK,
		Message:   "range query 'up' succeeded, RBAC ok",
	}
}
