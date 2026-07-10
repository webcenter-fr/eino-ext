# copilot — GitHub Copilot chat model provider

`copilot` provides a `model.ToolCallingChatModel` backed by the GitHub Copilot
API (`https://api.githubcopilot.com`). It implements `model.ToolCallingChatModel`
and `model.ChatModel`.

This package makes direct HTTP calls using `net/http` — it does not depend on
any OpenAI SDK or ACL library.

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
| `Openai-Intent` | `conversation-edits` | Mirrors Copilot extension behavior |
| `x-initiator` | `user` or `agent` | Per-request: `user` for plain text prompts, `agent` for tool/assistant follow-ups |
| `Copilot-Vision-Request` | `true` | Only sent when a message contains image parts |

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
| `Model` | `string` | Model ID to use (required at call time; can be overridden via `model.WithModel`) |
| `Temperature` | `*float32` | Sampling temperature (0 to 2) |
| `MaxCompletionTokens` | `*int` | Upper bound on generated tokens (sent as `max_tokens` for chat, `max_output_tokens` for responses) |
| `ReasoningEffort` | `ReasoningEffort` | Reasoning effort: `low`, `medium`, or `high` |

## Vision / image input

When a user message contains `UserInputMultiContent` (or the deprecated
`MultiContent`) with `image_url` parts, the package sends array content with
`text` and `image_url` parts, matching the OpenAI-compatible format:

```go
imgURL := "https://example.com/photo.png"
msg := &schema.Message{
    Role: schema.User,
    UserInputMultiContent: []schema.MessageInputPart{
        {Type: schema.ChatMessagePartTypeText, Text: "What's in this image?"},
        {
            Type: schema.ChatMessagePartTypeImageURL,
            Image: &schema.MessageInputImage{
                MessagePartCommon: schema.MessagePartCommon{URL: &imgURL},
            },
        },
    },
}
```

The `Copilot-Vision-Request: true` header is sent automatically when any
message carries an image part.

## Reasoning round-trip

The provider supports multi-turn reasoning via `reasoning_text` (the model's
thinking) and `reasoning_opaque` (an opaque blob for Copilot's multi-turn
context):

- **Inbound**: `reasoning_text` → `Message.ReasoningContent`;
  `reasoning_opaque` → `Message.Extra["copilot_reasoning_opaque"]`.
- **Outbound**: `Message.ReasoningContent` → `reasoning_text`;
  `Message.Extra["copilot_reasoning_opaque"]` → `reasoning_opaque`.

This ensures reasoning context is preserved across conversation turns.

## Per-call reasoning effort

The `Config.ReasoningEffort` field sets a default that can be overridden per
call via `model.WrapImplSpecificOptFn`. The effort is passed as the
`reasoning_effort` request field.

## GPT-5 Responses API routing

GPT-5-class models (`gpt-5`, `gpt-5-chat-latest`, `gpt-6`, etc.)
automatically use the Copilot `/responses` endpoint instead of
`/chat/completions`. The exception is `gpt-5-mini`, which stays on the chat
completions path.

The routing is handled by `useResponsesAPI(modelID)`:
- Models matching `gpt-N` where N ≥ 5 (and not `gpt-5-mini`) → `/responses`
- All other models → `/chat/completions`

**Built-in provider tools** (web_search, code_interpreter, image_generation,
file_search, local_shell) and their approval/mcp_approval_response flows are
intentionally unsupported. The package only handles function tools. Unknown
SSE event types from the Responses stream are tolerated silently.

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

The returned `ModelInfo` includes:
- `ID`, `Name`, `Family`, `Version`
- `MaxContextWindowTokens`, `MaxOutputTokens`, `MaxPromptTokens`
- `MaxPromptImageSize`, `MaxPromptImages`
- `SupportsToolCalls`, `SupportsStreaming`, `SupportsVision`, `SupportsReasoning`
- `ReasoningEfforts` (list of available reasoning effort levels)
- `SupportedEndpoints` (e.g. `["/chat/completions", "/responses"]`)

The Copilot API returns `capabilities.supports` as an **object** (not an
array). This package correctly parses the object shape, including
`vision` (bool), `adaptive_thinking`, `reasoning_effort` (string array),
`max_thinking_budget`, and vision limits (media types, max images/size).
