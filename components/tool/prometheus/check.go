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

// Check performs a health check against configured Prometheus instances.
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
		} else {
			all = append(all, probeInstance(baseCtx, client, instance)...)
		}

		baseCancel()
	}

	return all
}

func clientErrorResults(instance string, err error) checkup.Results {
	errStr := err.Error()
	return checkup.Results{
		{Component: "prometheus_instance_list", Instance: instance, Status: checkup.StatusError, Error: errStr},
		{Component: "prometheus_metric", Instance: instance, Status: checkup.StatusError, Error: errStr},
		{Component: "prometheus_target_list", Instance: instance, Status: checkup.StatusError, Error: errStr},
	}
}

func probeInstance(ctx context.Context, client promapi.API, instance string) checkup.Results {
	return checkup.Results{
		probeInstanceList(instance),
		probeMetric(ctx, client, instance),
		probeTargetList(ctx, client, instance),
	}
}

func probeInstanceList(instance string) checkup.Result {
	return checkup.Result{
		Component: "prometheus_instance_list",
		Instance:  instance,
		Status:    checkup.StatusOK,
	}
}

func probeMetric(ctx context.Context, client promapi.API, instance string) checkup.Result {
	_, _, err := client.Query(ctx, "up", time.Time{})
	if err != nil {
		return checkup.Result{
			Component: "prometheus_metric",
			Instance:  instance,
			Status:    checkup.StatusError,
			Error:     errors.Wrap(err, "failed to execute metric query").Error(),
		}
	}
	return checkup.Result{
		Component: "prometheus_metric",
		Instance:  instance,
		Status:    checkup.StatusOK,
		Message:   "query 'up' succeeded, RBAC ok",
	}
}

func probeTargetList(ctx context.Context, client promapi.API, instance string) checkup.Result {
	targetsResult, err := client.Targets(ctx)
	if err != nil {
		return checkup.Result{
			Component: "prometheus_target_list",
			Instance:  instance,
			Status:    checkup.StatusError,
			Error:     errors.Wrap(err, "failed to list targets").Error(),
		}
	}
	msg := fmt.Sprintf("%d active targets, %d dropped targets, RBAC ok", len(targetsResult.Active), len(targetsResult.Dropped))
	return checkup.Result{
		Component: "prometheus_target_list",
		Instance:  instance,
		Status:    checkup.StatusOK,
		Message:   msg,
	}
}
