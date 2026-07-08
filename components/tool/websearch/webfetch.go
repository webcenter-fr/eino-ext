package websearch

import (
	"context"
	_ "embed"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
	"github.com/cloudwego/eino/components/tool/utils"
)

//go:embed prompts/web_fetch.md
var webFetchDescription string

// kiloUserAgent is a fallback User-Agent used when the primary UA gets a 403
// that looks like a Cloudflare challenge.
const kiloUserAgent = "kilo/1.0"

// WebFetchParams are the input parameters for the web_fetch tool.
type WebFetchParams struct {
	URL     string `json:"url" validate:"required" jsonschema:"(required) The URL to fetch. Must start with http:// or https://."`
	Format  string `json:"format,omitempty" validate:"omitempty,oneof=markdown text html" jsonschema:"(optional, default markdown) Output format: markdown, text, or html."`
	Timeout int    `json:"timeout,omitempty" validate:"omitempty,min=1,max=120" jsonschema:"(optional, default 30, max 120) Request timeout in seconds."`
}

// WebFetchTool is an invokable tool that fetches web page content.
type WebFetchTool struct {
	cfg Config
	tool.InvokableTool
}

// Invoke fetches a web page and returns its content in the requested format.
func (t *WebFetchTool) Invoke(ctx context.Context, params *WebFetchParams) (string, error) {
	// Validate and sanitize the URL.
	fetchURL, err := validateFetchURL(params.URL)
	if err != nil {
		return "", err
	}

	// Validate URL against SSRF (can be skipped for testing).
	if !t.cfg.SkipSSRFCheck {
		if err := checkSSRF(fetchURL); err != nil {
			return "", err
		}
	}

	// Set format default.
	format := params.Format
	if format == "" {
		format = t.cfg.DefaultFormat
	}

	// Apply per-request timeout if specified.
	if params.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(params.Timeout)*time.Second)
		defer cancel()
	}

	// Fetch the URL using the configured HTTP client.
	client := getHTTPClient(&t.cfg)
	body, err := fetchURLWithRetry(ctx, fetchURL, t.cfg.UserAgent, t.cfg.MaxBodySize, client)
	if err != nil {
		return "", errors.Wrap(err, "failed to fetch URL")
	}

	// Convert to the requested format.
	switch format {
	case "html":
		return string(body), nil
	case "text":
		text, textErr := stripTagsToText(string(body))
		if textErr != nil {
			return "", textErr
		}
		return text, nil
	case "markdown":
		md, mdErr := htmlToMarkdown(string(body))
		if mdErr != nil {
			return "", mdErr
		}
		return md, nil
	default:
		return "", fmt.Errorf("unsupported format: %s (must be markdown, text, or html)", format)
	}
}

// validateFetchURL validates and sanitizes the fetch URL.
func validateFetchURL(rawURL string) (string, error) {
	if rawURL == "" {
		return "", errors.New("URL is required")
	}

	// Strip leading/trailing whitespace.
	rawURL = strings.TrimSpace(rawURL)

	// Parse the URL.
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", errors.Wrapf(err, "invalid URL: %s", rawURL)
	}

	// Only allow http and https.
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("unsupported URL scheme: %s (only http and https are allowed)", parsed.Scheme)
	}

	// Reject URLs with credentials.
	if parsed.User != nil {
		return "", errors.New("URLs with embedded credentials are not allowed")
	}

	return rawURL, nil
}

// fetchURLWithRetry fetches a URL with retry on transient errors and Cloudflare
// challenges. It never returns a Cloudflare block page as a successful result.
func fetchURLWithRetry(ctx context.Context, urlStr, ua string, maxSize int64, client *http.Client) ([]byte, error) {
	userAgents := []string{ua, kiloUserAgent}

	var lastErr error

	for i, agent := range userAgents {
		if i > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(1 * time.Second):
			}
		}

		body, err := doFetchRequest(ctx, urlStr, agent, maxSize, client)
		if err != nil {
			lastErr = err
			continue
		}

		// If this is a Cloudflare block page, retry with the next UA.
		if isCloudflareBlock(body) {
			lastErr = errors.New("received Cloudflare challenge page")
			continue
		}

		return body, nil
	}

	// All user agents failed.
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, errors.New("failed to fetch URL: all user agents were blocked")
}

