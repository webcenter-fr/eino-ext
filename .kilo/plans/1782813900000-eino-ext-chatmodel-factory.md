# Plan: eino-ext reusable chat-model factory (thinking levels + output cap)

Target repo: `github.com/webcenter-fr/eino-ext` (module root `go.mod` =
`github.com/webcenter-fr/eino-ext`, Go 1.25). Implement in that repo, then tag a
release so `rancher-doc-chat-api-k8s` can consume it (see companion plan
`.kilo/plans/1782813816774-thinking-iterations-tokens.md`, Plan B).

## Why this lives in the framework
The provider-generic model construction — mapping a thinking *level* to provider
config and capping output tokens — is reusable across any eino project. The repo
already owns analogous reusable components: `components/model/cachestab`,
`components/middleware/{agentattr,contextopt}`. This adds a sibling
`components/model` package. Per-role wiring, config keys, and MaxIterations policy
stay in the consuming project, not here.

## Constraints / facts verified
- eino OpenAI model (`eino-ext/components/model/openai@v0.1.13`) exposes:
  - `ReasoningEffort ReasoningEffortLevel` (`chatmodel.go:182`), values only
    `Low/Medium/High` (`types.go:30`).
  - `MaxCompletionTokens *int` (`chatmodel.go:134`).
  - `Temperature`, `Timeout`, `BaseURL`, `Model`.
- eino Ollama model exposes `Thinking *ollamaapi.ThinkValue{Value bool}` only — no
  levels.
- Keep the API additive; do not modify `cachestab` (consumers still wrap the
  result with `cachestab.NewToolCallingChatModel`).

## Tasks

### 1. New package `components/model/chatmodel`
Create `components/model/chatmodel/chatmodel.go`, `package chatmodel`.

- `type ThinkingLevel string` with consts:
  `Off="off"`, `Low="low"`, `Medium="medium"`, `High="high"`.
- `func ParseThinkingLevel(s string) (ThinkingLevel, error)`:
  - case-insensitive; trims space.
  - `""` and `"false"`/`"none"` → `Off`.
  - `"true"` → a documented default (`Medium`).
  - unknown value → error.
- `const OutputTokenMax = 32_000` (mirrors kilocode `OUTPUT_TOKEN_MAX`).
- `type Config struct`:
  - `Plan string` — `"ollama"` | `"copilot"` | `"openai"`.
  - `BaseURL string`
  - `Model string`
  - `Temperature float32`
  - `Thinking ThinkingLevel`
  - `MaxOutputTokens int` (0 = unset, leave provider default)
  - `Timeout time.Duration` (0 = package default 60m, used by openai path)
- `func New(ctx context.Context, cfg *Config) (model.ToolCallingChatModel, error)`:
  - validate `cfg` non-nil, `Plan` supported (error `unsupported plan: %s`).
  - **ollama**: `ollama.NewChatModel` with
    `Thinking: ptr.To(ollamaapi.ThinkValue{Value: cfg.Thinking != Off})`,
    `Options.Temperature`. (Ollama has no levels; non-Off ⇒ true.) Ollama has no
    completion-token field in this version — ignore `MaxOutputTokens` there (or
    map to `Options.NumPredict` if available; verify the ollama api struct before
    using).
  - **copilot/openai**: `openai.NewChatModel` with `BaseURL`, `Model`,
    `Temperature`, `Timeout` (default 60m), and:
    - reasoning: only set `ReasoningEffort` when `Thinking != Off`, mapping
      `Low→ReasoningEffortLevelLow`, `Medium→Medium`, `High→High`. Leaving it
      unset for `Off` keeps non-reasoning models unaffected.
    - tokens: set `MaxCompletionTokens: ptr.To(cfg.MaxOutputTokens)` only when
      `cfg.MaxOutputTokens > 0`.
  - wrap construction errors with `emperror.dev/errors` (match repo convention;
    confirm which error lib the repo already uses).

### 2. Output-token helper
- `func CapOutputTokens(modelOutputLimit, ceiling int) int`:
  - if `ceiling <= 0` → `ceiling = OutputTokenMax`.
  - return `min(modelOutputLimit, ceiling)`; if `modelOutputLimit <= 0` return
    `ceiling` (limit unknown). Mirrors kilocode `transform.ts:1389`.

### 3. Mapping helper (internal or exported)
- `func reasoningEffort(l ThinkingLevel) (openai.ReasoningEffortLevel, bool)`
  returning the mapped level and `ok=false` for `Off`, so `New` can decide
  whether to set the field. Keep it small and table-driven for testing.

### 4. Tests `components/model/chatmodel/chatmodel_test.go`
Follow existing `cachestab_test.go` / `example_test.go` style.
- `ParseThinkingLevel`: each alias, empty, `true`/`false`, unknown→error.
- `reasoningEffort`: Low/Medium/High map correctly; Off→ok=false.
- `CapOutputTokens`: ceiling default when 0; min selection; unknown limit→ceiling.
- `New` validation: unsupported plan errors; nil cfg errors.
- Where practical, assert that for `Off` no reasoning effort is set and that
  `MaxCompletionTokens` stays nil when `MaxOutputTokens == 0` (may require a thin
  seam or constructing config structs directly rather than hitting a network).

### 5. Docs
- `components/model/chatmodel/README.md`: purpose, `Config` fields, thinking-level
  semantics (incl. Ollama bool collapse and the v0.1.13 Low/Medium/High limit),
  and a short usage example (mirroring `model/cachestab/example_test.go`).

### 6. Release
- `go build ./...`, `go vet ./...`, `go test ./components/model/chatmodel/...`.
- Commit and push; create a tag / pseudo-version that the consuming project pins.
- API must be additive — no breaking changes to existing exported packages.

## Risks / notes
- v0.1.13 OpenAI model has no `none`/`minimal`/`xhigh`; do not invent levels the
  SDK can't send. `Off` = "omit reasoning".
- Verify the Ollama api struct for an output-token equivalent
  (`Options.NumPredict`) before wiring `MaxOutputTokens` on the ollama path;
  otherwise document that it is openai-only.
- Confirm the repo's error-wrapping convention (`emperror.dev/errors` vs stdlib)
  and match it.
- Keep `Config` additive-friendly (struct of options) so future fields don't break
  callers.

## Validation summary
- Unit tests green for parsing, mapping, capping, and constructor validation.
- `New` produces a working `model.ToolCallingChatModel` for both `ollama` and
  `openai/copilot` plans against a local/stub endpoint.

## Handoff
Once tagged, proceed with Plan B (B1 bump) in
`.kilo/plans/1782813816774-thinking-iterations-tokens.md`.

> Source edits require an implementation-capable agent working in the eino-ext
> repo; this plan is read-only.
