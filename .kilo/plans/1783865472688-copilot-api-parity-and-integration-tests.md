# Copilot API Parity + Real-API Integration Test Suite

Plan for `components/model/copilot`. Produced from a deep inspection of the
official GitHub Copilot CLI v1.0.70 (Node SEA binary + Rust `runtime.node`
addon), the CLI's `--log-level all` debug output, the embedded JS source, and
**direct verification against the live API** (`api.individual.githubcopilot.com`)
using the configured Copilot OAuth token. Every fact below was confirmed with a
real HTTP call (prompt `"Hi"`).

---

## 0. Why the current client fails (root causes, all confirmed)

| # | Bug | Evidence |
|---|-----|----------|
| 1 | **No `Copilot-Integration-Id` header** (deliberately omitted) | `GET /models` w/o it → 7 legacy models; with `copilot-developer-cli` → 32 models. Premium models return `model_not_supported` without it. |
| 2 | **No model-session (`Copilot-Session-Token`) flow** | `POST /chat/completions` for `gpt-5-mini`/`claude-haiku-4.5` with bearer-only → `model_not_supported`; with a session token → `200`. **This is the universal unlock for premium models.** |
| 3 | **Wrong `Openai-Intent` value** (`conversation-edits`) | Official CLI sends `openai-intent: conversation-agent` (confirmed in raw capture + runtime strings). |
| 4 | **Wrong base URL** (`api.githubcopilot.com`) | Official CLI uses `https://api.individual.githubcopilot.com`. |
| 5 | **Wrong User-Agent** (`GitHubCopilotChat/0.52.0`) | Official sends `copilot/1.0.70 (client/github/cli linux v24.16.0) term/unknown`. |
| 6 | **Wrong/no `editor-version` header** (README claims `Editor-Version`/`Editor-Plugin-Version`) | Official sends a single header `editor-version: copilot/1.0.70` (lowercase). No `Editor-Plugin-Version` exists. |
| 7 | **Wrong routing for `gpt-5.4-mini`/`gpt-5.4-nano`** → forced to `/chat/completions` | Catalog: these models support **only** `/responses` (+`ws:/responses`). `/chat/completions` rejects them. `gpt-5.4-nano` on `/responses` + session token → `200`. |
| 8 | **Sends `temperature` on reasoning models** | `gpt-5-mini` + any `temperature` ≠ omitted/`1.0` → `400 "Unsupported parameter: 'temperature' is not supported with this model"`. |
| 9 | **Reasoning effort set is incomplete** (`low/medium/high`) | Catalog/CLI expose `none, minimal, low, medium, high, xhigh, max` (per-model subsets). |
| 10 | **`ListModels` skips `model_picker_enabled=false`** | All GPT-5/Claude/Gemini models have `model_picker_enabled=false`; skipping hides them. Official CLI lists all 32. |
| 11 | **No `x-github-api-version`, `x-interaction-id`, `x-initiator`, `x-client-machine-id` headers** | Confirmed in raw capture of `GET /models` and `POST /models/session`. |
| 12 | **No policy-enablement step for `state=disabled` models** | `claude-sonnet-5`, `gpt-5.4-mini`, `gpt-5.4`, `gpt-5.5`, `gemini-*`, `claude-fable-5`, `claude-opus-*` need enablement before use (still `model_not_supported` even with a session token). |

---

## 1. Complete API reference (canonical — no future inspection needed)

### 1.1 Hosts & endpoints

| Purpose | Method & path | Notes |
|---|---|---|
| List models | `GET /models` | Requires `copilot-integration-id` header to return the 32-model catalog. |
| Acquire model session | `POST /models/session` | Body `{"auto_mode":{"model_hints":["<modelId>"]}}` → `{available_models, selected_model, session_token (JWT), expires_at}`. **`session_token` is the `Copilot-Session-Token`.** |
| Chat (OpenAI-compat) | `POST /chat/completions` | Body uses `messages`. Works for models whose catalog lists `/chat/completions`. |
| Responses (GPT-5 class) | `POST /responses` | Body uses `input`/`reasoning`/`include`/`store`/`parallel_tool_calls`/`max_output_tokens`. Works for models whose catalog lists `/responses`. |
| Anthropic native | `POST /v1/messages` | For Claude models; alternate to `/chat/completions`. |
| Embeddings | `POST /embeddings` | Already implemented. |
| Auto-intent prediction | `POST /models/session/intent` | Optional (auto-mode routing). Not required for explicit model use. |

Base URL: `https://api.individual.githubcopilot.com` (override via `Config.BaseURL`
or `COPILOT_API_URL` env for tests). Enterprise: `https://copilot-api.<enterprise>`.

