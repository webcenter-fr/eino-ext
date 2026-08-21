# Plan: Fix promptenhance context loss & history corruption

## Goal

Fix two defects in `github.com/webcenter-fr/eino-ext` that affect the consuming
application:

1. **Context-blind enhancement** — the middleware enhances the last user message
   using only that message's raw string, so terse context-dependent follow-ups
   ("re run the command", "ran37hpd2") get rewritten or refused by the small model.
2. **In-place mutation corrupts persisted history** — the middleware writes the
   enhanced text back onto the caller-owned `*schema.Message`, so the consumer's
   persisted conversation stores enhancer output instead of what the user typed.

The fix adds a context-aware `EnhanceInContext` API and makes every middleware
branch replace `state.Messages[idx]` with a clone, never mutating the caller's
original pointer.

## Source of truth

External design doc: `.kilo/1787324934253-eino-ext-promptenhance-context-fix.md`.
Line numbers cited there were verified against current source and match
(`middleware.go:78/89/115/120`, `prompt_enhance.go:68`). No drift.

## Non-goals (explicitly OUT of scope)

- Optional additive `ShouldEnhanceState` / `ShouldEnhanceStateFunc` hook on the
  middleware `Config` — do NOT add it.
- Any change in the consumer (rancher) repo.
- Memory/window/summarization subsystem changes.
- Reworking the interrupt/approval UX flow.
- Keeping public constructors source-compatible: `NewEnhancer(ctx, *Config)` and
  `NewMiddleware(*Config)` signatures must NOT change (consumer only constructs
  these; verified it never calls `Enhance` directly).

## Resolved design decisions

- `Config.MaxContextMessages int`, `validate:"gte=0"`, default **6** when unset.
- Negative values are **treated as 0** (defensive clamp) and then receive the
  default → net effect: any `MaxContextMessages <= 0` becomes 6. The `gte=0` tag
  is retained as a documented contract but never fires after normalization (see
  note in Task 1). This is the user-resolved outcome of the design doc's open
  question (default 6, not 0).
- Context is rendered as a role-labelled compact transcript embedded in the
  single user message (NOT as real role messages), bounded to the last
  `min(maxContextMessages, len(history))` messages.
- The middleware always replaces `state.Messages[idx]` with a clone on every
  branch that marks/alters the message; the caller's original pointer is never
  mutated. The `InterruptError` branch stays non-mutating.

---

## Ordered implementation tasks

### Task 1 — `libs/promptenhance/prompt_enhance.go` (MODIFY)

#### 1a. Add package constant

```go
// defaultMaxContextMessages is the number of prior messages used as context
// when Config.MaxContextMessages is left unset (0).
const defaultMaxContextMessages = 6
```

#### 1b. Extend `Config`

Add one field to the existing `Config` struct (keep `Model` and `SystemPrompt`
unchanged):

```go
type Config struct {
	// Model is the small model used to enhance prompts.
	// Typically a fast, cheap model (claude-haiku, gemini-flash, gpt-5-nano).
	Model model.BaseChatModel `validate:"required" jsonschema:"required,description=Model used to enhance prompts (should be small/fast)"`

	// SystemPrompt overrides the default enhancement system prompt.
	// When empty, DefaultEnhanceSystem is used.
	SystemPrompt string `jsonschema:"description=Optional override for the enhancement system prompt"`

	// MaxContextMessages bounds how many most-recent prior messages are included
	// as conversation context. 0 (unset) defaults to 6; a negative value is
	// treated as 0. Context is embedded in the single user message, not sent as
	// real role messages.
	MaxContextMessages int `validate:"gte=0" jsonschema:"description=Maximum number of prior messages to include as conversation context (default 6)"`
}
```

#### 1c. Extend `Enhancer`

```go
type Enhancer struct {
	model              model.BaseChatModel
	systemPrompt       string
	maxContextMessages int
}
```

#### 1d. Rewrite `NewEnhancer` (apply defaults BEFORE validation)

