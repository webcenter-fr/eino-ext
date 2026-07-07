package websearch

import (
	"context"
	"time"

	"emperror.dev/errors"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/checkup"
)

const wsCheckTimeout = 30 * time.Second

func Check(ctx context.Context, cfg *Config) checkup.Results {
	if cfg == nil {
		c := DefaultConfig()
		cfg = &c
	}

	baseCtx, cancel := context.WithTimeout(ctx, wsCheckTimeout)
	defer cancel()

	var results checkup.Results
	localCfg := cfg.applyDefaults(DefaultConfig())

	searchTool := &WebSearchTool{cfg: localCfg}
	_, err := searchTool.Invoke(baseCtx, &WebSearchParams{Query: "health check", NumResults: 1})
	if err != nil {
		results = append(results, checkup.Result{
			Component: "web_search",
			Status:    checkup.StatusError,
			Error:     errors.Wrap(err, "web search probe failed").Error(),
		})
	} else {
		results = append(results, checkup.Result{
			Component: "web_search",
			Status:    checkup.StatusOK,
			Message:   "search query succeeded",
		})
	}

	fetchTool := &WebFetchTool{cfg: localCfg}
	_, err = fetchTool.Invoke(baseCtx, &WebFetchParams{
		URL:     "https://httpbin.org/get",
		Timeout: 30,
	})
	if err != nil {
		results = append(results, checkup.Result{
			Component: "web_fetch",
			Status:    checkup.StatusError,
			Error:     errors.Wrap(err, "web fetch probe failed").Error(),
		})
	} else {
		results = append(results, checkup.Result{
			Component: "web_fetch",
			Status:    checkup.StatusOK,
			Message:   "URL fetch succeeded",
		})
	}

	return results
}