### 1.2 Auth

- **OAuth token** (value `gho_…`, field `CopilotToken`/`Config.CopilotToken`, env
  `GITHUB_COPILOT_TOKEN`) is sent as `Authorization: Bearer <token>` and works
  **directly** — no `/copilot_internal/v2/token` exchange is needed for OAuth
  tokens. (A GitHub PAT *can* still be exchanged via
  `/copilot_internal/v2/token` at `api.github.com` for the legacy path, but the
  OAuth token is preferred and sufficient.)
- **Model session token** (JWT, ~5–10 min TTL) acquired from `POST /models/session`,
  sent as `Copilot-Session-Token: <jwt>` on every chat/responses/messages call.
  Must be refreshed before `expires_at` (reuse via `existingToken`; the
  `mc_session_token_expired` flow triggers re-acquisition).

### 1.3 Exact header set (from raw capture of `GET /models` and `POST /models/session`)

```
authorization:          Bearer <oauth-or-copilot-token>
copilot-integration-id: copilot-developer-cli
copilot-session-token:  <jwt>            # only on chat/responses/messages calls
user-agent:             copilot/1.0.70 (client/github/cli linux v24.16.0) term/unknown
editor-version:         copilot/1.0.70
content-type:           application/json
accept:                 application/json
openai-intent:          conversation-agent
x-github-api-version:   2026-07-01
x-interaction-id:       <uuid>           # per session/turn
x-initiator:            user | agent     # user for plain prompts, agent for tool/follow-up
x-client-machine-id:    <uuid>           # stable per install
x-copilot-vision-request: true           # only when a message has image parts
```

Additional headers the official Rust client may set (not all required for 200;
include the ones needed and tolerate others): `x-copilot-traceparent`,
`x-github-user`, `x-agent-task-id`, `x-client-session-id`,
`x-parent-agent-id`, `x-model-provider-preference`. **Not** set by the official
CLI: `Editor-Version` (camelCase), `Editor-Plugin-Version`, `Copilot-Integration-ID`
(camelCase) — the README's claimed header set is incorrect; fix it.

### 1.4 Model catalog (32 models, `GET /models` with integration id) — endpoint + state + efforts

| Model | supported_endpoints | state | reasoning_effort | Works with session token? |
|---|---|---|---|---|
| `gpt-5-mini` | `/chat/completions`, `/responses`, `ws:/responses` | enabled | low,medium,high | ✅ both endpoints |
| `gpt-5.4-nano` | `/responses`, `ws:/responses` | enabled | none,low,medium,high,xhigh | ✅ `/responses` only |
| `gpt-5.4-mini` | `/responses`, `ws:/responses` | **disabled** | none,low,medium,high,xhigh | needs enablement |
| `gpt-5.4` | `/responses`, `/chat/completions`, `ws:/responses` | **disabled** | none,…,xhigh | needs enablement |
| `gpt-5.5` | `/responses`, `ws:/responses` | **disabled** | none,…,xhigh | needs enablement |
| `claude-sonnet-5` | `/v1/messages`, `/chat/completions` | **disabled** | low,medium,high,xhigh,max | needs enablement |
| `claude-sonnet-4.6` | `/chat/completions`, `/v1/messages` | **disabled** | low,medium,high,max | needs enablement |
| `claude-haiku-4.5` | `/chat/completions`, `/v1/messages` | enabled | — | ✅ `/chat/completions` |
| `claude-fable-5`/`claude-opus-4.7/4.8*` | `/v1/messages`, `/chat/completions` | **disabled** | low,…,max | needs enablement |
| `gemini-3.1-pro-preview` | `/chat/completions` | **disabled** | low,medium,high | needs enablement |
| `gemini-3.5-flash` | `/chat/completions` | **disabled** | minimal,low,medium,high | needs enablement |
| `kimi-k2.7-code` | `/chat/completions` | **disabled** | — | needs enablement |
| `gpt-4.1` / `gpt-4.1-2025-04-14` | (default `/chat/completions`) | enabled | — | ✅ no session token needed |
| `gpt-4o`, `gpt-4o-mini`, `gpt-4`, `gpt-3.5-turbo` (+dated) | `/chat/completions` | enabled | — | ✅ no session token needed |
| `text-embedding-3-small`, `text-embedding-ada-002` | — | enabled | — | embeddings only |

### 1.5 Request body formats