```go
func NewEnhancer(ctx context.Context, cfg *Config) (*Enhancer, error) {
	if cfg == nil {
		return nil, errors.New("promptenhance: config is required")
	}
	// Treat negative as 0, then default unset (0) to 6.
	if cfg.MaxContextMessages < 0 {
		cfg.MaxContextMessages = 0
	}
	if cfg.MaxContextMessages == 0 {
		cfg.MaxContextMessages = defaultMaxContextMessages
	}
	if err := validate.Struct(cfg); err != nil {
		return nil, err
	}

	sp := cfg.SystemPrompt
	if sp == "" {
		sp = DefaultEnhanceSystem
	}

	return &Enhancer{
		model:              cfg.Model,
		systemPrompt:       sp,
		maxContextMessages: cfg.MaxContextMessages,
	}, nil
}
```

> Note: because the clamp runs before `validate.Struct`, the `gte=0` tag never
> actually rejects a negative value. Keep it anyway as the documented contract
> (matches the design doc) and as a guard against future reordering. This is
> intentional per "treat negative as 0".

#### 1e. Reduce `Enhance` to a wrapper

```go
// Enhance rewrites draft into a clearer prompt without answering it.
func (e *Enhancer) Enhance(ctx context.Context, draft string) (string, error) {
	return e.EnhanceInContext(ctx, nil, draft)
}
```

#### 1f. Add `EnhanceInContext`

```go
// EnhanceInContext rewrites draft into a clearer prompt, using up to
// e.maxContextMessages most-recent prior messages from history as reference
// context. history may be nil or empty; it is never mutated.
func (e *Enhancer) EnhanceInContext(ctx context.Context, history []*schema.Message, draft string) (string, error) {
	if draft == "" {
		return "", nil
	}

	userContent := buildUserContent(history, draft, e.maxContextMessages)

	messages := []*schema.Message{
		schema.SystemMessage(e.systemPrompt),
		{
			Role:    schema.User,
			Content: userContent,
		},
	}

	result, err := e.model.Generate(ctx, messages)
	if err != nil {
		return "", errors.Wrap(err, "promptenhance: model generation failed")
	}

	return clean(result.Content), nil
}
```

#### 1g. Add context-rendering helpers

```go
// buildUserContent renders the draft (plus optional conversation context) into
// the single user message. When there is no context it falls back to the legacy
// single-draft format.
func buildUserContent(history []*schema.Message, draft string, maxContextMessages int) string {
	ctxStr := renderContext(history, maxContextMessages)
	if ctxStr == "" {
		return fmt.Sprintf("Draft prompt to enhance, not answer:\n\n<draft>%s</draft>", draft)
	}
	return fmt.Sprintf("%s\n\nDraft to rewrite (rewrite ONLY this into a clear, standalone prompt, resolving references using the context above; do NOT answer it):\n<draft>%s</draft>", ctxStr, draft)
}

// renderContext builds a role-labelled compact transcript of the last up-to-N
// prior messages, skipping nil and empty-content entries. Returns "" when there
// is nothing to render or context is disabled (maxContextMessages <= 0).
func renderContext(history []*schema.Message, maxContextMessages int) string {
	if maxContextMessages <= 0 || len(history) == 0 {
		return ""
	}

	n := maxContextMessages
	if n > len(history) {
		n = len(history)
	}
	start := len(history) - n

	var b strings.Builder
	b.WriteString("Recent conversation (context only — do NOT answer or continue it):\n<context>")
	for _, m := range history[start:] {
		if m == nil || strings.TrimSpace(m.Content) == "" {
			continue
		}
		b.WriteString("\n")
		b.WriteString(roleLabel(m.Role))
		b.WriteString(": ")
		b.WriteString(m.Content)
	}
	b.WriteString("\n</context>")
	return b.String()
}

// roleLabel maps a message role to a human-readable label for the transcript.
func roleLabel(role schema.RoleType) string {
	switch role {
	case schema.User:
		return "User"
	case schema.Assistant:
		return "Assistant"
	case schema.Tool:
		return "Tool"
	case schema.System:
		return "System"
	default:
		return "Message"
	}
}
```

Keep `clean` and `stripSurroundingQuotes` unchanged. Ensure `strings` is already
imported (it is). No new imports required beyond what exists.

Edge cases handled:
- `draft == ""` → short-circuit before any model call.
- `history == nil` / `len(history) == 0` → legacy single-draft format.
- `maxContextMessages <= 0` → no context (defensive; reachable via zero-value
  `Enhancer` in tests or future changes).