// doFetchRequest performs the actual HTTP GET request.
func doFetchRequest(ctx context.Context, urlStr, ua string, maxSize int64, client *http.Client) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create fetch request")
	}
	req.Header.Set("User-Agent", ua)

	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "fetch request failed")
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("fetch request received status %d (retryable)", resp.StatusCode)
	}

	if resp.StatusCode != http.StatusOK {
		// Try to read body for context even on non-200.
		body, _ := readLimitedBody(resp.Body, 8192) // Read up to 8KB for error context.
		return nil, fmt.Errorf("fetch request received status %d: %.200s", resp.StatusCode, string(body))
	}

	body, err := readLimitedBody(resp.Body, maxSize)
	if err != nil {
		return nil, err
	}

	return body, nil
}

// isCloudflareBlock checks if the response body looks like a Cloudflare
// challenge page. The comparison is case-insensitive.
func isCloudflareBlock(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	limit := len(body)
	if limit > 1024 {
		limit = 1024
	}
	s := strings.ToLower(string(body[:limit]))
	return strings.Contains(s, "cloudflare") &&
		(strings.Contains(s, "challenge") || strings.Contains(s, "captcha") ||
			strings.Contains(s, "just a moment") || strings.Contains(s, "ray id"))
}

// checkSSRF validates a URL against SSRF attacks by resolving the hostname
// and checking the resolved IP addresses against private/routed ranges.
// For dial-time protection the default transport in getHTTPClient also performs
// this check at connect time (to prevent DNS rebinding).
func checkSSRF(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return errors.Wrap(err, "failed to parse URL for SSRF check")
	}

	host := parsed.Hostname()
	if host == "" {
		return errors.New("URL has no hostname")
	}

	ips, err := net.LookupIP(host)
	if err != nil {
		return errors.Wrapf(err, "failed to resolve host: %s", host)
	}

	for _, ip := range ips {
		if isPrivateIP(ip) {
			return fmt.Errorf("blocked access to private IP address: %s (host: %s)", ip.String(), host)
		}
	}

	return nil
}

// isPrivateIP checks if an IP address is in a private/routed range.
func isPrivateIP(ip net.IP) bool {
	if ip.IsLoopback() {
		return true
	}
	if ip.IsLinkLocalUnicast() {
		return true
	}
	if ip.IsLinkLocalMulticast() {
		return true
	}
	if ip.IsPrivate() {
		return true
	}
	// Also check for IPv4 private ranges not covered by IsPrivate.
	ip4 := ip.To4()
	if ip4 != nil {
		// 169.254.0.0/16 (link-local, sometimes not caught by IsPrivate for IPv4 mapped)
		if ip4[0] == 169 && ip4[1] == 254 {
			return true
		}
	}
	return false
}

// NewWebFetchTool creates a new web_fetch tool.
func NewWebFetchTool(ctx context.Context, cfg *Config) (tool.InvokableTool, error) {
	if cfg == nil {
		c := DefaultConfig()
		cfg = &c
	}
	if err := validate.Struct(cfg); err != nil {
		return nil, errors.Wrap(err, "invalid webfetch config")
	}
	// Make a local copy so the caller's config is not mutated.
	localCfg := cfg.applyDefaults(DefaultConfig())

	fetchTool := &WebFetchTool{
		cfg: localCfg,
	}

	invokable, err := utils.InferTool("web_fetch", webFetchDescription, fetchTool.Invoke)
	if err != nil {
		return nil, err
	}
	fetchTool.InvokableTool = invokable

	return fetchTool, nil
}
