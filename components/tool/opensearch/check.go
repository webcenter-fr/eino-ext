package opensearch

import (
	"context"
	"fmt"
	"time"

	"emperror.dev/errors"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/checkup"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/osclient"
)

const osCheckTimeout = 10 * time.Second

func Check(ctx context.Context, cfg *osclient.Config) checkup.Results {
	if cfg == nil || len(cfg.URLs) == 0 {
		return checkup.Results{
			{
				Component: "opensearch",
				Status:    checkup.StatusError,
				Error:     "no OpenSearch URLs configured",
			},
			{
				Component: "opensearch_search",
				Status:    checkup.StatusError,
				Error:     "no OpenSearch URLs configured",
			},
		}
	}

	baseCtx, cancel := context.WithTimeout(ctx, osCheckTimeout)
	defer cancel()

	client, err := NewClient(baseCtx, cfg)
	if err != nil {
		errStr := err.Error()
		return checkup.Results{
			{Component: "opensearch", Status: checkup.StatusError, Error: errStr},
			{Component: "opensearch_search", Status: checkup.StatusError, Error: errStr},
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
		Component: "opensearch_search",
		Status:    checkup.StatusLimited,
		Message:   fmt.Sprintf("requires search parameters for invocation, connectivity verified via %s", cfg.URLs),
	})

	return results
}
