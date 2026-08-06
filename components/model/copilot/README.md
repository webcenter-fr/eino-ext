# copilot — GitHub Copilot chat model provider

`copilot` provides a `model.ToolCallingChatModel` backed by the GitHub Copilot
API. It implements `model.ToolCallingChatModel` and `model.ChatModel`.

This package makes direct HTTP calls using `net/http` — it does not depend on
any OpenAI SDK or ACL library.

## Plan-correct API host resolution

The Copilot API base URL depends on the Copilot plan tier tied to the GitHub
token:

| Plan | API host (from `endpoints.api` in exchange response) |
|------|------------------------------------------------------|
| Individual | `https://api.individual.githubcopilot.com` |
| Business | `https://api.business.githubcopilot.com` |
| Enterprise | `https://api.enterprise.githubcopilot.com` (or custom slug) |
| Free | `https://api.individual.githubcopilot.com` (direct-bearer; same host as Individual) |

Free-tier reuses the Individual host; `DetectPlan` reports `PlanIndividual` for
both. Free-tier is authenticated via a fine-grained PAT used directly as the
bearer token (no exchange).

When using `GitHubToken` with no `BaseURL`, `NewCopilotChatModel` now
**auto-detects** the plan-correct host from the token exchange response's
`endpoints.api` field (for classic PATs) or resolves the host via `ResolveBaseURL`
(for fine-grained PATs in direct-bearer mode). The hardcoded `ResolveBaseURL`
default (`api.individual.githubcopilot.com`) is only a fallback and only works for
**individual**-plan tokens; business/enterprise-plan tokens always hit 421
against it.

Precedence for `GitHubToken`:
1. Explicit `cfg.BaseURL` (always wins).
2. `endpoints.api` from the exchange response (classic PATs, auto-detected, plan-correct).
3. `ResolveBaseURL(cfg.EnterpriseURL)` fallback (direct-bearer + exchange fallback).

For `CopilotToken` (pre-obtained bearer token): no exchange happens, so
auto-detection is unavailable. Callers MUST set `cfg.BaseURL` explicitly for
business/enterprise-plan tokens, or the individual-only default is used.

## ResolveCopilotToken (one-off token resolution helper)

`ResolveCopilotToken` resolves a raw GitHub token and returns the plan-correct
`BaseURL` without constructing a full `CopilotModel` — useful for pre-flight
`ListModels` checks and other one-off API calls.

For fine-grained PATs (`github_pat_...`), `ResolveCopilotToken` returns the PAT
itself as `Token` (direct-bearer mode) with `ExpiresAt == 0` and
`Kind == TokenKindFineGrainedPAT`. The PAT is validated once via
`GET /copilot_internal/user` (best-effort; 401/403 fail fast). For classic PATs,
it returns the exchanged Copilot token as before.

```go
resolved, err := copilot.ResolveCopilotToken(ctx, githubToken, enterpriseURL, "", 15*time.Second)
if err != nil {
    log.Fatalf("token resolution failed: %v", err)
}
models, err := copilot.ListModels(ctx, resolved.Token, resolved.BaseURL, 30*time.Second)
```

Pass a non-empty `baseURL` (4th argument) to override: the explicit value
always wins over the default. See the function doc comment for the full
precedence rules.

## Auth modes

The component supports three authentication modes:

| Mode | Config field | Description |
|------|-------------|-------------|
| Direct token | `CopilotToken` | Pre-obtained Copilot OAuth bearer token (`gho_...`). Used directly, no refresh. |
| GitHub PAT (fine-grained, free-tier) | `GitHubToken` (`github_pat_...`) | Used **directly as the bearer token** (direct-bearer mode). No exchange, no refresh. Requires "Copilot Requests" account permission (Read) on a user-owned token. |
| GitHub PAT (classic, paid) | `GitHubToken` (`ghp_...`) | Exchanged at `/copilot_internal/v2/token` for a short-lived Copilot token. Background refresh enabled. |
| Model session | `SessionToken` | Pre-obtained Copilot model session token (JWT). |

The provider auto-detects the token flavor by prefix (`github_pat_` → direct-bearer,
`ghp_` → exchange, `gho_` → CopilotToken). Unknown prefixes default to exchange
(backward compatible).

At least one of `GitHubToken` or `CopilotToken` must be set.

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
| `COPILOT_FREE_TIER` | Set to `1` to run the free-tier integration suite (requires `GITHUB_TOKEN=github_pat_...`) |
| `COPILOT_API_URL` | Override base URL for debugging (default: `https://api.individual.githubcopilot.com`) |

## Free-tier Copilot support

