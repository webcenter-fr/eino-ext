# Plan: Fix GitHub Copilot provider — deep structural comparison with kilocode

> Written: 2026-07-10
> Scope: `components/model/copilot/` only.

## Executive summary

A line-by-line comparison of eino-ext's copilot provider against kilocode
(v2 protocol layer) and copilot-api reveals **9 concrete structural
differences**. Of these, 5 are bugs that directly cause the user's
reported failures (model switching, thinking-level switching, smallModel
switching). The remaining 4 are missing features or stale comments.

## Methodology

Compared against two reference implementations:
- **kilocode** `packages/llm/` — the user's "works very well" reference
- **copilot-api** `src/` — the user's "good working" reference

Read every struct field, every header, every JSON tag, every endpoint URL,
every option resolution path across all three codebases.

---

## COMPARISON TABLE

### A. Headers

| Header | kilocode | copilot-api | eino-ext | Bug? |
|--------|----------|-------------|----------|------|
| `Authorization` | `Bearer <token>` | `Bearer <token>` | `Bearer <token>` | No |
| `Content-Type` | `application/json` (from `jsonPost`) | `application/json` | `application/json` | No |
| `Accept` | `text/event-stream` (streaming) | — | `text/event-stream` (streaming) | No |
| `User-Agent` | not sent | `GitHubCopilotChat/0.26.7` | `GitHubCopilotChat/0.52.0` | No |
| `copilot-integration-id` | not sent | `vscode-chat` | **intentionally omitted** | No |
| `editor-version` | not sent | `vscode/<version>` | **intentionally omitted** | No |
| `editor-plugin-version` | not sent | `copilot-chat/0.26.7` | not sent | No |
| `openai-intent` | not sent | `conversation-panel` | `conversation-edits` | No |
| `x-github-api-version` | not sent | `2025-04-01` | not sent (only on token exchange) | No |
| `x-request-id` | not sent | `<uuid>` | not sent | No |
| `x-vscode-user-agent-library-version` | not sent | `electron-fetch` | not sent | No |
| `x-initiator` | not sent | `agent` or `user` | `agent` or `user` | No |
| `Copilot-Vision-Request` | not sent | `true` (if vision) | `true` (if vision) | No |

**Conclusion**: Header differences are NOT the cause. Kilocode works perfectly
with MINIMAL headers (only bearer token). The intentionally-omitted
integration-id in eino-ext is correct — forcing vscode-chat restricts the
model allowlist against the token's OAuth grant.

### B. Chat Completions `/chat/completions` request body

| Field | kilocode schema | copilot-api sends | eino-ext struct | Bug? |
|-------|----------------|-------------------|-----------------|------|
| `model` | ✓ String | ✓ | ✓ String | No |
| `messages` | ✓ Array | ✓ | ✓ Array | No |
| `stream` | ALWAYS `true` | ✓ | ✓ Bool | No |
| `stream_options` | ALWAYS `{include_usage:true}` | sent | Conditional (when `stream==true`) | No |
| `max_tokens` | ✓ optional | ✓ | ✓ `*int` | No |
| `temperature` | ✓ optional | ✓ | ✓ `*float32` | No |
| `top_p` | ✓ optional | ✓ | ✓ `*float32` | No |
| `stop` | ✓ optional array | ✓ | ✓ `[]string` | No |
| `tool_choice` | ✓ auto/none/required/{type,function:{name}} | ✓ | ✓ same format | No |
| `tools` | ✓ array | ✓ | ✓ array | No |
| `reasoning_effort` | ✓ optional | sent | ✓ optional | No |
| `store` | ✓ optional bool | not sent | **MISSING** | Bug #1 |
| `frequency_penalty` | ✓ optional | ✓ | **MISSING** | Bug #2 |
| `presence_penalty` | ✓ optional | ✓ | **MISSING** | Bug #3 |
| `seed` | ✓ optional | ✓ | **MISSING** | Bug #4 |

**Bug #1-4**: eino-ext's `copilotChatRequest` struct is missing 4 fields that
both kilocode and copilot-api send. When the eino framework passes these via
option helpers, they are silently dropped because the JSON serialization of
`copilotChatRequest` has no field to hold them. This directly breaks callers
that rely on `frequency_penalty` or `seed` for determinism.

### C. Responses API `/responses` request body