- nil message or blank content in history → skipped.
- more history than the cap → only the last `n` are rendered.

---

### Task 2 — `libs/promptenhance/prompts/enhance_system.md` (REPLACE ENTIRE CONTENT)

Replace the file with exactly this single paragraph (verbatim from the design
doc; the file remains `//go:embed`-ed into `DefaultEnhanceSystem`, so no Go
change is needed):

```text
You rewrite the user's latest draft prompt for another assistant. You may be
given the recent conversation as context — use it ONLY to resolve references
(e.g. "it", "that command", "the same pod", a bare name or identifier) so you
can turn the draft into a clear, self-contained request. Never answer,
execute, continue, or comment on the conversation. Rewrite ONLY the final
draft. Return only the rewritten prompt text the user could send next — no
conversation, explanations, lead-ins, bullet points, placeholders, surrounding
quotes, or markdown fences. If the draft is already clear, or is a short
reply/confirmation/answer to a previous question (e.g. "yes", a name, a number,
a hostname or cluster id), or is a direct instruction that only makes sense
as-is, return it verbatim. Never refuse and never state that the input is not a
prompt — if there is nothing to improve, output the draft unchanged. Do not
modify technical information provided by the user such as application names,
environments, server names, identifiers, commands, or code.
```

---

### Task 3 — `components/middleware/promptenhance/middleware.go` (MODIFY)

#### 3a. Replace `findLastUserMessage` with `findLastUserMessageIndex`

```go
func findLastUserMessageIndex(msgs []*schema.Message) int {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i] != nil && msgs[i].Role == schema.User {
			return i
		}
	}
	return -1
}
```

#### 3b. Add clone/apply/mark helpers

```go
// cloneMessage returns a shallow copy of m with a freshly-allocated Extra map,
// so that marker mutations applied to the clone never leak back to the
// caller-owned original. Other reference fields (ToolCalls, MultiContent,
// ReasoningContent, ResponseMeta) are shallow-copied: user messages have nil
// ToolCalls and none of these fields are mutated by this middleware, so a deep
// copy of them is unnecessary.
func cloneMessage(m *schema.Message) *schema.Message {
	if m == nil {
		return nil
	}
	c := *m
	if m.Extra != nil {
		c.Extra = make(map[string]any, len(m.Extra))
		for k, v := range m.Extra {
			c.Extra[k] = v
		}
	}
	return &c
}

// applyEnhanced replaces state.Messages[idx] with a clone carrying content and
// the enhanced marker, leaving the caller's original untouched.
func applyEnhanced(state *adk.ChatModelAgentState, idx int, content string) {
	c := cloneMessage(state.Messages[idx])
	if c == nil {
		return
	}
	c.Content = content
	markEnhanced(c)
	state.Messages[idx] = c
}

// markSkipped replaces state.Messages[idx] with a clone marked enhanced, so the
// original is untouched while the skip stays idempotent within the run.
func markSkipped(state *adk.ChatModelAgentState, idx int) {
	c := cloneMessage(state.Messages[idx])
	if c == nil {
		return
	}
	markEnhanced(c)
	state.Messages[idx] = c
}
```

Keep `isEnhanced` and `markEnhanced` unchanged (they delegate to
`memory.HasBoolMarker` / `memory.SetBoolMarker`).

#### 3c. Rewrite `BeforeModelRewriteState`

```go
func (m *Middleware) BeforeModelRewriteState(
	ctx context.Context,
	state *adk.ChatModelAgentState,
	mc *adk.ModelContext,
) (context.Context, *adk.ChatModelAgentState, error) {
	if state == nil || len(state.Messages) == 0 {
		return ctx, state, nil
	}

	idx := findLastUserMessageIndex(state.Messages)
	if idx < 0 {
		return ctx, state, nil
	}

	lastUser := state.Messages[idx]
	if isEnhanced(lastUser) {
		return ctx, state, nil
	}

	history := state.Messages[:idx]

	choice := getChoiceFromCtx(ctx)
	if choice != nil {
		return m.applyResume(ctx, state, choice, idx, history)
	}

	if m.shouldEnhance != nil && !m.shouldEnhance(ctx) {
		markSkipped(state, idx)
		return ctx, state, nil
	}

	enhanced, err := m.enhancer.EnhanceInContext(ctx, history, lastUser.Content)
	if err != nil {
		return ctx, state, errors.Wrap(err, "promptenhance: enhancement failed")
	}

	if enhanced == "" || enhanced == lastUser.Content {
		markSkipped(state, idx)
		return ctx, state, nil
	}

	if m.autoAccept {
		applyEnhanced(state, idx, enhanced)
		return ctx, state, nil
	}

	// Interrupt path: DO NOT mutate state.Messages (preserves the invariant
	// asserted by TestMiddleware_BeforeModelRewriteState_FirstCall).
	return ctx, state, &InterruptError{
		InterruptInfo: InterruptInfo{
			Original: lastUser.Content,
			Enhanced: enhanced,
		},
	}
}
```