**`/chat/completions`** (OpenAI-compat, `messages`):
```json
{"model":"<id>","messages":[{"role":"user","content":"Hi"}],
 "stream":false,"store":false,"max_tokens":50,
 "temperature":0.7,"reasoning_effort":"medium"}
```
Response: `{choices:[{message:{content,role,reasoning_opaque?,reasoning_text?,encrypted_content?},finish_reason}],usage,object:"chat.completion"}`.

**`/responses`** (GPT-5 class, `input`):
```json
{"model":"<id>","input":[{"role":"user","content":[{"type":"input_text","text":"Hi"}]}],
 "stream":false,"store":false,"reasoning":{"effort":"medium"},
 "include":["reasoning.encrypted_content"],"parallel_tool_calls":true,
 "max_output_tokens":50}
```
Response: `{id,output:[{type:"message"|"function_call"|"reasoning",...}],usage:{input_tokens,output_tokens,...},copilot_usage,incomplete_details}`.

**`/v1/messages`** (Anthropic native, Claude) — alternate; body:
`{model,max_tokens,messages:[{role,content}]}`, header `anthropic-version: 2023-06-01`.

### 1.6 Temperature rules (confirmed)

- **Reasoning models** (any `gpt-5*`, and Claude/Gemini with reasoning): `temperature`
  is **unsupported** → any value other than omitted/`1.0` returns
  `400 "Unsupported parameter: 'temperature' is not supported with this model"`.
  **Omit `temperature` for all reasoning-capable models.**
- **Standard models** (`gpt-4.1`, `gpt-4o`, `gpt-3.5-turbo`, …): accept `0.0`–`2.0`.
- **Claude** (Anthropic): range `0.0`–`1.0` (verify in tests; clamp >1 to 1).

### 1.7 Endpoint selection rule (replaces `wouldUseResponses`)

For a model `m`, pick the first endpoint from `m.supported_endpoints` (catalog)
in this preference order, gated by what the client implements:
1. `/responses` — if listed AND model is GPT-5-class (reasoning). Use `input` body.
2. `/chat/completions` — if listed. Use `messages` body.
3. `/v1/messages` — for Claude if `/chat/completions` absent (future).
- `gpt-5.4-mini`/`gpt-5.4-nano`/`gpt-5.5` list **only** `/responses` → must use `/responses`.
- Legacy `gpt-4*`/`gpt-3.5*` → `/chat/completions`, no session token needed.

`ForceChatCompletions` remains as an escape hatch but must NOT route a model to an
endpoint absent from its `supported_endpoints`.

---

## 2. Client backport steps (so the integration tests can pass)

Files: `copilot.go`, `token.go`, `copilot_chat.go`, `copilot_responses.go`,
`copilot_responses_stream.go`, `copilot_stream.go`, `models.go`, `check.go`,
`README.md`. New file: `session.go`.

### 2.1 `token.go` — headers, base URL, constants
- Change `userAgentHeader` → `copilot/1.0.70 (client/github/cli linux v24.16.0) term/unknown`
  (compute dynamically: `copilot/<ver> (client/github/cli <platform> <nodever>) term/<TERM_PROGRAM>`).
- Add constants: `integrationID = "copilot-developer-cli"`, `editorVersion = "copilot/1.0.70"`,
  `apiVersion = "2026-07-01"`, `openaiIntent = "conversation-agent"`.
- Default base URL → `https://api.individual.githubcopilot.com` (`ResolveBaseURL`).

### 2.2 New `session.go` — model-session acquisition
- `acquireSession(ctx, modelHint) (*session, error)`: `POST {baseURL}/models/session`
  with body `{"auto_mode":{"model_hints":[modelHint]}}` and the full header set
  (§1.3, minus `copilot-session-token`). Parse `{session_token, expires_at, selected_model, available_models}`.
- `copilotSessionToken` struct with mutex + background refresh (reuse pattern from
  `copilotLockedToken`/`startTokenRefresh`); refresh ~60s before `expires_at`;
  on `mc_session_token_expired` / 401, re-acquire once and retry.
- Add `SessionToken` to `Config` (optional pre-obtained) for tests/offline.
- **Only acquire a session token for models that need it** (premium/non-legacy).
  Heuristic: if model is NOT in the legacy set (`gpt-4o`,`gpt-4o-mini`,`gpt-4`,
  `gpt-3.5-turbo`,`gpt-4.1` and dated variants), acquire a session. Better: check
  the catalog `supported_endpoints`/`state` cached from `ListModels`.

### 2.3 `copilot_chat.go` — header + body fixes
- `setAuthHeaders`: send `Authorization`, `User-Agent`, `editor-version`,
  `openai-intent: conversation-agent`, `copilot-integration-id`.
