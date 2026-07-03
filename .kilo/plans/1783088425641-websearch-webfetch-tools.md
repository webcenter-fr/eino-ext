# Web Search & Web Fetch Tools for eino-ext

## Goal

Add `web_search` and `web_fetch` tools to eino-ext that work **without any API key**,
using DuckDuckGo HTML scraping for search and direct HTTP GET for page fetching.

## Background

- Kilocode's `websearch` uses Exa/Parallel MCP providers (require API keys) — not suitable.
- Kilocode's `webfetch` uses direct HTTP GET with HTML→Markdown conversion — replicable in Go.
- DuckDuckGo API returns 202 due to bot protection; using the old HTML-only version avoids this.

## Package: `components/tool/websearch/`

### Files

```
components/tool/websearch/
├── config.go              # Config struct
├── registry.go            # NewAllTools(), NewWebSearchTool(), NewWebFetchTool()
├── websearch.go           # DuckDuckGo HTML-based search tool (utils.InferTool)
├── webfetch.go            # HTTP fetch with HTML→Markdown/Text conversion (utils.InferTool)
├── search.go              # DDG search: HTTP request + HTML result parsing
├── html2text.go           # HTML→text extraction, HTML→Markdown conversion helpers
├── prompts/
│   ├── web_search.md      # Web search description (//go:embed)
│   └── web_fetch.md       # Web fetch description (//go:embed)
├── suite_test.go          # httptest.Server-based test suite
├── websearch_test.go      # Search tool tests
└── webfetch_test.go       # Fetch tool tests
```

### Tool 1: `web_search`

- **Tool name**: `web_search`
- **Input struct**:
  - `query` (string, required) — search query
  - `numResults` (int, default 10, max 20)
- **Output**: JSON array of `[{title, url, description}]`
- **Primary backend**: `https://html.duckduckgo.com/html/?q=<query>`
- **Fallback backend**: `https://lite.duckduckgo.com/lite/?q=<query>`
- **Retry**: 3 attempts with exponential backoff (1s, 2s, 4s) on 202/403/429
- **User-Agent**: Chrome 143 on Windows (same as kilocode)
- **Parsing**: Use `golang.org/x/net/html` to extract result blocks (title, link, snippet)
- **Timeout**: Configurable, default 30s

### Tool 2: `web_fetch`

- **Tool name**: `web_fetch`
- **Input struct**:
  - `url` (string, required) — URL to fetch
  - `format` (string, optional, default "markdown") — "markdown", "text", or "html"
  - `timeout` (int, optional, default 30, max 120) — seconds
- **Output**: Raw content string in requested format
- **Validation**: URL must start with `http://` or `https://`
- **User-Agent**: Chrome 143 + retry with "kilo" UA on Cloudflare 403 challenge
- **Size limit**: 5MB max response
- **HTML→Markdown**: Using `github.com/JohannesKaufmann/html-to-markdown/v2`
- **HTML→Text**: Using `golang.org/x/net/html` (strip scripts, styles, noscript, iframe)
- **Timeouts**: Configurable, constrained to max 120s

### Config Struct

```go
type Config struct {
    Timeout     time.Duration // default 30s
    MaxRetry    int           // default 3
    UserAgent   string        // default Chrome UA
    MaxBodySize int64         // default 5MB
}
```

### Registry (factory functions)

```go
func NewWebSearchTool(cfg *Config) (tool.InvokableTool, error)
func NewWebFetchTool(cfg *Config) (tool.InvokableTool, error)
func NewAllTools(cfg *Config) ([]tool.InvokableTool, error)
```

### Dependencies

- `golang.org/x/net/html` — already an indirect dependency, for HTML parsing
- `github.com/JohannesKaufmann/html-to-markdown/v2` — **new**, for HTML→Markdown conversion
- `github.com/goccy/go-json` — already used, for JSON output
- `emperror.dev/errors` — already used, for error wrapping
- `github.com/cloudwego/eino/components/tool` and `tool/utils` — for tool creation (`utils.InferTool`)

### Security

- **SSRF prevention**: Validate URL scheme (only http/https), resolve host and block private/routed IP ranges:
  - `127.0.0.0/8`, `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`, `169.254.0.0/16`
  - `::1`, `fc00::/7`, `fe80::/10`
- **URL validation**: No `file://`, `javascript:`, etc. Strip credentials from URLs.
- **Response size cap**: 5MB hard limit on response body.
- **Rate limiting**: Exponential backoff between retries (1s → 2s → 4s).

### Testing

- `httptest.Server` to mock DuckDuckGo search result pages and arbitrary web pages
- Test DDG HTML result parsing (title, URL, snippet extraction)
- Test DDG Lite fallback when HTML version fails
- Test fetch format conversion (markdown, text, html)
- Test SSRF blocking (localhost, private IPs)
- Test timeout handling
- Test 5MB size limit enforcement
- Test retry behavior on 429/403

### Conventions (per CONTRIBUTING.md)

- Tool descriptions in `prompts/*.md` with `//go:embed`
- `utils.InferTool[T, D]` for tool creation
- `emperror.dev/errors` for error wrapping
- Table-driven tests with `testify/suite`
- Factory function `NewAllTools()` for creating all tools at once
