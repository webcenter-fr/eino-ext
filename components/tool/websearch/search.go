package websearch

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"emperror.dev/errors"
	"golang.org/x/net/html"
)

const (
	ddgHTMLURL = "https://html.duckduckgo.com/html/"
	ddgLiteURL = "https://lite.duckduckgo.com/lite/"
)

// SearchResult represents a single web search result.
type SearchResult struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Description string `json:"description"`
}

// search performs a DuckDuckGo HTML search with retry and fallback.
func search(ctx context.Context, query string, cfg Config) ([]SearchResult, error) {
	client := getHTTPClient(&cfg)

	backends := []struct {
		name string
		fn   func(ctx context.Context, query string, client *http.Client, ua string) (io.Reader, error)
	}{
		{"DDG HTML", searchDDGHTML},
		{"DDG Lite", searchDDGLite},
	}

	var lastErr error
	for _, backend := range backends {
		results, err := searchWithRetry(ctx, query, cfg, backend.fn, client)
		if err == nil {
			return results, nil
		}
		lastErr = errors.Wrap(err, fmt.Sprintf("backend %s failed", backend.name))
	}

	return nil, lastErr
}

// searchWithRetry attempts a search with the given backend, retrying on transient errors.
func searchWithRetry(
	ctx context.Context,
	query string,
	cfg Config,
	backend func(ctx context.Context, query string, client *http.Client, ua string) (io.Reader, error),
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

		bodyReader, err := backend(ctx, query, client, cfg.UserAgent)
		if err != nil {
			lastErr = err
			continue
		}

		results, err := parseDDGHTML(bodyReader, cfg.MaxBodySize)
		if err != nil {
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

// searchDDGHTML performs a search using DuckDuckGo's HTML endpoint.
func searchDDGHTML(ctx context.Context, queryStr string, client *http.Client, ua string) (io.Reader, error) {
	u, err := url.Parse(ddgHTMLURL)
	if err != nil {
		return nil, errors.Wrap(err, "failed to parse DDG HTML URL")
	}
	u.RawQuery = url.Values{"q": []string{queryStr}}.Encode()
	return doSearchRequest(ctx, u.String(), client, ua)
}

// searchDDGLite performs a search using DuckDuckGo's lite endpoint.
func searchDDGLite(ctx context.Context, queryStr string, client *http.Client, ua string) (io.Reader, error) {
	u, err := url.Parse(ddgLiteURL)
	if err != nil {
		return nil, errors.Wrap(err, "failed to parse DDG Lite URL")
	}
	u.RawQuery = url.Values{"q": []string{queryStr}}.Encode()
	return doSearchRequest(ctx, u.String(), client, ua)
}

// doSearchRequest performs an HTTP GET request for search and returns the
// response body as an io.Reader (bounded by MaxBodySize from the Config).
func doSearchRequest(ctx context.Context, urlStr string, client *http.Client, ua string) (io.Reader, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create search request")
	}
	req.Header.Set("User-Agent", ua)

	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "search request failed")
	}

	// 202, 403, 429 are considered retryable.
	if resp.StatusCode == http.StatusAccepted ||
		resp.StatusCode == http.StatusForbidden ||
		resp.StatusCode == http.StatusTooManyRequests {
		resp.Body.Close()
		return nil, fmt.Errorf("search request received status %d (retryable)", resp.StatusCode)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return nil, fmt.Errorf("search request received status %d: %.200s", resp.StatusCode, string(body))
	}

	return resp.Body, nil
}

// parseDDGHTML parses DuckDuckGo HTML search results from the html.duckduckgo.com format.
// The reader is limited to maxSize bytes to prevent excessive memory use.
func parseDDGHTML(r io.Reader, maxSize int64) ([]SearchResult, error) {
	// Read into a buffer for parsing — limit to maxSize.
	buf, err := readLimitedBody(r, maxSize)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read search results")
	}

	doc, err := html.Parse(bytes.NewReader(buf))
	if err != nil {
		return nil, errors.Wrap(err, "failed to parse search results HTML")
	}

	var results []SearchResult
	extractDDGResults(doc, &results)

	// If no results from the standard format, try the lite format.
	if len(results) == 0 {
		extractDDGLiteResults(doc, &results)
	}

	return results, nil
}

