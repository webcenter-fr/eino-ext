package websearch

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"emperror.dev/errors"
)

const maxQueryLen = 500

// SearchResult represents a single web search result.
type SearchResult struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Description string `json:"description"`
}

// retrier is an error that signals the operation should be retried.
type retrier interface {
	Retryable()
}

// search performs a web search via SearXNG with retry.
func search(ctx context.Context, query string, cfg Config) ([]SearchResult, error) {
	if len(query) > maxQueryLen {
		return nil, fmt.Errorf("search query exceeds maximum length of %d characters", maxQueryLen)
	}
	if cfg.SearxngURL == "" {
		return nil, errors.New("SearxngURL is required for web_search. Set up a SearXNG instance and configure its base URL (e.g. https://searxng.example.com)")
	}
	client := getHTTPClient(&cfg)
	return searchWithRetry(ctx, query, cfg, client)
}

// searchWithRetry attempts a search with exponential backoff, retrying on
// transient errors and empty results.
func searchWithRetry(
	ctx context.Context,
	query string,
	cfg Config,
	client *http.Client,
) ([]SearchResult, error) {
	var lastErr error
	for attempt := 0; attempt <= cfg.MaxRetry; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(1<<(attempt-1)) * time.Second
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
		}

		results, err := searxngSearch(ctx, query, cfg, client)
		if err != nil {
			var r retrier
			if !errors.As(err, &r) {
				return nil, err
			}
			lastErr = err
			continue
		}

		if len(results) > 0 {
			return results, nil
		}

		lastErr = errors.New("search returned no results")
	}

	return nil, errors.Wrap(lastErr, fmt.Sprintf("search failed after %d attempts", cfg.MaxRetry+1))
}
