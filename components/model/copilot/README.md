# copilot — GitHub Copilot chat model provider

`copilot` provides a `model.ToolCallingChatModel` backed by the GitHub Copilot
API (`https://api.individual.githubcopilot.com`). It implements `model.ToolCallingChatModel`
and `model.ChatModel`.

This package makes direct HTTP calls using `net/http` — it does not depend on
any OpenAI SDK or ACL library.

## Auth modes

The component supports three authentication modes:

| Mode | Config field | Description |
|------|-------------|-------------|
| Direct token | `CopilotToken` | Pre-obtained Copilot OAuth bearer token. Used directly with no refresh. |
| GitHub PAT | `GitHubToken` | GitHub PAT with `read:user` scope. Auto-exchanged for a Copilot token via `/copilot_internal/v2/token`. Supports background refresh. |
| Model session | `SessionToken` | Pre-obtained Copilot model session token (JWT). Used directly for premium models. |

At least one of `GitHubToken` or `CopilotToken` must be set.

Model session tokens are acquired automatically for premium models (GPT-5,
Claude, Gemini) via `POST /models/session`. For tests or offline use, a
pre-obtained session token can be passed in `Config.SessionToken`.

## Environment variables

If config fields are empty, the constructor falls back to environment variables:

| Env var | Config field |
|---------|-------------|
| `GITHUB_COPILOT_TOKEN` | `CopilotToken` |
| `GITHUB_TOKEN` | `GitHubToken` |
| `GITHUB_COPILOT_ENTERPRISE_URL` | `EnterpriseURL` |

For integration tests:

| Env var | Purpose |
|---------|---------|
| `COPILOT_INTEGRATION` | Set to `1` to enable integration tests |
| `COPILOT_API_URL` | Override base URL for debugging (default: `https://api.individual.githubcopilot.com`) |

## Request headers

Every request to the Copilot API includes these headers:

| Header | Value | Purpose |
|--------|-------|---------|
| `Authorization` | `Bearer {token}` | OAuth authentication |
| `Copilot-Integration-Id` | `copilot-developer-cli` | Required for full model catalog (32 models) |
| `Copilot-Session-Token` | JWT from `/models/session` | Required for premium models (GPT-5, Claude, Gemini) |
| `User-Agent` | `copilot/1.0.70 ...` | Required by Copilot API |
| `Editor-Version` | `copilot/1.0.70` | Required by Copilot API |
| `Openai-Intent` | `conversation-agent` | Required by Copilot API |
| `X-GitHub-Api-Version` | `2026-07-01` | Required by Copilot API |
| `X-Initiator` | `user` or `agent` | Per-request: `user` for plain text prompts, `agent` for tool/assistant follow-ups |
| `X-Interaction-Id` | UUID | Per-turn unique identifier |
| `X-Client-Machine-Id` | UUID | Stable per model instance |
| `X-Copilot-Vision-Request` | `true` | Only sent when a message contains image parts |

## Model session token flow

Premium models (GPT-5, Claude, Gemini) require a **model session token**. The
flow is:

1. `POST /models/session` with body `{"auto_mode":{"model_hints":["<modelId>"]}}`
2. Response: `{session_token (JWT), expires_at, selected_model, available_models}`
3. Send `Copilot-Session-Token: <jwt>` on every chat/responses/messages call
4. Refresh ~60s before `expires_at`; re-acquire on 401/`mc_session_token_expired`

The session token is acquired automatically on the first call for a premium
model. Set `Config.SessionToken` to provide a pre-obtained token.

## Temperature rules

| Model family | Temperature support | Notes |
|---|---|---|
| GPT-5 (`gpt-5-mini`, `gpt-5.4-nano`, …) | **Unsupported** | Any value other than omitted returns 400. Omitted automatically. |
| Standard OpenAI (`gpt-4.1`, `gpt-4o`, `gpt-3.5-turbo`) | 0.0 – 2.0 | Full range supported. |
| Claude (`claude-haiku-4.5`, …) | 0.0 – 1.0 | Values >1 clamp to 1.0. |
| Gemini | 0.0 – 1.0 | (after enablement) |

## Endpoint routing

Models are routed to the appropriate endpoint based on their catalog:

| Condition | Endpoint |
|---|---|
| GPT-5-class models with `/responses` in `supported_endpoints` | `/responses` (with `input` body) |
| Models with `/chat/completions` in `supported_endpoints` | `/chat/completions` (with `messages` body) |
| `gpt-5.4-nano`, `gpt-5.4-mini`, `gpt-5.5` | `/responses` (only endpoint listed) |

`ForceChatCompletions` overrides routing but rejects models whose catalog
lacks `/chat/completions`.

## Reasoning effort

Available values: `none`, `minimal`, `low`, `medium`, `high`, `xhigh`, `max`.

Each model supports a subset; validate against the model's catalog
`reasoning_effort` list. Omit the field when the model has none.

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
| `CopilotToken` | `string` | Pre-obtained Copilot OAuth bearer token |
| `SessionToken` | `string` | Pre-obtained Copilot model session token (JWT) |
| `EnterpriseURL` | `string` | GitHub Enterprise domain |
| `BaseURL` | `string` | Override Copilot API base URL |
| `Timeout` | `time.Duration` | API request timeout (≥1s, default 60m) |
| `TLSSkipVerify` | `bool` | Skip TLS certificate verification |
| `Model` | `string` | Model ID to use (required at call time; can be overridden via `model.WithModel`) |
| `Temperature` | `*float32` | Sampling temperature (0 to 2 for standard models; omitted for reasoning models) |
| `MaxCompletionTokens` | `*int` | Upper bound on generated tokens (sent as `max_tokens` for chat, `max_output_tokens` for responses) |
| `ReasoningEffort` | `ReasoningEffort` | Reasoning effort: `none`, `minimal`, `low`, `medium`, `high`, `xhigh`, `max` |
| `ForceChatCompletions` | `bool` | Force `/chat/completions` endpoint even for models that would use `/responses` |
| `FrequencyPenalty` | `*float32` | Frequency penalty (-2 to 2) |
| `PresencePenalty` | `*float32` | Presence penalty (-2 to 2) |
| `Seed` | `*int` | Deterministic sampling seed |
| `Store` | `*bool` | Store conversation for later use |

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

The `X-Copilot-Vision-Request: true` header is sent automatically when any
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
call via `model.WrapImplSpecificOptFn`:

```go
msg, err := m.Generate(ctx, messages,
    model.WithMaxTokens(100),
    model.WrapImplSpecificOptFn(func(o *copilot.CopilotOptions) {
        o.ReasoningEffort = copilot.ReasoningEffortHigh
    }),
)
```

## Integration tests

The package includes a real-API integration test suite with build tag
`integration`:

```bash
COPILOT_INTEGRATION=1 GITHUB_COPILOT_TOKEN=gho_... \
  go test -tags=integration -run 'TestIntegration' ./components/model/copilot/...

# Run with a logging proxy to debug:
COPILOT_INTEGRATION=1 GITHUB_COPILOT_TOKEN=gho_... COPILOT_API_URL=http://127.0.0.1:8080 \
  go test -tags=integration -run 'TestIntegration' ./components/model/copilot/...
```

Tests cover:
- Model listing (≥20 models with GPT-5 and Claude)
- Temperature matrix across model families
- Reasoning effort values
- Streaming for reasoning and standard models
- Endpoint routing (gpt-5.4-nano → /responses, gpt-4.1 → /chat/completions)
- Disabled model handling (skipped until enablement lands)

## Connectivity checkup

The `Check` function probes the Copilot API:

```go
results := copilot.Check(ctx, cfg)
fmt.Println(results.JSON("  "))
```

It verifies:
- Token exchange (when using `GitHubToken`)
- `/models` endpoint reachability with full catalog
- Model availability (distinguishes "no models" as `"limited"` vs "error")

## Model discovery

`ListModels` fetches available models from `GET /models` with the
`Copilot-Integration-Id` header to return the full 32-model catalog:

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
- `State` (`"enabled"` or `"disabled"`)
- `ModelPickerEnabled` (whether the model in the picker)

All models are returned (including disabled and non-picker models). Filter
by `State` or `ModelPickerEnabled` in your own code as needed.

The Copilot API returns `capabilities.supports` as an **object** (not an
array). The `adaptive_thinking` field can be a boolean or the string
`"unsupported"` — this package handles both.
