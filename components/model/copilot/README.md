# copilot — GitHub Copilot chat model provider

`copilot` provides a `model.ToolCallingChatModel` backed by the GitHub Copilot
API (`https://api.githubcopilot.com`). It implements `model.ToolCallingChatModel`
and `model.ChatModel`.

The Copilot API is OpenAI-compatible, so the implementation wraps the existing
`openai.ChatModel` and injects Copilot-specific auth and headers on every request.

## Auth modes

The component supports two authentication modes:

| Mode | Config field | Description |
|------|-------------|-------------|
| Direct token | `CopilotToken` | Pre-obtained Copilot bearer token. Used directly with no refresh. |
| GitHub PAT | `GitHubToken` | GitHub PAT with `read:user` scope. Auto-exchanged for a Copilot token via `/copilot_internal/v2/token`. Supports background refresh. |

At least one of `GitHubToken` or `CopilotToken` must be set.

## Environment variables

If config fields are empty, the constructor falls back to environment variables:

| Env var | Config field |
|---------|-------------|
| `GITHUB_COPILOT_TOKEN` | `CopilotToken` |
| `GITHUB_TOKEN` | `GitHubToken` |
| `GITHUB_COPILOT_ENTERPRISE_URL` | `EnterpriseURL` |

## Request headers

Every request to the Copilot API includes these headers:

| Header | Value | Purpose |
|--------|-------|---------|
| `Authorization` | `Bearer {token}` | Authentication |
| `Copilot-Integration-ID` | `vscode-chat` | Required by Copilot API |
| `Editor-Version` | `vscode/1.100.0` | Required by Copilot API |
| `Editor-Plugin-Version` | `copilot-chat/0.52.0` | Required by Copilot API |
| `User-Agent` | `GitHubCopilotChat/0.52.0` | Required by Copilot API |
| `Openai-Intent` | `conversation-agent` | Mirrors Copilot CLI behavior |

## Usage

### Direct token

```go
import (
    "context"
    "github.com/webcenter-fr/eino-ext/components/model/copilot"
)

ctx := context.Background()
m, err := copilot.NewCopilotChatModel(ctx, &copilot.Config{
    CopilotToken: os.Getenv("GITHUB_COPILOT_TOKEN"),
})
```

### GitHub PAT with auto-refresh

```go
m, err := copilot.NewCopilotChatModel(ctx, &copilot.Config{
    GitHubToken: os.Getenv("GITHUB_TOKEN"),
})
```

### GitHub Enterprise

```go
m, err := copilot.NewCopilotChatModel(ctx, &copilot.Config{
    CopilotToken:  token,
    EnterpriseURL: "github.mycompany.com",
})
```

### Via chatmodel factory

```go
import "github.com/webcenter-fr/eino-ext/components/model/chatmodel"

m, err := chatmodel.New(ctx, &chatmodel.Config{
    Plan:    "github-copilot",
    APIKey:  copilotToken,
    Model:   "gpt-4o",
})
```

### TLS skip verify (self-signed certificates)

```go
m, err := copilot.NewCopilotChatModel(ctx, &copilot.Config{
    CopilotToken:  token,
    TLSSkipVerify: true,
})
```

## Configuration

| Field | Type | Description |
|-------|------|-------------|
| `GitHubToken` | `string` | GitHub PAT with `read:user` scope |
| `CopilotToken` | `string` | Pre-obtained Copilot bearer token |
| `EnterpriseURL` | `string` | GitHub Enterprise domain |
| `BaseURL` | `string` | Override Copilot API base URL |
| `Timeout` | `time.Duration` | API request timeout (≥1s, default 60m) |
| `TLSSkipVerify` | `bool` | Skip TLS certificate verification |

## Connectivity checkup

The `Check` function probes the Copilot API:

```go
results := copilot.Check(ctx, cfg)
fmt.Println(results.JSON("  "))
```

It verifies:
- Token exchange (when using `GitHubToken`)
- `/models` endpoint reachability
- Model availability (distinguishes "no models" as `"limited"` vs "error")

## Model discovery

`ListModels` fetches and filters available models from `GET /models`:

```go
models, err := copilot.ListModels(ctx, token, baseURL, 10*time.Second)
```
