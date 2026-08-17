package alertmanager

import (
	"context"
	"fmt"
	"time"

	"emperror.dev/errors"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/checkup"
)

const alertmanagerCheckTimeout = 10 * time.Second

// Check performs a health check against configured Alertmanager instances.
func Check(ctx context.Context, configs Configs) checkup.Results {
	if len(configs) == 0 {
		return checkup.Results{{
			Component: "alertmanager",
			Status:    checkup.StatusError,
			Error:     "no Alertmanager instances configured",
		}}
	}

	instances := configs.GetInstanceNames()
	var all checkup.Results

	for _, instance := range instances {
		cfg := configs.GetConfig(instance)
		baseCtx, baseCancel := context.WithTimeout(ctx, alertmanagerCheckTimeout)

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
	names := allComponentNames()
	errStr := err.Error()
	results := make(checkup.Results, len(names))
	for i, name := range names {
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
		instanceListToolName,
		alertToolName,
		alertWriteToolName,
	}
}

func probeInstance(ctx context.Context, c *alertmanagerClient, instance string) checkup.Results {
	return checkup.Results{
		{
			Component: instanceListToolName,
			Instance:  instance,
			Status:    checkup.StatusOK,
		},
		probeAlert(ctx, c, instance),
		{
			Component: alertWriteToolName,
			Instance:  instance,
			Status:    checkup.StatusOK,
			Message:   "guidance tool, no external call required",
		},
	}
}

// probeAlert performs a real GET /api/v2/alerts (read-only and safe) for the
// alertmanager_alert tool.
func probeAlert(ctx context.Context, c *alertmanagerClient, instance string) checkup.Result {
	alerts, err := c.ListAlerts(ctx, &listAlertsParams{Active: boolPtr(true)})
	if err != nil {
		return checkup.Result{
			Component: alertToolName,
			Instance:  instance,
			Status:    checkup.StatusError,
			Error:     errors.Wrap(err, "failed to list Alertmanager alerts").Error(),
		}
	}
	return checkup.Result{
		Component: alertToolName,
		Instance:  instance,
		Status:    checkup.StatusOK,
		Message:   fmt.Sprintf("%d alerts found, RBAC ok", len(alerts)),
	}
}
