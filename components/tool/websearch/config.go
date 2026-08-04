package websearch

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"time"
)

// DefaultConfig values for the websearch component.
const (
	DefaultTimeout     = 30 * time.Second
	DefaultMaxRetry    = 3
	DefaultUserAgent   = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36"
	DefaultMaxBodySize = 5 * 1024 * 1024 // 5MB
)

// Config holds the configuration for web search and web fetch tools.
type Config struct {
	// Timeout for HTTP requests. Defaults to 30s if zero.
	Timeout time.Duration `validate:"gte=0" jsonschema:"description=HTTP request timeout, defaults to 30s if zero"`
	// MaxRetry is the maximum number of retry attempts on transient errors.
	// Defaults to 3 if zero.
	MaxRetry int `validate:"gte=0" jsonschema:"description=Maximum retry attempts on transient errors, defaults to 3 if zero"`
	// UserAgent is the User-Agent header sent with requests.
	// Defaults to Chrome 143 on Windows if empty.
	UserAgent string `jsonschema:"description=User-Agent header sent with requests, defaults to Chrome 143 if empty"`
	// MaxBodySize is the maximum response body size in bytes.
	// Defaults to 5MB if zero.
	MaxBodySize int64 `validate:"gte=0" jsonschema:"description=Maximum response body size in bytes, defaults to 5MB if zero"`
	// DefaultFormat is the output format used when not specified in the request.
	// Must be one of: markdown, text, html. Defaults to markdown.
	DefaultFormat string `validate:"omitempty,oneof=markdown text html" jsonschema:"(optional, default markdown) Output format used when not specified in the request: markdown, text, or html."`
	// HTTPClient is an optional custom HTTP client. If nil, a default client
	// with the configured Timeout and SSRF-safe transport is used.
	// Useful for testing with custom transports (e.g. httptest.Server).
	//
	// CAVEAT: Setting a custom HTTPClient bypasses the built-in transport-level
	// SSRF checks (DNS rebinding defense at dial time). When using a custom
	// HTTPClient, ensure you implement equivalent SSRF protection in your own
	// transport, or rely solely on the pre-flight checkSSRF hostname resolution.
	HTTPClient *http.Client `json:"-" jsonschema:"-"`
	// SkipSSRFCheck disables SSRF protection. Only set this for testing
	// against local servers.
	//
	// CAVEAT: When SkipSSRFCheck is true, both the pre-flight hostname
	// resolution check AND the dial-time transport check are disabled.
	// Never enable this in production.
	SkipSSRFCheck bool `jsonschema:"description=Disable SSRF protection, only for testing"`
}

// DefaultConfig returns a Config with safe default values.
func DefaultConfig() Config {
	return Config{
		Timeout:       DefaultTimeout,
		MaxRetry:      DefaultMaxRetry,
		UserAgent:     DefaultUserAgent,
		MaxBodySize:   DefaultMaxBodySize,
		DefaultFormat: "markdown",
	}
}

// applyDefaults fills in zero-valued fields with defaults from another Config.
func (c Config) applyDefaults(defaults Config) Config {
	if c.Timeout <= 0 {
		c.Timeout = defaults.Timeout
	}
	if c.MaxRetry <= 0 {
		c.MaxRetry = defaults.MaxRetry
	}
	if c.UserAgent == "" {
		c.UserAgent = defaults.UserAgent
	}
	if c.MaxBodySize <= 0 {
		c.MaxBodySize = defaults.MaxBodySize
	}
	if c.DefaultFormat == "" {
		c.DefaultFormat = defaults.DefaultFormat
	}
	return c
}

// getHTTPClient returns the configured HTTP client, or builds a default one
// with an SSRF-safe transport (unless SkipSSRFCheck is set).
func getHTTPClient(cfg *Config) *http.Client {
	if cfg.HTTPClient != nil {
		return cfg.HTTPClient
	}

	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
	}

	// Only add the SSRF-safe dialer when protection is enabled AND the
	// environment does NOT route requests through an HTTP proxy. When a
	// proxy is configured (e.g. HTTPS_PROXY=http://squid.squid.svc:8080),
	// Go's transport calls DialContext for the connection TO the proxy —
	// the proxy's own address may be a private IP (e.g. squid.squid.svc
	// → 10.43.192.93). SSRF protection for the actual target is handled
	// upstream: webfetch uses the pre-flight checkSSRF, and search only
	// calls hardcoded DuckDuckGo URLs.
	//
	// We probe with an actual DDG URL so that NO_PROXY rules (e.g.
	// *.duckduckgo.com) are correctly resolved by ProxyFromEnvironment.
	probeReq, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, ddgLiteURL, nil)
	if !cfg.SkipSSRFCheck && proxyForURL(probeReq) == nil {
		transport.DialContext = ssrfSafeDialer
	}

	jar, _ := cookiejar.New(nil)

	return &http.Client{
		Transport: transport,
		Timeout:   cfg.Timeout,
		Jar:       jar,
	}
}

// proxyForURL returns the proxy URL that would be used for the given request,
// or nil if no proxy is configured. Delegates to http.ProxyFromEnvironment.
func proxyForURL(req *http.Request) *url.URL {
	u, _ := http.ProxyFromEnvironment(req)
	return u
}

// ssrfSafeDialer is a DialContext function that resolves the target hostname
// and blocks connections to private/routed IP addresses.
func ssrfSafeDialer(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve %s: %w", host, err)
	}
	for _, ip := range ips {
		if isPrivateIP(ip.IP) {
			return nil, fmt.Errorf("blocked access to private IP address: %s (host: %s)", ip.IP, host)
		}
	}
	var d net.Dialer
	return d.DialContext(ctx, network, net.JoinHostPort(host, port))
}
