package opensearch

import (
	"context"
	"fmt"
	"time"

	"emperror.dev/errors"
	"github.com/disaster37/opensearch/v3/config"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/checkup"
)

const osCheckTimeout = 10 * time.Second

func Check(ctx context.Context, cfg *config.Config) checkup.Results {
	if cfg == nil || len(cfg.URLs) == 0 {
		return checkup.Results{
			{
				Component: "opensearch",
				Status:    checkup.StatusError,
				Error:     "no OpenSearch URLs configured",
			},
			{
				Component: "opensearch_log_kubernetes",
				Status:    checkup.StatusError,
				Error:     "no OpenSearch URLs configured",
			},
		}
	}

	baseCtx, cancel := context.WithTimeout(ctx, osCheckTimeout)
	defer cancel()

	client, err := NewClient(cfg)
	if err != nil {
		errStr := err.Error()
		return checkup.Results{
			{Component: "opensearch", Status: checkup.StatusError, Error: errStr},
			{Component: "opensearch_log_kubernetes", Status: checkup.StatusError, Error: errStr},
		}
	}

	var results checkup.Results

	_, err = client.Cat().Health(baseCtx)
	if err != nil {
		results = append(results, checkup.Result{
			Component: "opensearch",
			Status:    checkup.StatusError,
			Error:     errors.Wrap(err, "failed to get cluster health").Error(),
		})
	} else {
		results = append(results, checkup.Result{
			Component: "opensearch",
			Status:    checkup.StatusOK,
			Message:   "cluster health check succeeded, connectivity ok",
		})
	}

	results = append(results, checkup.Result{
		Component: "opensearch_log_kubernetes",
		Status:    checkup.StatusLimited,
		Message:   fmt.Sprintf("requires pod-specific parameters for invoke, connectivity verified via %s", cfg.URLs),
	})

	return results
}
