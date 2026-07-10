# Plan: Fix & complete the GitHub Copilot model provider (`components/model/copilot`)

## Goal

Bring `components/model/copilot` to functional parity with the working kilocode
GitHub Copilot provider (`/projects/kilocode/packages/core/src/github-copilot`
and `/projects/kilocode/packages/opencode/src/plugin/github-copilot`), in idiomatic
Go, while respecting `CONTRIBUTING.md`/`AGENTS.md` conventions.

Scope confirmed with user: **fix all issues**, including the Responses API path for
GPT-5-class models.

The Go code currently builds and its tests pass, so every item below is a
behavioral/parity defect, not a compile error. Several current tests are
**wrong** (they assert the wrong wire shape) and must be updated, not trusted.

## Reference map (kilocode → eino-ext)

- Chat completions model: `chat/openai-compatible-chat-language-model.ts` → `copilot_chat.go` + `copilot_stream.go`
- Message conversion: `chat/convert-to-openai-compatible-chat-messages.ts` → `convertMessage` in `copilot_chat.go`
- Tool prep: `chat/openai-compatible-prepare-tools.ts` → `convertTools`/tool_choice
- Finish reason: `chat/map-openai-compatible-finish-reason.ts` → (missing in Go)
- Models discovery: `opencode/.../models.ts` → `models.go`
- Headers/intent/initiator: `opencode/.../copilot.ts` → `setAuthHeaders` in `copilot_chat.go`
- Responses API: `responses/*.ts` → NEW `copilot_responses.go` (+ stream)
- GPT-5 routing: `shouldUseResponsesApi` in `llm/src/providers/github-copilot.ts` and `plugin/provider/github-copilot.ts` → NEW routing in `Generate`/`Stream`

---

## Task list (ordered)

### A. Confirmed correctness bugs (chat completions)

1. **Fix `/models` capability parsing (`models.go`).**
   - The real Copilot API returns `capabilities.supports` as an **object**, not an
     array. Current `copilotModelSupport []` + `Supports []copilotModelSupport`
     never populates any flag against the live API.
   - Replace `copilotModelSupport`/`Supports []` with a struct matching kilocode
     `models.ts` schema:
     ```
     supports: {
       tool_calls bool, streaming bool, vision *bool,
       structured_outputs *bool, adaptive_thinking *bool,
       reasoning_effort []string, max_thinking_budget *int, min_thinking_budget *int
     }
     ```
   - Add `capabilities.family string`, `capabilities.limits.max_prompt_tokens int`,
     `capabilities.limits.vision { max_prompt_image_size, max_prompt_images,
     supported_media_types []string }`, top-level `version string`,
     `supported_endpoints []string`.
   - Recompute `ModelInfo`:
     - `SupportsToolCalls = supports.tool_calls`
     - `SupportsStreaming = supports.streaming`
     - `SupportsVision = supports.vision || any(limits.vision.supported_media_types startsWith "image/")`
     - `SupportsReasoning = adaptive_thinking || len(reasoning_effort)>0 || max/min_thinking_budget set`
     - Add `MaxPromptTokens int`, `Family string`, `ReasoningEfforts []string`,
       `SupportedEndpoints []string` to `ModelInfo`.
   - Keep filtering: `model_picker_enabled == true && policy.state != "disabled"`
     (policy optional).
   - Update `models_test.go` to feed the correct object shape and assert the new
     fields.

2. **Send `tool_choice` (`copilot_chat.go`).**
   - `buildChatRequest` computes `toolChoice` via `GetCommonOptions` then drops it.
   - Add `ToolChoice any json:"tool_choice,omitempty"` to `copilotChatRequest` and
     map eino `schema.ToolChoice` → `"auto"|"none"|"required"` (and
     `{type:"function",function:{name}}` for a forced tool), mirroring
     `openai-compatible-prepare-tools.ts`. Only send when tools are present.

3. **Confirm max-tokens field name.**
   - kilocode chat sends `max_tokens`; eino-ext sends `max_completion_tokens`.
   - Decision: send **`max_tokens`** to match the working reference for the
     chat/completions endpoint. Keep the config field name
     `MaxCompletionTokens` (public API) but marshal it to `max_tokens`.
   - (Responses API path uses `max_output_tokens` — see section C.)

