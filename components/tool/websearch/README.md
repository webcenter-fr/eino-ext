# Web Search & Web Fetch Tools

eino tools for DuckDuckGo web search and URL fetching with HTML-to-Markdown
conversion and SSRF protection.

## Available Tools

| Tool Name | Description |
|---|---|
| `web_search` | Search the web via DuckDuckGo HTML/Lite backends |
| `web_fetch` | Fetch a URL and return content as Markdown, text, or HTML |

## Design

- **Dual backend** — `web_search` tries DuckDuckGo HTML first, falls back to
  DDG Lite on failure.
- **Anti-bot mitigation** — `web_search` uses cookie jar persistence, browser-like
  headers (Accept, Accept-Language, etc.), and session warm-up to reduce DuckDuckGo
  HTTP 202 anti-bot challenge responses.
- **Retry** — Both tools retry on transient errors (202, 403, 429) with
  exponential backoff up to `MaxRetry` attempts. On retry, the session is
  refreshed to reduce further 202 probability.
- **SSRF protection** — The HTTP transport blocks connections to private IP
  ranges (loopback, link-local, private, 169.254.0.0/16). Can be disabled with
  `SkipSSRFCheck` for testing.
- **Cloudflare bypass** — `web_fetch` automatically retries with a fallback
  User-Agent if a Cloudflare challenge page is detected.
- **Format conversion** — `web_fetch` converts HTML to Markdown (via
  `html-to-markdown`), plain text (tag stripping), or returns raw HTML.
- **Body size limits** — Response bodies are capped at `MaxBodySize` (default
  5MB).

## Configuration

```go
import "github.com/webcenter-fr/eino-ext/components/tool/websearch"

cfg := websearch.DefaultConfig()
cfg.Timeout = 60 * time.Second
cfg.UserAgent = "my-agent/1.0"

// Both tools at once
tools, err := websearch.NewAllTools(&cfg)
```

### Config options

| Field | Default | Description |
|---|---|---|
| `Timeout` | 30s | HTTP request timeout |
| `MaxRetry` | 3 | Max retry attempts on transient errors |
| `UserAgent` | Chrome 143 | User-Agent header |
| `MaxBodySize` | 5MB | Max response body size in bytes |
| `SkipSSRFCheck` | false | Disable SSRF protection (testing only) |
| `HTTPClient` | nil | Custom HTTP client (e.g. for `httptest`) |

## Usage

```go
tools, err := websearch.NewAllTools(&cfg)
if err != nil {
    return err
}

// Tools can be registered directly with a ToolsNode:
//   web_search(query="golang generics tutorial", numResults=5)
//   web_fetch(url="https://example.com", format="markdown")
```

### Individual tools

```go
searchTool, err := websearch.NewWebSearchTool(&cfg)
fetchTool, err := websearch.NewWebFetchTool(&cfg)
```

## Tool parameters

### web_search

| Parameter | Required | Description |
|---|---|---|
| `query` | Yes | Search query string |
| `numResults` | No | Number of results (default 10, max 20) |

### web_fetch

| Parameter | Required | Description |
|---|---|---|
| `url` | Yes | URL to fetch (http/https only) |
| `format` | No | Output format: `markdown`, `text`, or `html` (default `markdown`) |
| `timeout` | No | Per-request timeout in seconds (default 30, max 120) |

## Security

- URLs must use `http` or `https` scheme only
- URLs with embedded credentials are rejected
- All hostnames are resolved and checked against private/routed IP ranges
  before connecting
- Response bodies are bounded by `MaxBodySize` to prevent memory exhaustion