#### 3d. Rewrite `applyResume`

```go
func (m *Middleware) applyResume(
	ctx context.Context,
	state *adk.ChatModelAgentState,
	choice *Choice,
	idx int,
	history []*schema.Message,
) (context.Context, *adk.ChatModelAgentState, error) {
	switch choice.Action {
	case "original", "skip_always":
		markSkipped(state, idx)
	case "enhanced":
		enhanced, err := m.enhancer.EnhanceInContext(ctx, history, state.Messages[idx].Content)
		if err != nil {
			return ctx, state, errors.Wrap(err, "promptenhance: re-enhancement failed")
		}
		if enhanced == "" {
			markSkipped(state, idx)
			return ctx, state, nil
		}
		applyEnhanced(state, idx, enhanced)
	case "modified":
		if choice.Text == "" {
			return ctx, state, errors.New("promptenhance: modified action requires text")
		}
		applyEnhanced(state, idx, choice.Text)
	default:
		return ctx, state, errors.Errorf("promptenhance: unknown action %q", choice.Action)
	}

	return ctx, state, nil
}
```

Notes:
- `history` is `state.Messages[:idx]` — a read-only sub-slice; `EnhanceInContext`
  only reads it, so sharing the backing array is safe.
- The empty-enhancement guard is added to the `"enhanced"` branch as well
  (consistent with design decision #4; an empty enhancement is never meaningful).
- Error wrapping uses `emperror.dev/errors` with operation context, matching the
  existing style.

---

### Task 4 — `components/middleware/promptenhance/types.go` (NO CHANGE)

No modification required. Do NOT add `ShouldEnhanceState` / a state hook.

---

### Task 5 — Tests

#### 5a. `libs/promptenhance/prompt_enhance_test.go` (MODIFY — add tests)

Add to `TestNewEnhancer` (or as a new function `TestNewEnhancer_MaxContextMessages`):

- `"default when unset"`: `NewEnhancer(ctx, &Config{Model: &mockModel{}})` →
  assert `e.maxContextMessages == 6`.
- `"negative treated as default"`: `NewEnhancer(ctx, &Config{Model: &mockModel{}, MaxContextMessages: -5})`
  → succeeds, assert `e.maxContextMessages == 6`.
- `"explicit value respected"`: `MaxContextMessages: 2` → assert `== 2`.

Add `TestEnhancer_EnhanceInContext` with subtests (use a `mockModel` whose
`generateFunc` captures `input` into an outer var):

- `"renders context and draft"`: history `[user("first"), assistant("reply")]`,
  draft `"re run"`, `MaxContextMessages: 6`. Assert result equals the model's
  fixed return `"rewritten"`; assert the captured user message (`input[len(input)-1]`)
  `Content` contains `"first"`, `"reply"`, `"<context>"`,
  `"<draft>re run</draft>"`, and `"Recent conversation"`.
- `"respects max context messages"`: history of 4 messages
  `[user("a"), assistant("b"), user("c"), assistant("d")]`, `MaxContextMessages: 2`.
  Assert user content contains `"c"` and `"d"` but NOT `"a"` / `"b"`.
- `"skips nil and empty"`: history `[nil, {Role: user, Content: ""}, {Role: user, Content: "kept"}]`.
  Assert user content contains `"kept"` and does not contain a bare `": "` empty
  line for the nil/blank entries (simplest: assert `"kept"` present, and that
  `strings.Contains(content, "\n\nUser:")` is false for consecutive blanks — or
  simply assert content contains `"kept"` and `"<context>"`).
- `"empty draft short circuit"`: `EnhanceInContext(ctx, history, "")` returns
  `("", nil)` and the model's `generateFunc` is never called (have it
  `t.Fatal("model should not be called")` if invoked).