4. **Validate/require a model id.**
   - Empty `Config.Model` with no per-call `model` option currently produces
     `"model":""`. After `GetCommonOptions`, if the resolved model is empty,
     return a wrapped error (`emperror.dev/errors`) instead of calling the API.
   - Do NOT add `validate:"required"` on `Config.Model` (per-call override must
     still work); enforce at request-build time.

5. **Fix streaming reasoning/content in the same chunk (`copilot_stream.go`).**
   - Current logic `continue`s on any chunk containing `reasoning_text`/
     `reasoning_opaque`, silently dropping `content`/`tool_calls` present in the
     same delta (kilocode explicitly handles reasoning + content in one chunk).
   - Rework `streamEvents`: within a single delta, emit reasoning first (as a
     `ReasoningContent` message), then, in the **same** iteration, process
     `content` and `tool_calls`. Remove the `reasoningOpen` skip hack.
   - Preserve existing behaviors covered by `copilot_stream_test.go`
     (tool-call accumulation, sort-by-index emission on finish, opaque→reasoning
     mapping). Add a regression case: single chunk carrying both `reasoning_text`
     and `content`.

6. **Add a shared finish-reason mapper.**
   - Port `map-openai-compatible-finish-reason.ts` to Go
     (`stop|length|content_filter|tool_calls|function_call` → normalized values)
     and use it to populate `ResponseMeta.FinishReason` in both `Generate` and
     `Stream` (streaming currently never sets a finish reason on emitted msgs).

### B. Chat-completions parity features

7. **Vision / image input (`copilot_chat.go`).**
   - `convertMessage` currently flattens every message to a text string, dropping
     images. Port `convert-to-openai-compatible-chat-messages.ts`:
     - For user messages with `UserInputMultiContent` (or deprecated
       `MultiContent`), build an array content of
       `{type:"text",text}` and `{type:"image_url",image_url:{url}}` parts.
     - Image URL: use `ImageURL.URL` if set, else
       `data:{MIMEType};base64,{Base64Data}` (normalize `image/*` → `image/jpeg`).
     - Keep the single-text fast path (string content) when only one text part.
   - Update `copilotMessage.Content` to `any` (string | []part) or add a typed
     content-part model; add content-part structs mirroring
     `openai-compatible-api-types.ts`.

8. **Reasoning round-trip (`reasoning_text` / `reasoning_opaque`).**
   - Outbound: in `convertMessage` for assistant messages, re-emit
     `reasoning_text`/`reasoning_opaque` when present so multi-turn reasoning
     context is preserved (kilocode attaches `reasoning_opaque` via provider
     metadata). Store/read the opaque blob in `schema.Message.Extra`
     (e.g. key `copilot_reasoning_opaque`); map `ReasoningContent` →
     `reasoning_text`.
   - Inbound: `convertChoiceToMessage` and streaming already map
     `reasoning_text` → `ReasoningContent`; additionally persist
     `reasoning_opaque` into `Message.Extra` so the next turn can send it back.

9. **Forward standard sampling options.**
   - In `buildChatRequest`, pass through `GetCommonOptions` values that Copilot
     accepts: `top_p` (`options.TopP`), `stop` (`options.Stop`). Add optional
     `frequency_penalty`, `presence_penalty`, `seed`, `n` to `Config` only if we
     want config-level control; otherwise wire from options. Keep
     `reasoning_effort` but also allow a per-call override (see model options
     below).