Free-tier accounts authenticate with a **fine-grained GitHub PAT**
(`github_pat_...`) that is **user-owned** and has the **"Copilot Requests"
account permission (Read)**. The PAT is used **directly as
`Authorization: Bearer <PAT>`** against
`https://api.individual.githubcopilot.com`. There is **no token exchange** —
the `/copilot_internal/v2/token` endpoint returns
`403 Resource not accessible by personal access token` for fine-grained PATs,
so the provider detects the `github_pat_` prefix and routes to
**direct-bearer mode** (no exchange, no background refresh).

The provider validates the PAT once via `GET /copilot_internal/user` (Bearer PAT)
at construction time for early, clear errors. Validation is best-effort: 401/403
fail fast, transient errors do not block construction.

Free-tier has a reduced model catalog and a monthly quota; `402`/`403` on
chat/models means quota exhausted (the PAT validation still succeeds).

### Generating a GitHub PAT for free-tier

1. **Ensure Copilot is enabled** (Settings → Copilot; free tier active).
2. **Fine-grained PAT** (required for free-tier):
   - GitHub → Settings → Developer settings → Fine-grained personal access tokens → Generate new token.
   - **Resource owner: your personal account** (user-owned; the Copilot Requests permission is only available on user-owned fine-grained tokens, not organization-owned).
   - Repository access: not required for Copilot.
   - Account permissions: **`Copilot Requests` → Read**. No other permissions needed.
   - Token format: `github_pat_...`.
3. **Classic `ghp_` PATs** are for the **paid-plan exchange path** (Pro/Pro+/Business/Enterprise). They are **not** used for free-tier (free-tier uses direct-bearer with a fine-grained PAT). If you have a paid plan and a classic PAT, the provider exchanges it at `/copilot_internal/v2/token` (existing behavior).
4. **`gho_` tokens** (Copilot OAuth) are used directly as the bearer token (no exchange); pass them in `CopilotToken` (or `GitHubToken` — the provider auto-promotes them).
5. **Use it**:
   ```go
   m, err := copilot.NewCopilotChatModel(ctx, &copilot.Config{
        GitHubToken: os.Getenv("GITHUB_TOKEN"), // github_pat_...
        Model:       "gpt-4o",
   })
   ```
   Or via env var:
   ```bash
   export GITHUB_TOKEN=github_pat_...
   ```
   ```go
   m, err := copilot.NewCopilotChatModel(ctx, &copilot.Config{Model: "gpt-4o"})
   ```

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
// With EnterpriseURL set, the auto-detected endpoints.api takes
// precedence over the ResolveBaseURL(enterpriseURL) fallback. An
// explicit BaseURL still wins.
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
| `GitHubToken` | `string` | Fine-grained GitHub PAT (`github_pat_...`) with Copilot Requests permission |
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
# Paid/classic suite (unchanged; requires ghp_ or gho_):
COPILOT_INTEGRATION=1 GITHUB_COPILOT_TOKEN=gho_... \
  go test -tags=integration -run 'TestIntegration' ./components/model/copilot/...

# Free-tier acceptance suite (fine-grained PAT, direct-bearer):
COPILOT_INTEGRATION=1 COPILOT_FREE_TIER=1 GITHUB_TOKEN=github_pat_... \
  go test -tags=integration -run 'TestIntegration_FreeTier' \
  ./components/model/copilot/...

# Free-tier + shared ListModels test (adaptive):
COPILOT_INTEGRATION=1 COPILOT_FREE_TIER=1 GITHUB_TOKEN=github_pat_... \
  go test -tags=integration -run 'TestIntegration_FreeTier|TestIntegration_ListModels' \
  ./components/model/copilot/...

# Run with a logging proxy to debug:
COPILOT_INTEGRATION=1 GITHUB_COPILOT_TOKEN=gho_... COPILOT_API_URL=http://127.0.0.1:8080 \
  go test -tags=integration -run 'TestIntegration' ./components/model/copilot/...
```

Tests cover:
- Model listing (≥20 models with GPT-5 and Claude for paid; ≥1 for free-tier)
- Temperature matrix across model families
- Reasoning effort values
- Streaming for reasoning and standard models
- Endpoint routing (gpt-5.4-nano → /responses, gpt-4.1 → /chat/completions)
- Disabled model handling (skipped until enablement lands)
- Auto-detect base URL acceptance tests (require `GITHUB_TOKEN`; skipped for `github_pat_`)
- Direct-bearer free-tier tests (require `COPILOT_FREE_TIER=1` + `github_pat_` token)

**Note:** Free-tier tests require a fine-grained PAT (`github_pat_...`). The classic
acceptance tests (`TestIntegration_AutoDetectBaseURL`, `TestIntegration_ResolveCopilotToken`,
`TestIntegration_Check_GitHubToken`) require a `ghp_`/exchangeable token and are skipped
for `github_pat_` tokens.

### Acceptance tests (real API, requires exchangeable GitHub PAT)

```bash
COPILOT_INTEGRATION=1 GITHUB_TOKEN=ghp_... \
  go test -tags=integration -run 'TestIntegration_AutoDetectBaseURL|TestIntegration_ResolveCopilotToken|TestIntegration_Check_GitHubToken' \
  ./components/model/copilot/...