| Field | kilocode schema | eino-ext struct | Bug? |
|-------|----------------|-----------------|------|
| `model` | ✓ String | ✓ String | No |
| `input` | ✓ Array | ✓ Array | No |
| `stream` | ALWAYS `true` | ✓ Bool | No |
| `max_output_tokens` | ✓ optional | ✓ `*int` | No |
| `temperature` | ✓ optional | **INTENTIONALLY OMITTED** | Bug #5 |
| `top_p` | ✓ optional | **INTENTIONALLY OMITTED** | Bug #6 |
| `tools` | ✓ array | ✓ array | No |
| `tool_choice` | ✓ **flat format** `{type:"function",name:"x"}` | **nested format** `{type:"function",function:{name:"x"}}` | Bug #7 |
| `store` | ✓ optional | ✓ (omitempty) | No |
| `prompt_cache_key` | ✓ optional | **MISSING** | Feature gap |
| `include` | ✓ optional | ✓ when reasoning effort set | No |
| `reasoning` | ✓ `{effort?, summary?}` | ✓ when reasoning effort set | No |
| `text` | ✓ `{verbosity?}` | **MISSING** | Feature gap |
| `instructions` | ✓ optional | **MISSING** | Feature gap |

**Bug #5-6**: `buildResponsesRequest` at `copilot_responses.go:326-329` says
`temperature/top_p are intentionally omitted. The /responses endpoint is used
only for GPT-5+ reasoning models, which reject these parameters` — but this is
**factually wrong** about kilocode. Kilocode's `openai-responses.ts:428-441`
EXPLICITLY includes `temperature: generation?.temperature` and `top_p:
generation?.topP`. The comment claims to mirror kilocode but does the
opposite.

**Bug #7 (Responses tool_choice format)**: The Responses API uses a flat tool
choice format `{type: "function", name: "tool_name"}` (kilocode
`openai-responses.ts:113-116`), while eino-ext reuses `convertToolChoice`
which produces the Chat API's nested format `{type: "function", function:
{name: "tool_name"}}`. Additionally, `buildResponsesRequest` passes `nil` for
`allowedToolNames`, so `ToolChoiceForced` always falls through to `"required"`.

### D. Per-call option resolution

| Aspect | Chat path (`buildChatRequest`) | Responses path (`buildResponsesRequest`) | Bug? |
|--------|-------------------------------|----------------------------------------|------|
| Model | `model.GetCommonOptions(opts...)` → resolved ✓ | `m.resolveModel()` (no opts) → `m.cfg.Model` ✗ | Bug #8 |
| MaxTokens | `options.MaxTokens` ✓ | `m.cfg.MaxCompletionTokens` (no opts) ✗ | Bug #8 |
| Temperature | `options.Temperature` ✓ | omitted entirely ✗ | Bug #8 |
| Tools | `options.Tools` ✓ | `m.tools` (no opts) ✗ | Bug #8 |
| ToolChoice | `options.ToolChoice` + `AllowedToolNames` ✓ | `m.toolChoice` + `nil` ✗ | Bug #8 |
| ReasoningEffort | `CopilotOptions` from opts ✓ | `m.cfg.ReasoningEffort` (no opts) ✗ | Bug #8 |

**Bug #8 (ROOT CAUSE)**: `buildResponsesRequest` at `copilot_responses.go:316`
has `_ = opts // per-call options deferred to future iteration`. Every option
is read from `m.cfg` or `m.tools`. All per-call overrides are silently
dropped **only** for the /responses endpoint. The /chat/completions path
works correctly.

### E. Reasoning defaults for GPT-5

| Aspect | kilocode `gpt5DefaultOptions` | eino-ext | Bug? |
|--------|-------------------------------|----------|------|
| Default `reasoning.effort` | `"medium"` for all non-chat/non-pro gpt-5 models | only when `Config.ReasoningEffort != ""` | Bug #9 |
| Default `reasoning.summary` | `"auto"` | only when reasoning effort set | Bug #9 |
| Default `include` | `["reasoning.encrypted_content"]` | only when reasoning effort set | Bug #9 |
| Default `text.verbosity` | `"low"` for gpt-5.* (non-codex, non-chat) | not supported | Feature gap |

**Bug #9**: When `Config.ReasoningEffort` is `""` (user didn't set it),
eino-ext sends no reasoning configuration at all for GPT-5 models. Kilocode
sends `reasoning: {effort: "medium", summary: "auto"}`, `include:
["reasoning.encrypted_content"]`, and optionally `text: {verbosity: "low"}`
as defaults. This means GPT-5 models in eino-ext get different reasoning
behavior when reasoning effort is not explicitly configured.

### F. Chat request struct completeness