- `setPerRequestHeaders`: add `x-github-api-version`, `x-interaction-id` (per-turn
  UUID stored on the model), `x-initiator` (keep existing logic), `x-client-machine-id`
  (stable UUID, generated once in `NewCopilotChatModel`).
- Add `Copilot-Session-Token` header on chat/responses calls (from `session.go`).
- **Temperature**: in `buildChatRequest`, drop `Temperature` from the request when
  the resolved model is reasoning-capable (family `gpt-5*` or
  `capabilities.supports.reasoning_effort` non-empty / `adaptive_thinking`).
- **Reasoning effort**: extend `ReasoningEffort` to
  `none|minimal|low|medium|high|xhigh|max`; validate against the model's catalog
  `reasoning_effort` list; omit the field if the model has none.

### 2.4 `copilot_responses.go` / routing
- Replace `wouldUseResponses`/`useResponsesAPI` with the catalog-driven rule (§1.7).
  When `ListModels` has been called (cache), use `supported_endpoints`; otherwise
  fall back to the `gpt-N` (N≥5) heuristic but **never** route `gpt-5.4-mini`/
  `gpt-5.4-nano`/`gpt-5.5` to `/chat/completions`.
- `/responses` path already builds the `input` body — keep it; ensure
  `Copilot-Session-Token` is attached.

### 2.5 `models.go` — catalog fix
- `ListModels`: send `copilot-integration-id` (so it returns 32 models, not 7).
- Parse `capabilities.supports` correctly (`adaptive_thinking` can be the string
  `"unsupported"` or a bool — use `json.RawMessage`/custom unmarshal).
- Do **not** drop `model_picker_enabled=false` models; expose them with a flag so
  callers can still select GPT-5/Claude. Keep a `State` field (`enabled`/`disabled`).
- Expose `SupportedEndpoints` and `ReasoningEfforts` (already present) and `Family`.

### 2.6 Enablement for `state=disabled` models (scope-gated)
- Implement `enableModelPolicy(ctx, modelId)` calling the CAPI enablement endpoint
  (Rust fn `capi_client_enable_model_policy_with_client`; exact path requires a
  final raw capture — see §4 open question). Until implemented, tests for
  disabled models are marked `t.Skip("needs enablement")` rather than failing.
- `claude-sonnet-5` (the user-reported failure) is `state=disabled` → blocked on this.

### 2.7 README
- Replace the incorrect header table with the §1.3 set; document the session-token
  flow and the model/endpoint/temperature matrix.

---

## 3. Real-API integration test suite

New file `integration_test.go` with build tag `//go:build integration`.
Existing `*_test.go` (httptest mocks) stay unchanged and run by default.

### 3.1 Gating
- Build tag `integration` (excluded from `go test ./...` unless `-tags=integration`).
- Env gate: skip all unless `COPILOT_INTEGRATION=1` AND
  (`GITHUB_COPILOT_TOKEN` or `GITHUB_TOKEN`) set. Use `t.Skip` otherwise so CI is
  green.
- `COPILOT_API_URL` may override base URL (point at a local logging proxy for
  debugging); default to `https://api.individual.githubcopilot.com`.

### 3.2 Test matrix — all models × all temperatures

Enumerate models from the **live** `GET /models` at test start (call `ListModels`).
Skip embeddings models. For each chat model, run a subtest per temperature in a
per-family set:

| Family | Temperatures tested | Expectation |
|---|---|---|
| Reasoning GPT-5 (`gpt-5-mini`,`gpt-5.4-nano`,…) | `nil`, `1.0` | success; `0.0`/`2.0` → expect `400 unsupported temperature` (assert error, not fail) |
| Standard OpenAI (`gpt-4.1`,`gpt-4o`,`gpt-3.5-turbo`) | `0.0`,`0.5`,`1.0`,`1.5`,`2.0` | all success |
| Claude (`claude-haiku-4.5`,`claude-sonnet-5`) | `0.0`,`0.5`,`1.0` | success (clamp >1→1); `1.5`→`2.0` expect rejection |
| Gemini | `0.0`,`1.0` | success (after enablement) |

Prompt `"Hi"`, `max_tokens`/`max_output_tokens` ≤ 30, `stream:false` (streaming
covered by a separate, smaller matrix to limit cost). Use
`model.WithModel(id)` + `model.WithTemperature(t)` per subtest.

### 3.3 Test cases (table-driven)

1. **`TestIntegration_ListModels`** — `ListModels` returns ≥20 models incl.
   `gpt-5-mini` and a Claude model (proves the integration-id header works).