- `"no history uses legacy format"`: `EnhanceInContext(ctx, nil, "draft")` (or
  empty history) → captured user content contains `"Draft prompt to enhance, not answer"`
  and `<draft>draft</draft>` and does NOT contain `"<context>"`.
- `"model error propagation"`: `generateFunc` returns error → `EnhanceInContext`
  returns a wrapped error (assert `err != nil`).

Existing `TestEnhancer_Enhance`, `TestClean`, `TestNewEnhancer` subtests remain
unchanged and must still pass (the `Enhance` wrapper now delegates to
`EnhanceInContext(nil, draft)`, which preserves behavior).

#### 5b. `components/middleware/promptenhance/middleware_test.go` (MODIFY — add tests)

Add (same package, so unexported `enhancedMarkerKey` is accessible):

- `TestMiddleware_AutoAccept_DoesNotMutateOriginal`:
  - Build `mw` via `newTestMiddleware(t, "enhanced version", nil, true, nil)`.
  - `orig := user("original draft")`; set `orig.Extra = map[string]any{"custom": "value"}`.
  - `state := &adk.ChatModelAgentState{Messages: []*schema.Message{orig}}`.
  - Run `BeforeModelRewriteState`; assert no error.
  - Assert `state.Messages[0] != orig` (replaced by a clone).
  - Assert `orig.Content == "original draft"` (unchanged).
  - Assert `!isEnhanced(orig)` and `_, ok := orig.Extra[enhancedMarkerKey]; !ok`
    (marker did NOT leak — proves deep copy of `Extra`).
  - Assert `orig.Extra["custom"] == "value"` still present.
  - Assert `state.Messages[0].Content == "enhanced version"` and
    `isEnhanced(state.Messages[0])`.

- `TestMiddleware_PassesConversationContext`:
  - Construct a custom `fakeModel` whose `generateFunc` captures `input` and
    returns `&schema.Message{Content: "rewritten last"}`.
  - Build enhancer with `NewEnhancer(&libspromptenhance.Config{Model: mock})`
    (default `MaxContextMessages` 6) and middleware with `AutoAccept: true`.
  - `state` = `[user("first message"), assistant("reply text"), user("last message")]`.
  - Run; assert no error; assert captured user message `Content` contains
    `"first message"`, `"reply text"`, `"<context>"`, and
    `"<draft>last message</draft>"` (i.e., only the last message is the draft).

- `TestMiddleware_EmptyEnhancedIsNoOp`:
  - Custom `fakeModel` returning `&schema.Message{Role: schema.Assistant, Content: ""}`.
  - `AutoAccept: true`, `orig := user("original draft")`, state `[orig]`.
  - Run; assert no error; assert `state.Messages[0] != orig`,
    `state.Messages[0].Content == "original draft"`, `isEnhanced(state.Messages[0])`,
    and `orig.Content == "original draft"` + `!isEnhanced(orig)`.

- `TestMiddleware_ShouldEnhanceFalse_DoesNotMutateOriginal` (skip-branch coverage):
  - `mw := newTestMiddleware(t, "", nil, false, func(context.Context) bool { return false })`.
  - `orig := user("original draft")`, state `[orig]`.
  - Run; assert `state.Messages[0] != orig`, `orig.Content` unchanged,
    `!isEnhanced(orig)`, `isEnhanced(state.Messages[0])`.

Existing middleware tests must continue to pass unchanged. They check
`state.Messages[0].Content` and `isEnhanced(state.Messages[0])`, which remain
valid because we now replace the slice element with a clone; verify each still
holds (they do — see verification below). In particular
`TestMiddleware_BeforeModelRewriteState_FirstCall` still asserts no mutation
before interrupt, which the non-mutating InterruptError path preserves.

---

### Task 6 — README updates (CONTRIBUTING requires README per component)

#### 6a. `libs/promptenhance/README.md` (MODIFY)

- Add a "Usage" example for `EnhanceInContext` (show `history []*schema.Message`
  and note `Enhance` remains available as a no-context wrapper).
