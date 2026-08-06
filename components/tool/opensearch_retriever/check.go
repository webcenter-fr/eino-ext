package opensearch_retriever

import (
	"context"
	"fmt"
	"time"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/retriever"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/checkup"
)

const checkTimeout = 10 * time.Second

// Check performs a health check for the OpenSearch retriever tools,
// verifying each configured index is searchable.
func Check(ctx context.Context, configs []Config) checkup.Results {
	if len(configs) == 0 {
		return checkup.Results{
			{
				Component: "opensearch_retriever",
				Status:    checkup.StatusError,
				Error:     "no configs provided",
			},
		}
	}

	var results checkup.Results

	for _, cfg := range configs {
		results = append(results, probeConfig(ctx, cfg))
	}

	return results
}

func probeConfig(ctx context.Context, cfg Config) checkup.Result {
	baseCtx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()

	r, err := NewTool(baseCtx, &cfg)
	if err != nil {
		return checkup.Result{
			Component: "opensearch_retriever",
			Instance:  cfg.ToolName,
			Status:    checkup.StatusError,
			Error:     err.Error(),
		}
	}

	_, err = r.retriever.Retrieve(baseCtx, "__checkup_probe_query__", retriever.WithTopK(1))
	if err != nil {
		return checkup.Result{
			Component: "opensearch_retriever",
			Instance:  cfg.ToolName,
			Status:    checkup.StatusError,
			Error:     errors.Wrap(err, "dummy search failed").Error(),
		}
	}

	return checkup.Result{
		Component: "opensearch_retriever",
		Instance:  cfg.ToolName,
		Status:    checkup.StatusOK,
		Message:   fmt.Sprintf("search on index %q succeeded", cfg.Index),
	}
}