2. **`TestIntegration_ModelsTemperatures`** — the §3.2 matrix; for each
   `(model, temp)`:
   - acquire session token if model needs one (call the exported
     `AcquireSession` helper or rely on the client to do it internally);
   - `Generate(ctx, [user "Hi"], WithModel, WithTemperature)`;
   - assert: non-nil message, `ResponseMeta.FinishReason` set, content non-empty
     for success cases; for expected-rejection temps, assert the wrapped error
     contains `temperature` / `400`.
3. **`TestIntegration_ReasoningEffort`** — for one reasoning model, invoke with
   each catalog `reasoning_effort` value → success; invalid effort → assert error.
4. **`TestIntegration_Streaming`** — `Stream` for one reasoning model + one
   standard model; assert at least one content chunk + a final usage/meta chunk.
5. **`TestIntegration_SessionTokenRefresh`** — call with a deliberately-expired
   `SessionToken`; assert the client re-acquires and the call succeeds.
6. **`TestIntegration_EndpointRouting`** — for `gpt-5.4-nano` assert it hits
   `/responses` (route to `/chat/completions` would fail); for `gpt-4.1` assert
   `/chat/completions`. Validate via the success/error outcome (no proxy needed).
7. **`TestIntegration_DisabledModels`** — for `claude-sonnet-5`/`gpt-5.4-mini`:
   if enablement is unimplemented, `t.Skip`; else `enableModelPolicy` then call.

### 3.4 Test helpers
- `newIntegrationModel(t, modelID) *CopilotModel` — builds `Config` from env,
  sets `Model`, returns model (session token acquired lazily on first call).
- `requireSkip(t)` — env gate.
- Reuse a single `*CopilotModel` per model across temperature subtests (one
  session token, refreshed as needed) to minimize API calls.
- Mark every test `testing.Short()`-skip too.

### 3.5 Cost/quotas
- ~32 models × ≤5 temps × (Generate + 1 Stream) ≈ a few hundred short calls.
  `max_tokens` ≤30 keeps each call cheap. Tests are opt-in (build tag + env).
- Add a `Makefile`/AGENTS.md note: `COPILOT_INTEGRATION=1 GITHUB_COPILOT_TOKEN=… go test -tags=integration ./components/model/copilot/...`

---

## 4. Open questions / residual unknowns

1. **Exact enablement endpoint** for `state=disabled` models (Rust
   `capi_client_enable_model_policy_with_client`). Not captured because the CLI
   only reaches it via auto-mode/interactive consent. Resolve by running the CLI
   interactively with a disabled model through the local http logging proxy
   (`COPILOT_API_URL=http://127.0.0.1:8080`, proxy forwards to the real https
   host — system CA is read-only in this env, so the http→https proxy is the
   capture method). The `claude-sonnet-5` fix depends on this.
2. **Claude `/v1/messages` necessity** — `/chat/completions` works for
   `claude-haiku-4.5` with a session token; whether disabled Claude models
   require `/v1/messages` after enablement, or whether `/chat/completions`
   suffices, is confirmed during test §3.3 once enablement exists.
3. **HMAC auth kind** — the CAPI supports `authKind="hmac"` (HMAC key +
   `x-github-user`); not needed for the OAuth-token path. Out of scope unless a
   PAT-only user fails.

---

## 5. Validation / acceptance

- `go build ./...`, `go vet ./...`, `go test ./components/model/copilot/...` (mocks) green.
- `COPILOT_INTEGRATION=1 … go test -tags=integration` passes for all `enabled`
  models across their temperature matrix; disabled models skip until enablement lands.
- `gpt-5-mini`, `gpt-5.4-nano`, `claude-haiku-4.5`, `gpt-4.1` produce non-empty
  content in both `Generate` and `Stream`.
- Reasoning models succeed with `temperature` omitted/`1.0` and reject other temps.
- `claude-sonnet-5` either passes (post-enablement) or is explicitly skipped with
  a tracked TODO — never a silent failure.

## 6. Order of work
1. Headers/base-URL/constants + `ListModels` integration-id (§2.1, §2.5) — unblocks catalog visibility.
2. `session.go` + `Copilot-Session-Token` on chat/responses (§2.2, §2.3) — unblocks premium models.
3. Routing + temperature + reasoning-effort fixes (§2.3, §2.4) — unblocks `gpt-5.4-nano`/reasoning.
4. Integration test harness (§3) — drives validation as each fix lands.
5. Enablement flow for disabled models (§2.6) + final raw capture (§4.1) — unblocks `claude-sonnet-5`.
6. README + AGENTS.md test command (§2.7, §3.5).