- Add `MaxContextMessages` to the Configuration table:

  | Field | Required | Description |
  |-------|----------|-------------|
  | `Model` | Yes | Small/fast model for enhancement (claude-haiku, gemini-flash, gpt-5-nano) |
  | `SystemPrompt` | No | Override the default enhancement system prompt |
  | `MaxContextMessages` | No | Max prior messages included as conversation context (default 6; 0/negative → default; context is embedded in the user message, bounded for token/cost control) |

- Add a short note under "How it works" that context is rendered as a
  role-labelled transcript inside the user message and is never answered.

#### 6b. `components/middleware/promptenhance/README.md` (MODIFY)

- Add a note that the middleware passes prior conversation context to the
  enhancer (`EnhanceInContext`) and that it **never mutates the caller's message
  objects** — the user's original text is preserved in persisted history; the
  model sees an enhanced clone.
- Optionally note the token/cost implication is bounded by `MaxContextMessages`
  (default 6), configured on the `libs/promptenhance.Config`.

---

## Error handling & wrapping rules

- All enhancer model errors wrapped with `emperror.dev/errors.Wrap(err, ...)`
  with operation context (`"promptenhance: model generation failed"`,
  `"promptenhance: enhancement failed"`, `"promptenhance: re-enhancement failed"`).
- `modified` action with empty `Text` → `errors.New("promptenhance: modified action requires text")`.
- Unknown action → `errors.Errorf("promptenhance: unknown action %q", ...)`.
- No panics; nil state/message/`idx<0`/nil history handled as early returns or
  no-ops as shown above.

## Validation requirements

- `Config.MaxContextMessages` has `validate:"gte=0"` and a `jsonschema`
  description.
- `NewEnhancer` calls `libs/toolkit/validate.Struct(cfg)` AFTER applying
  defaults/clamp (CONTRIBUTING rule).
- `NewMiddleware` already calls `validate.Struct` on its copied `Config`; no
  change needed there.

## Verification commands

```bash
cd /projects/eino-ext

# format check (expect no output)
gofmt -l \
  libs/promptenhance/prompt_enhance.go \
  libs/promptenhance/prompt_enhance_test.go \
  components/middleware/promptenhance/middleware.go \
  components/middleware/promptenhance/middleware_test.go

# build / vet
go build ./...
go vet ./...

# targeted tests
go test ./libs/promptenhance/... ./components/middleware/promptenhance/...

# full suite (CONTRIBUTING PR gate)
go test ./...
```

Optional lint: `golangci-lint run` (`.golangci.yml` enables `misspell` and
`revive`; keep exported comments on `EnhanceInContext` and `Config` fields).

## Data flow after fix (for the implementer's mental model)

```
turn user msg (caller-owned pointer P, Content="re run the command")
  -> runner state.Messages = [ ...history..., P ]
  -> BeforeModelRewriteState:
        idx = findLastUserMessageIndex(...)
        history = state.Messages[:idx]
        enhanced = EnhanceInContext(history, "re run the command")   # context-aware
        state.Messages[idx] = clone(P){Content: enhanced, +marker}   # P untouched
  -> model sees enhanced clone
  -> caller persists P (original text)                               # history integrity
```

## Risks / notes for release

- Default `MaxContextMessages=6` changes default behavior vs legacy
  (context-blind). Intended and desired; call out in release notes.
- Token/cost per turn increases (context sent to the small model), bounded by
  `MaxContextMessages`.
- Enhanced text is no longer persisted; only the user's original is stored.
  Intended (history integrity) — note in changelog for any consumer relying on
  persisted-enhanced text.
- `cloneMessage` MUST deep-copy `Extra`; a shallow map copy would leak the
  enhanced marker back to the caller's original.

## Files touched (summary)

- MODIFY `libs/promptenhance/prompt_enhance.go`
- MODIFY `libs/promptenhance/prompts/enhance_system.md`
- MODIFY `components/middleware/promptenhance/middleware.go`
- MODIFY `libs/promptenhance/prompt_enhance_test.go`
- MODIFY `components/middleware/promptenhance/middleware_test.go`
- MODIFY `libs/promptenhance/README.md`
- MODIFY `components/middleware/promptenhance/README.md`
- NO CHANGE `components/middleware/promptenhance/types.go`
