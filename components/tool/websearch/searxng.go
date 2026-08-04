package websearch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"emperror.dev/errors"
)

type searxngResponse struct {
	Results []searxngResult `json:"results"`
}

type searxngResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Content string `json:"content"`
}

// searxngSearch queries a SearXNG instance and returns parsed results.
func searxngSearch(ctx context.Context, query string, cfg Config, client *http.Client) ([]SearchResult, error) {
	u, err := url.Parse(cfg.SearxngURL)
	if err != nil {
		return nil, errors.Wrap(err, "invalid SearXNG URL")
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/search"
	q := u.Query()
	q.Set("q", query)
	q.Set("format", "json")
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create search request")
	}
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "search request failed")
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests ||
		resp.StatusCode == http.StatusServiceUnavailable {
		return nil, fmt.Errorf("search request received status %d (retryable)", resp.StatusCode)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return nil, fmt.Errorf("search request received status %d: %.200s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, cfg.MaxBodySize))
	if err != nil {
		return nil, errors.Wrap(err, "failed to read search response")
	}

	var payload searxngResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, errors.Wrap(err, "failed to decode SearXNG response")
	}

	results := make([]SearchResult, 0, len(payload.Results))
	for _, r := range payload.Results {
		results = append(results, SearchResult{
			Title:       r.Title,
			URL:         r.URL,
			Description: r.Content,
		})
	}

	return results, nil
}