10. **Per-call reasoning effort.**
    - kilocode exposes `reasoningEffort` as a provider option. Add support to
      read a per-call reasoning effort (via an exported model `Option` using
      eino's `model.WrapImplSpecificOptFn`, or a documented `Config` default).
      Keep `Config.ReasoningEffort` as the default.

### C. Responses API port (GPT-5-class models)

11. **Add GPT-5 routing helper.**
    - Port `shouldUseResponsesApi`: `^gpt-(\d+)` with `n>=5` and not
      `gpt-5-mini` → use Responses endpoint. Expose as `func useResponsesAPI(model string) bool`.
    - In `Generate`/`Stream`, dispatch to the Responses implementation when
      `useResponsesAPI(resolvedModel)` is true, else the existing chat path.

12. **New `copilot_responses.go` (non-streaming) — port from
    `responses/openai-responses-language-model.ts` + `convert-to-openai-responses-input.ts`,
    scoped to eino needs.**
    - Endpoint: `POST {baseURL}/responses`.
    - Request body fields: `model`, `input` (converted), `max_output_tokens`
      (from `MaxCompletionTokens`), `temperature`, `top_p`, `stream`,
      `store` (default true), `tools`, `tool_choice`, `include`
      (`["reasoning.encrypted_content"]` when reasoning), and `reasoning:{effort,summary}`
      when a reasoning effort/summary is set.
    - `systemMessageMode`: default `"system"`; keep it configurable per-model if
      needed (kilocode maps some models to `developer`/`remove`). Start with
      `"system"` and note the mapping table as a follow-up constant.
    - Input conversion (`convert-to-openai-responses-input.ts`):
      - system → `{role:"system"|"developer",content}` (or drop for `remove`).
      - user → `{role:"user",content:[input_text | input_image]}`; images as
        `input_image` with `image_url` (URL or base64 data URL). PDF/`input_file`
        support optional (note as follow-up; eino image parts are the priority).
      - assistant text → `{role:"assistant",content:[{type:"output_text",text}]}`
        carrying `id` from `Message.Extra` item id when present.
      - assistant tool-call → `{type:"function_call",call_id,name,arguments,id?}`.
      - reasoning → emit `{type:"reasoning",id,encrypted_content,summary[]}` or an
        `item_reference` when `store` and an item id is known (round-trip via
        `Message.Extra`).
      - tool role → `{type:"function_call_output",call_id,output}`.
      - **Out of scope (eino has only function tools):** built-in provider tools
        (`web_search`, `web_search_preview`, `code_interpreter`,
        `image_generation`, `file_search`, `local_shell`) and their
        approval/`mcp_approval_response` flows. Do not port
        `responses/tool/*`. Add a code comment explaining the exclusion.
    - Tool prep: only the `function` branch of `openai-responses-prepare-tools.ts`
      (`{type:"function",name,description,parameters,strict}`), plus
      `tool_choice` (`auto|none|required|{type:"function",name}`).
    - Parse response output items: `message`/`output_text` → `Content`,
      `function_call` → `ToolCalls`, `reasoning` → `ReasoningContent` +
      persist `encrypted_content`/item id into `Message.Extra`.
    - Finish reason: port `map-openai-responses-finish-reason.ts`
      (`max_output_tokens`→length, `content_filter`→content-filter,
      null/other with function calls → tool-calls).
    - Usage: map `usage.input_tokens`/`output_tokens` +
      `output_tokens_details.reasoning_tokens` +
      `input_tokens_details.cached_tokens` → `schema.TokenUsage`.

13. **Responses streaming (`copilot_responses.go` or `copilot_responses_stream.go`).**
    - SSE `data:` events; handle the event `type`s used by kilocode:
      `response.created`, `response.output_item.added`,
      `response.output_text.delta`, `response.function_call_arguments.delta`,
      `response.reasoning_summary_text.delta`, `response.output_item.done`,
      `response.completed` / `response.incomplete`.
    - Accumulate function-call arguments per output item; on `output_item.done`
      or `completed` emit the tool call. Emit reasoning deltas as
      `ReasoningContent` messages; capture `encrypted_content` into
      `Message.Extra`. Ignore image/code-interpreter/annotation events (out of
      scope) but tolerate them without erroring.

### D. Headers & auth parity

14. **`x-initiator` header (`copilot_chat.go` / responses).**
    - kilocode sets `x-initiator: agent|user` per request based on whether the
      last message is a plain user prompt (`user`) vs a tool/continuation
      (`agent`). This affects Copilot rate/billing classification.
    - Add logic in request send: inspect the last input message — role `User`
      with text-only content → `user`; otherwise (assistant/tool follow-up,
      tool results, synthetic attachment) → `agent`. Set the header accordingly.

15. **`Copilot-Vision-Request: true` header** when any message carries an image
    part (both chat and responses paths).

16. **Align `Openai-Intent`.**
    - kilocode opencode uses `conversation-edits`; current Go uses
      `conversation-agent`. Decision: keep a single default but make it a package
      constant and document it. Recommend matching kilocode (`conversation-edits`)
      unless there is a known reason for `conversation-agent`; note this in the PR
      description for reviewer confirmation.
    - Leave `Editor-Version`/`User-Agent`/`Copilot-Integration-ID` as-is (valid
      VSCode identifiers); document that they are required Copilot headers.

### E. Tests, docs, checkup

17. **Update/expand tests (no live network).**
    - Fix `models_test.go` to the correct `supports` object shape (item A1).
    - Add `httptest`-based tests for `Generate`/`Stream` covering: text, tool
      calls, tool_choice serialization, `max_tokens` field name, vision message
      serialization, reasoning round-trip (opaque in→out), and finish-reason
      mapping.
    - Add responses-path tests: `useResponsesAPI` routing table
      (`gpt-5`, `gpt-5-mini`, `gpt-4o`, `gpt-6`…), input conversion, non-stream
      parse, and SSE stream parsing (mock event sequence).
    - Add a chat-stream regression test for reasoning+content in one chunk
      (item A5).
    - Keep table-driven style; use mock servers only.

18. **Update `README.md`.**
    - Document: vision support, reasoning round-trip, `x-initiator`/vision
      headers, per-call reasoning effort, `max_tokens` behavior, and the
      GPT-5 Responses API routing (which models use `/responses`, and that
      built-in provider tools are intentionally unsupported).
    - Refresh the request-headers table and the models/capabilities section
      (new `ModelInfo` fields).

19. **Checkup (`check.go`).**
    - No structural change required, but after A1 the `/models` probe will parse
      correctly; add/adjust a `check_test.go` case asserting the probe handles
      the real `supports` object shape and a `limited` result when no enabled
      models are returned.

### F. Cross-cutting conventions

20. Wrap all new errors with `emperror.dev/errors` including operation context.
21. Thread `ctx context.Context` as the first parameter through any new
    constructor/helper that issues HTTP requests.
22. No license banners; keep the package comment.
23. Prefer `fmt.Sprintf` over string concatenation; keep prompt-like constants as
    Go consts (no `//go:embed` needed here — no large prompt files).

---

## Design decisions (resolved)

- **Full parity including Responses API** (user directive).
- **Chat endpoint uses `max_tokens`** (matches working kilocode reference).
- **Built-in OpenAI provider tools are out of scope** — eino tools are function
  tools only; document the exclusion rather than porting `responses/tool/*`.
- **OAuth device-flow login is out of scope** — it is a UI/CLI concern in
  kilocode's opencode plugin. The Go library keeps its `GitHubToken` (PAT) →
  `/copilot_internal/v2/token` exchange and `CopilotToken` modes. (Note only.)
- **reasoning_opaque / encrypted_content and item ids** are round-tripped via
  `schema.Message.Extra` (eino has no dedicated field).

## Risks / watch-outs

- Changing `copilotMessage.Content` to support array content must not break the
  existing string-content path or JSON omitempty behavior.
- The current tests encode the *wrong* `/models` shape; they will fail after A1
  and MUST be rewritten to the correct shape (do not "fix" production code to the
  old tests).
- Responses SSE event set is large; only the listed event types are required —
  unknown events must be ignored gracefully, not error.
- `x-initiator` heuristic must handle tool-result-only follow-ups as `agent`.
- Keep streaming back-pressure/goroutine + `schema.Pipe` semantics identical to
  the current `Stream` implementation.

## Validation

Run from repo root and from the component dir:

```bash
go build ./...
go vet ./...
go test ./...
go test ./components/model/copilot/... -run . -count=1
```

All must pass with no live external calls (mock servers only).

## Suggested file layout after change

- `copilot.go` — config, constructor, WithTools/BindTools, routing (unchanged core).
- `copilot_chat.go` — chat/completions request build, message/tool/vision
  conversion, headers, finish-reason, usage.
- `copilot_stream.go` — chat SSE streaming (reasoning+content fix).
- `copilot_responses.go` — responses request build, input conversion, non-stream
  parse, finish-reason, usage.
- `copilot_responses_stream.go` — responses SSE streaming.
- `models.go` — corrected `/models` schema + `ModelInfo`.
- `token.go`, `check.go`, `copilot_embedding.go` — minor/no change.
- Tests: `*_test.go` per file above; README updated.

> Implementation requires source-file edits; switch to an implementation-capable
> agent to execute this plan.