| Field | kilocode `bodyFields` | eino-ext `copilotChatRequest` | Missing? |
|-------|----------------------|------------------------------|----------|
| `model` | ✓ | ✓ `Model` | |
| `messages` | ✓ | ✓ `Messages` | |
| `temperature` | ✓ | ✓ `Temperature` | |
| `max_tokens` | ✓ | ✓ `MaxTokens` | |
| `reasoning_effort` | ✓ | ✓ `ReasoningEffort` | |
| `top_p` | ✓ | ✓ `TopP` | |
| `stop` | ✓ | ✓ `Stop` | |
| `stream` | ✓ | ✓ `Stream` | |
| `stream_options` | ✓ | ✓ `StreamOptions` | |
| `tools` | ✓ | ✓ `Tools` | |
| `tool_choice` | ✓ | ✓ `ToolChoice` | |
| `store` | ✓ | **MISSING** | Bug #1 |
| `frequency_penalty` | ✓ | **MISSING** | Bug #2 |
| `presence_penalty` | ✓ | **MISSING** | Bug #3 |
| `seed` | ✓ | **MISSING** | Bug #4 |

---

## ALL BUGS (ordered by priority)

### Bug #8 — CRITICAL: `buildResponsesRequest` ignores all per-call options

**File**: `copilot_responses.go:315-348`
**Impact**: Switching model, thinking level, max tokens, or tools via
`model.WithModel()`, `CopilotOptions{}`, `model.WithMaxTokens()`, etc. is
silently ignored for GPT-5+ models using /responses. The routing decision
(`useResponsesAPI`) correctly resolves the per-call model, so the request
goes to /responses, but the body carries `m.cfg.Model` instead.

### Bug #7 — MEDIUM: Responses tool_choice uses wrong format

**File**: `copilot_responses.go:339` (calls `convertToolChoice(m.toolChoice, nil)`)
**Impact**: Responses API tool choice uses the Chat API's nested format
`{type: "function", function: {name: "x"}}` instead of the Responses API's
flat format `{type: "function", name: "x"}`. Also `nil` allowedToolNames
means forced single-tool choice degrades to `"required"`.

### Bug #5-6 — MEDIUM: Temperature/top_p incorrectly omitted from Responses API

**File**: `copilot_responses.go:326-329`
**Impact**: Comment claims kilocode omits these — but kilocode includes them.
If a caller passes `model.WithTemperature(0.5)` for a GPT-5 model, it's
silently dropped. The Copilot API may now accept these params.

### Bug #9 — MEDIUM: No default reasoning configuration for GPT-5 models

**File**: `copilot_responses.go:338-345`
**Impact**: When `Config.ReasoningEffort` is empty, GPT-5 models get no
reasoning config. Kilocode defaults to `effort: "medium"`, `summary: "auto"`,
`include: ["reasoning.encrypted_content"]` for all non-chat/non-pro GPT-5
models.

### Bug #1-4 — LOW: Missing chat request fields

**File**: `copilot_chat.go:94-106` (`copilotChatRequest` struct)
**Impact**: `store`, `frequency_penalty`, `presence_penalty`, `seed` are
missing from the request struct. Callers relying on these eino options will
see them silently dropped.

---

## PROPOSED FIXES

### Fix 1: Rewrite `buildResponsesRequest` to resolve per-call options

**File**: `copilot_responses.go`

Replace the entire function body to mirror `buildChatRequest`'s option
resolution:

```go
func (m *CopilotModel) buildResponsesRequest(in []*schema.Message, opts ...model.Option) (responsesRequest, error) {
    options := model.GetCommonOptions(&model.Options{
        MaxTokens:  m.cfg.MaxCompletionTokens,
        Model:      &m.cfg.Model,
        Tools:      m.tools,
        ToolChoice: m.toolChoice,
        Temperature: m.cfg.Temperature,
        TopP:       nil, // will be set from opts if provided
    }, opts...)

    effort := m.cfg.ReasoningEffort
    if copilotOpts := model.GetImplSpecificOptions[CopilotOptions](nil, opts...); copilotOpts != nil && copilotOpts.ReasoningEffort != "" {
        effort = copilotOpts.ReasoningEffort
    }

    resolvedModel := ""
    if options.Model != nil {
        resolvedModel = *options.Model
    }
    if resolvedModel == "" {
        return responsesRequest{}, errors.New("copilot: model must not be empty; set Config.Model or pass model.WithModel()")
    }

    req := responsesRequest{
        Model:           resolvedModel,
        Input:           convertToResponsesInput(in),
        MaxOutputTokens: options.MaxTokens,
        Temperature:     options.Temperature,
        TopP:            options.TopP,
    }

    // Add default reasoning config for GPT-5 non-chat/non-pro models
    // (backported from kilocode gpt5DefaultOptions)
    if shouldSetReasoningDefaults(resolvedModel, effort) {
        req.Include = []string{"reasoning.encrypted_content"}
        eff := string(ReasoningEffortMedium)
        if effort != "" {
            eff = string(effort)
        }
        req.Reasoning = &responsesReasoning{Effort: eff, Summary: "auto"}
    } else if effort != "" {
        req.Include = []string{"reasoning.encrypted_content"}
        req.Reasoning = &responsesReasoning{Effort: string(effort), Summary: "auto"}
    }

    if len(options.Tools) > 0 {
        req.Tools = convertResponsesTools(options.Tools)
        req.ToolChoice = convertResponsesToolChoice(options.ToolChoice, options.AllowedToolNames)
    }

    return req, nil
}
```