```

These prove the plan-correct host auto-detection against the real Copilot API.
Do not set `COPILOT_API_URL` or `GITHUB_COPILOT_TOKEN` — they override
auto-detection and mask the bug.

## Connectivity checkup

The `Check` function probes the Copilot API:

```go
results := copilot.Check(ctx, cfg)
fmt.Println(results.JSON("  "))
```

It verifies:
- Token exchange (when using `GitHubToken`; reports direct-bearer for fine-grained PATs)
- `/models` endpoint reachability with full catalog
- Model availability (distinguishes "no models" as `"limited"` vs "error")

When using `GitHubToken` without `BaseURL`, `Check` auto-detects the
plan-correct host from `endpoints.api` (same precedence as `NewCopilotChatModel`),
or uses `ResolveBaseURL` fallback for direct-bearer mode.

## Embeddings

`NewEmbedder` requires an explicit `baseURL`. It does not auto-detect the
plan-correct host from a token exchange — the library caller is responsible
for obtaining the correct host via `ResolveCopilotToken` (when using a raw
GitHub PAT) or by using `ResolveBaseURL("")` when on the individual plan:

```go
// Individual plan
e, _ := copilot.NewEmbedder(ctx, cfg, token, copilot.ResolveBaseURL(""), timeout)

// Business/enterprise plan with raw GitHub PAT
resolved, _ := copilot.ResolveCopilotToken(ctx, ghToken, "", "", timeout)
e, _ := copilot.NewEmbedder(ctx, cfg, resolved.Token, resolved.BaseURL, timeout)

// Free-tier (fine-grained PAT): pass the PAT as copilotToken.
// ResolveCopilotToken returns the PAT as Token in direct-bearer mode.
resolved, _ := copilot.ResolveCopilotToken(ctx, pat, "", "", timeout)
e, _ := copilot.NewEmbedder(ctx, cfg, resolved.Token, resolved.BaseURL, timeout)
```

Passing an empty `baseURL` returns an error.

## Troubleshooting

### `421 Misdirected Request` on any Copilot endpoint

This error means the wrong plan-specific API host is being used. Common causes:

- Using `CopilotToken` (pre-obtained bearer token) without setting `BaseURL`
  explicitly for a business/enterprise plan — no exchange happens, so
  auto-detection is unavailable.
- Using the library's default from a version before the `endpoints.api`
  auto-detection fix.
- A proxy/gateway in front of the Copilot API that rewrites the host header.

Diagnose: run a manual token exchange and check the `endpoints.api` field:

```bash
curl https://api.github.com/copilot_internal/v2/token \
  -H "Authorization: token <github_pat_...>" \
  -H "User-Agent: copilot/1.0.70 (client/github/cli linux v24.16.0) term/unknown" \
  -H "Accept: application/json" | jq .endpoints.api
```

If the returned host differs from `api.individual.githubcopilot.com`, you
must either:
- Switch to `GitHubToken` (raw PAT) so `NewCopilotChatModel` auto-detects the
  plan-correct host from the exchange response; or
- Set `BaseURL` explicitly to the value from `endpoints.api`.

### `403 Resource not accessible by personal access token` on token exchange

This means a fine-grained PAT (`github_pat_...`) was sent to
`/copilot_internal/v2/token`. Fine-grained PATs are **not exchanged** — the
provider uses them directly as the bearer token (direct-bearer mode). If you
see this error, ensure you are using a recent version of the provider that
detects the `github_pat_` prefix. For the exchange path (paid plans), use a
classic `ghp_` PAT or a `gho_` OAuth token.

### `401`/`403` from `validateFineGrainedPAT` (construction time)

The fine-grained PAT is invalid/expired (401) or lacks the "Copilot Requests"
account permission / the account has no Copilot (403). Regenerate the PAT
with the Copilot Requests permission (Read) on a user-owned token.

### `402 Payment Required` or `403` with a quota message on chat/models

Free-tier monthly quota exhausted. Wait for the quota reset (monthly) or upgrade
to Copilot Pro/Pro+. The PAT validation itself still succeeds; only chat/models
calls return 402/403. Diagnose with `copilot.Check`.

### PAT rotation

Direct-bearer mode does not auto-refresh the PAT (it is long-lived). To rotate,
generate a new PAT and recreate the `CopilotModel`.

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
