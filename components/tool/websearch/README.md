# Web Search & Web Fetch Tools

eino tools for SearXNG web search and URL fetching with HTML-to-Markdown
conversion and SSRF protection.

## Available Tools

| Tool Name | Description |
|---|---|
| `web_search` | Search the web via a SearXNG instance (self-hosted metasearch engine) |
| `web_fetch` | Fetch a URL and return content as Markdown, text, or HTML |

## Design

- **SearXNG backend** — `web_search` queries a SearXNG instance which aggregates
  results from Google, Bing, Brave, Wikipedia, and other engines. SearXNG provides
  a clean JSON API with no tokens or rate limits.
- **Retry** — Retries on transient errors (429, 503) with exponential backoff up
  to `MaxRetry` attempts.
- **SSRF protection** — The HTTP transport blocks connections to private IP
  ranges (loopback, link-local, private, 169.254.0.0/16). Can be disabled with
  `SkipSSRFCheck` for testing.
- **Cloudflare bypass** — `web_fetch` automatically retries with a fallback
  User-Agent if a Cloudflare challenge page is detected.
- **Format conversion** — `web_fetch` converts HTML to Markdown (via
  `html-to-markdown`), plain text (tag stripping), or returns raw HTML.
- **Body size limits** — Response bodies are capped at `MaxBodySize` (default
  5MB).

## Prerequisites

### SearXNG Instance

`web_search` requires a SearXNG instance. The recommended way to run it is with
Docker Compose.

Create a `searxng` directory with the following files:

**`docker-compose.yml`**

```yaml
services:
  searxng:
    image: searxng/searxng:latest
    container_name: searxng
    ports:
      - "8080:8080"
    volumes:
      - ./searxng/settings.yml:/etc/searxng/settings.yml:ro
    restart: unless-stopped
```

**`searxng/settings.yml`**

```yaml
# Read the documentation before extending the defaults:
# https://docs.searxng.org/admin/settings/

use_default_settings: true

search:
  safe_search: 0
  autocomplete: ""
  formats:
    - html
    - json

server:
  # Shared secret for API authentication. Must match SearxngSecretKey
  # in the Go config. Generate with: openssl rand -hex 32
  secret_key: "CHANGE_ME_GENERATE_RANDOM_KEY"
  bind_address: "0.0.0.0"
  # Rate limiter disabled so the JSON API can handle burst queries.
  # Re-enable if the instance is exposed to the internet.
  limiter: false
  image_proxy: true

ui:
  static_use_hash: true

# Disable Redis for single-instance setups. Enable for multi-worker deployments.
redis:
  url: false
```

**Key settings:**

| Setting | Value | Purpose |
|---|---|---|
| `search.formats` | `[html, json]` | Enables the `/search?format=json` endpoint used by `web_search` |
| `server.limiter` | `false` | Disables rate limiting so programmatic queries are not throttled |
| `redis.url` | `false` | Single-instance mode; set to a Redis URL for multi-worker deployments |
| `server.secret_key` | random hex | Required; generate with `openssl rand -hex 32` |

**Start the instance:**

```bash
docker compose up -d
```

**Verify it works:**

```bash
curl "http://localhost:8080/search?q=test&format=json"
```

See [SearXNG docs](https://docs.searxng.org) for production hardening, scaling,
and engine configuration.

## Configuration

```go
import "github.com/webcenter-fr/eino-ext/components/tool/websearch"

cfg := websearch.DefaultConfig()
cfg.SearxngURL = "http://localhost:8080" // Required for web_search
cfg.Timeout = 60 * time.Second

// Both tools at once
tools, err := websearch.NewAllTools(ctx, &cfg)
```

### Config options

| Field | Default | Description |
|---|---|---|
| `SearxngURL` | (required) | Base URL of a SearXNG instance (e.g. `https://searxng.example.com`) |
| `SearxngSecretKey` | (optional) | Shared secret matching `server.secret_key` in settings.yml; sent as `Authorization: Bearer <key>` |
| `Timeout` | 30s | HTTP request timeout |
| `MaxRetry` | 3 | Max retry attempts on transient errors |
| `UserAgent` | Chrome 143 | User-Agent header (used by web_fetch) |
| `MaxBodySize` | 5MB | Max response body size in bytes |
| `SkipSSRFCheck` | false | Disable SSRF protection (testing only) |
| `HTTPClient` | nil | Custom HTTP client (e.g. for `httptest`) |

## Usage

```go
tools, err := websearch.NewAllTools(ctx, &cfg)
if err != nil {
    return err
}

// Tools can be registered directly with a ToolsNode:
//   web_search(query="golang generics tutorial", numResults=5)
//   web_fetch(url="https://example.com", format="markdown")
```

### Individual tools

```go
searchTool, err := websearch.NewWebSearchTool(ctx, &cfg)
fetchTool, err := websearch.NewWebFetchTool(ctx, &cfg)
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