### Fix 2: Add Responses-specific `convertResponsesToolChoice`

**File**: `copilot_responses.go`

The Responses API uses a flat tool choice format. Add a dedicated function:

```go
func convertResponsesToolChoice(tc *schema.ToolChoice, allowedToolNames []string) any {
    if tc == nil {
        return nil
    }
    switch *tc {
    case schema.ToolChoiceForbidden:
        return "none"
    case schema.ToolChoiceAllowed:
        return "auto"
    case schema.ToolChoiceForced:
        if len(allowedToolNames) == 1 {
            return map[string]string{
                "type": "function",
                "name": allowedToolNames[0],
            }
        }
        return "required"
    default:
        return "auto"
    }
}
```

### Fix 3: Add `shouldSetReasoningDefaults` helper

**File**: `copilot_responses.go`

Backport from kilocode `gpt5DefaultOptions`:

```go
func shouldSetReasoningDefaults(modelID string, configuredEffort ReasoningEffort) bool {
    id := strings.ToLower(modelID)
    return strings.Contains(id, "gpt-5") &&
        !strings.Contains(id, "gpt-5-chat") &&
        !strings.Contains(id, "gpt-5-pro")
}
```

When this returns `true`, apply default `effort: "medium"`, `summary:
"auto"`, `include: ["reasoning.encrypted_content"]`. The configured effort
overrides the default.

### Fix 4: Add missing fields to `copilotChatRequest`

**File**: `copilot_chat.go`

Add to the struct and wire in `buildChatRequest` from
`model.GetCommonOptions`:

```go
type copilotChatRequest struct {
    // ... existing fields ...
    Store            *bool                `json:"store,omitempty"`
    FrequencyPenalty *float32             `json:"frequency_penalty,omitempty"`
    PresencePenalty  *float32             `json:"presence_penalty,omitempty"`
    Seed             *int                 `json:"seed,omitempty"`
}
```

### Fix 5: Align `wouldUseResponses` with kilocode

**File**: `copilot_chat.go` (lines 726-734)

Remove the `gpt-5.4-mini` and `gpt-5.4-nano` exclusion blocks. Update the
doc comment.

### Fix 6: Add tests for all fixes

**File**: `copilot_responses_test.go`

1. `TestResponsesWithModelOverride` — model.WithModel("gpt-5") overrides cfg.Model="gpt-4o"
2. `TestResponsesWithReasoningEffortOverride` — CopilotOptions{ReasoningEffort:"high"} overrides cfg.ReasoningEffort="low"
3. `TestResponsesWithMaxTokensOverride` — model.WithMaxTokens(500) overrides cfg.MaxCompletionTokens=1000
4. `TestResponsesWithTemperatureOverride` — model.WithTemperature(0.5) is present in body
5. `TestResponsesWithToolChoiceFormat` — flattened tool choice for single tool
6. `TestResponsesWithDefaultReasoning` — no config reasoning → default medium effort for gpt-5
7. `TestResponsesStreamingWithModelOverride` — model override on Stream()

**File**: `copilot_test.go`

8. Update `TestWouldUseResponses`/`TestUseResponsesAPI` — remove 5.4-mini/nano exclusion expectations
9. `TestBuildChatRequestHasMissingFields` — test that store/seed/etc. are populated from options

---

## Validation

```bash
cd /projects/eino-ext \
  && go build ./components/model/copilot/... \
  && go vet ./components/model/copilot/... \
  && go test ./components/model/copilot/... -v -count=1
```

All tests use mock servers only. No live network dependency.

---

## Files modified

1. `copilot_responses.go` — `buildResponsesRequest` rewrite, `convertResponsesToolChoice`, `shouldSetReasoningDefaults`
2. `copilot_chat.go` — add missing fields to `copilotChatRequest`, wire in `buildChatRequest`, remove 5.4-mini/nano exclusions from `wouldUseResponses`
3. `copilot_responses_test.go` — 7 new tests
4. `copilot_test.go` — updated exclusion tests, new struct field test