// extractDDGResults walks the HTML tree and extracts results from the html.duckduckgo.com format.
func extractDDGResults(n *html.Node, results *[]SearchResult) {
	if n.Type == html.ElementNode && n.Data == "div" && hasClass(n, "result__body") {
		var r SearchResult
		r.Title, r.URL = extractResultTitleAndURL(n)
		r.Description = extractResultSnippet(n)

		if r.URL == "" {
			r.URL = extractResultLink(n)
		}

		if r.Title != "" || r.Description != "" {
			*results = append(*results, r)
		}
	}

	for c := n.FirstChild; c != nil; c = c.NextSibling {
		extractDDGResults(c, results)
	}
}

// extractResultTitleAndURL extracts title and URL from a result__body node.
func extractResultTitleAndURL(n *html.Node) (title, link string) {
	var f func(*html.Node)
	f = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "a" && hasClass(node, "result__a") {
			title = getTextContent(node)
			for _, attr := range node.Attr {
				if attr.Key == "href" {
					link = extractDDGURL(attr.Val)
				}
			}
			return
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			f(c)
		}
	}
	f(n)
	return
}

// extractResultSnippet extracts description/snippet from a result__body node.
func extractResultSnippet(n *html.Node) string {
	var snippet *html.Node
	var f func(*html.Node)
	f = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "a" && hasClass(node, "result__snippet") {
			snippet = node
			return
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			if snippet == nil {
				f(c)
			}
		}
	}
	f(n)
	if snippet != nil {
		return strings.TrimSpace(getTextContent(snippet))
	}
	return ""
}

// extractResultLink extracts the first href from a link in the result body.
func extractResultLink(n *html.Node) string {
	var link string
	var f func(*html.Node)
	f = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "a" && link == "" {
			for _, attr := range node.Attr {
				if attr.Key == "href" {
					link = extractDDGURL(attr.Val)
				}
			}
			return
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			if link == "" {
				f(c)
			}
		}
	}
	f(n)
	return link
}

// extractDDGURL extracts the real URL from a DDG redirect URL.
func extractDDGURL(raw string) string {
	if strings.Contains(raw, "uddg=") {
		u, err := url.Parse(raw)
		if err == nil {
			uddg := u.Query().Get("uddg")
			if uddg != "" {
				return uddg
			}
		}
	}
	return raw
}

// extractDDGLiteResults extracts results from the lite.duckduckgo.com format.
func extractDDGLiteResults(doc *html.Node, results *[]SearchResult) {
	var collect func(*html.Node)
	collect = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" && hasClass(n, "result-link") {
			title := strings.TrimSpace(getTextContent(n))
			link := ""
			for _, attr := range n.Attr {
				if attr.Key == "href" {
					link = attr.Val
				}
			}
			if title != "" {
				*results = append(*results, SearchResult{
					Title:       title,
					URL:         link,
					Description: extractLiteSnippet(n),
				})
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			collect(c)
		}
	}
	collect(doc)
}

// extractLiteSnippet gets the snippet text following a result link in lite format.
func extractLiteSnippet(linkNode *html.Node) string {
	parent := linkNode.Parent
	if parent == nil {
		return ""
	}
	return strings.TrimSpace(getTextContent(parent))
}

// hasClass checks if an HTML node has a given CSS class.
func hasClass(n *html.Node, class string) bool {
	for _, attr := range n.Attr {
		if attr.Key == "class" {
			for _, c := range strings.Fields(attr.Val) {
				if c == class {
					return true
				}
			}
		}
	}
	return false
}

// getTextContent extracts all text content from an HTML node tree.
func getTextContent(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	var sb strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		sb.WriteString(getTextContent(c))
	}
	return sb.String()
}
