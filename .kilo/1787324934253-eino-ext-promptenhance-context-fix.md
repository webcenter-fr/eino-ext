# Fix prompt-enhance context loss & history corruption (webcenter-fr/eino-ext)

## Target repository

`github.com/webcenter-fr/eino-ext` (the module vendored by rancher-doc-chat-api-k8s).
Currently pinned by the consumer at `v0.0.0-20260820053534-312420f6d0bf`.

All file paths below are relative to the **eino-ext repo root**.

## Problem (root cause)

When a user continues an existing conversation with a terse, context-dependent
follow-up (e.g. "re run the command", or answering "which cluster?" with
"ran37hpd2"), the assistant appears to "lose context" and asks confused
clarifying questions.

It is **not** a memory/history-window bug. The consumer proved (diagnostic log)
that full history is loaded and injected: `full_msgs=12 window_msgs=13
window_tokens=1876 budget=95200`. The defect is in the `promptenhance` package:

1. **Context-blind enhancement.** The middleware enhances the last user message
   using **only that message's raw string**, with no conversation context:
   - `components/middleware/promptenhance/middleware.go:78`
     `enhanced, err := m.enhancer.Enhance(ctx, lastUser.Content)`
   - `libs/promptenhance/prompt_enhance.go:68` `Enhance(ctx, draft string)`
   For a follow-up like "re run the command" the small model rewrites it into a
   different/inverted request ("Provide the specific command you want to
   re-run…"); for a bare identifier "ran37hpd2" it **refuses** with meta-text
   ("…appears to be a code or identifier rather than a prompt to enhance…"),
   violating its own instruction. The supervisor then receives nonsense.

2. **In-place mutation corrupts persisted history.** The enhanced text is written
   back onto the caller-owned `*schema.Message` pointer:
   - `middleware.go:89` `lastUser.Content = enhanced` (AutoAccept path)
   - `middleware.go:115` `lastUser.Content = enhanced` (resume "enhanced")
   - `middleware.go:120` `lastUser.Content = choice.Text` (resume "modified")
   The consumer persists that same pointer, so the stored conversation shows the
   mangled text instead of what the user typed, compounding confusion on later
   turns.

Real evidence (consumer export, conversation `19c89a0c…`): user messages #8 and
#12 in storage are enhancer output, not what the user typed.

## Goals

- Enhance follow-ups **in the context of the conversation** so references resolve
  and short/technical replies are preserved (never refused).
- Guarantee the middleware **never mutates the caller's message object**, so the
  consumer's persisted history always keeps the user's original text.
- Keep the public constructor API (`NewEnhancer`, `NewMiddleware`) source-compatible
  for the consumer (it only constructs these; it does not call `Enhance` directly —
  verified in `internal/server/agent/chat.go:601,608`).

## Non-goals / out of scope

- Any change in the rancher consumer repo (separate follow-up: remove the
  temporary `[HistoryDiag]` log in `internal/server/agent.go`; optionally gate
  enhancement to first-turn-only via `ShouldEnhance`).
- Changing the memory/window/summarization subsystem.
- Reworking the interrupt/approval UX flow.

## Design decisions

1. **Add a context-aware enhancement method (additive, non-breaking).**
   In `libs/promptenhance`, add
   `EnhanceInContext(ctx context.Context, history []*schema.Message, draft string) (string, error)`.
   Keep the existing `Enhance(ctx, draft)` as a thin wrapper
   (`return e.EnhanceInContext(ctx, nil, draft)`) so any external caller and the
   existing lib test keep working. The middleware switches to `EnhanceInContext`.

2. **Bound the context.** Add `Config` fields to cap context size and cost:
   - `MaxContextMessages int` (default 6) — number of most-recent prior messages
     included as context; `0` disables context (legacy behavior).
   - Render context as a compact transcript embedded in the single user message
     (role-labelled lines), NOT as real role messages, to keep the model in
     "rewrite, don't answer" mode.

3. **Never mutate the caller's message.** In the middleware, when applying an
   enhanced/modified/marked result, replace `state.Messages[idx]` with a **clone**
   (`cloneMessage`) whose `Content` (and enhanced marker) are set on the clone.
   The caller's original pointer is left untouched → persisted history keeps the
   user's real text; the model sees the enhanced clone. Apply this to **every**
   branch that currently calls `markEnhanced(lastUser)` or assigns
   `lastUser.Content`, so no branch ever writes to the input element.

4. **Safety guards.** In `EnhanceInContext`: keep the empty-draft short-circuit;
   in the middleware keep "enhanced == original ⇒ skip", and add "enhanced == \"\"
   ⇒ skip (treat as no-op)". Update the default system prompt to forbid refusals
   and require verbatim pass-through when there is nothing to improve.

5. **System prompt rewrite** (`libs/promptenhance/prompts/enhance_system.md`) to
   describe context usage, reference resolution, pass-through of short/technical
   inputs, and an explicit "never refuse / never say this isn't a prompt" rule.

## Implementation tasks (ordered)

### 1. `libs/promptenhance/prompt_enhance.go`
- Add fields to `Config`:
  - `MaxContextMessages int` (jsonschema description; validate `gte=0`).
- Store `maxContextMessages` on `Enhancer`; default to `6` in `NewEnhancer` when
  the field is `0` **only if** you want a non-zero default. Decision: default
  **6**; treat negative as `0`. (If preserving exact legacy behavior by default is
  preferred, default to `0`; but 6 is recommended so the fix is effective without
  consumer config changes.)
- Add `EnhanceInContext(ctx, history []*schema.Message, draft string) (string, error)`:
  - `if draft == "" { return "", nil }`.
  - Build the user content:
    - If `maxContextMessages > 0 && len(history) > 0`, take the last
      `min(maxContextMessages, len(history))` messages, skip nil/empty, render as:
      ```
      Recent conversation (context only — do NOT answer or continue it):
      <context>
      User: ...
      Assistant: ...
      </context>

      Draft to rewrite (rewrite ONLY this into a clear, standalone prompt,
      resolving references using the context above; do NOT answer it):
      <draft>{draft}</draft>
      ```
    - Else fall back to the current single-draft format
      (`"Draft prompt to enhance, not answer:\n\n<draft>%s</draft>"`).
  - `messages := []*schema.Message{schema.SystemMessage(e.systemPrompt), {Role: schema.User, Content: userContent}}`.
  - `e.model.Generate`, then `clean(result.Content)` (unchanged).
- Reduce `Enhance(ctx, draft)` to `return e.EnhanceInContext(ctx, nil, draft)`.
- Keep `clean`, `stripSurroundingQuotes` unchanged.

### 2. `libs/promptenhance/prompts/enhance_system.md`
Replace with (single paragraph acceptable; keep it tight):

> You rewrite the user's latest draft prompt for another assistant. You may be
> given the recent conversation as context — use it ONLY to resolve references
> (e.g. "it", "that command", "the same pod", a bare name or identifier) so you
> can turn the draft into a clear, self-contained request. Never answer,
> execute, continue, or comment on the conversation. Rewrite ONLY the final
> draft. Return only the rewritten prompt text the user could send next — no
> conversation, explanations, lead-ins, bullet points, placeholders, surrounding
> quotes, or markdown fences. If the draft is already clear, or is a short
> reply/confirmation/answer to a previous question (e.g. "yes", a name, a number,
> a hostname or cluster id), or is a direct instruction that only makes sense
> as-is, return it verbatim. Never refuse and never state that the input is not a
> prompt — if there is nothing to improve, output the draft unchanged. Do not
> modify technical information provided by the user such as application names,
> environments, server names, identifiers, commands, or code.

### 3. `components/middleware/promptenhance/middleware.go`
- Change `findLastUserMessage` → `findLastUserMessageIndex(msgs) int` (return `-1`
  when none). Update callers to use the index and `state.Messages[idx]`.
- Add helpers:
  - `cloneMessage(m *schema.Message) *schema.Message`: shallow struct copy
    (`c := *m`), then deep-copy `Extra` into a fresh map so markers set on the
    clone never leak to the caller's original. (User messages have nil ToolCalls;
    a shallow copy of other slice fields is acceptable since they are not mutated.)
  - `applyEnhanced(state, idx, content string)`: `c := cloneMessage(state.Messages[idx]); c.Content = content; markEnhanced(c); state.Messages[idx] = c`.
  - `markSkipped(state, idx)`: `c := cloneMessage(state.Messages[idx]); markEnhanced(c); state.Messages[idx] = c` (used for the no-op/skip branches so the
    original is never touched, while idempotency within the run is preserved on
    the clone in `state.Messages`).
- Rewrite `BeforeModelRewriteState`:
  - Guard nil/empty state (unchanged).
  - `idx := findLastUserMessageIndex(state.Messages); if idx < 0 { return }`.
  - `lastUser := state.Messages[idx]`; `if isEnhanced(lastUser) { return }`.
  - `history := state.Messages[:idx]`.
  - Choice path → `applyResume(ctx, state, choice, idx, history)`.
  - `shouldEnhance==false` → `markSkipped(state, idx); return`.
  - `enhanced, err := m.enhancer.EnhanceInContext(ctx, history, lastUser.Content)`.
  - On error → wrap and return (unchanged).
  - `if enhanced == "" || enhanced == lastUser.Content { markSkipped(state, idx); return }`.
  - `if m.autoAccept { applyEnhanced(state, idx, enhanced); return }`.
  - Else return `InterruptError{Original: lastUser.Content, Enhanced: enhanced}`
    WITHOUT mutating (unchanged; keep the invariant asserted by
    `TestMiddleware_BeforeModelRewriteState_FirstCall`).
- Rewrite `applyResume(ctx, state, choice, idx, history)`:
  - `"original" / "skip_always"` → `markSkipped(state, idx)`.
  - `"enhanced"` → `enhanced, err := m.enhancer.EnhanceInContext(ctx, history, state.Messages[idx].Content)`; on ok `applyEnhanced(state, idx, enhanced)`.
  - `"modified"` → require `choice.Text != ""`; `applyEnhanced(state, idx, choice.Text)`.
  - unknown → error (unchanged).

### 4. `components/middleware/promptenhance/types.go`
- No required change. (Optional, separable) To let consumers gate to first-turn
  only without ctx plumbing, you may add an additive hook
  `ShouldEnhanceStateFunc func(ctx context.Context, state *adk.ChatModelAgentState) bool`
  and a `Config.ShouldEnhanceState` field, consulted before `ShouldEnhance`.
  Mark as optional; not needed for the core fix.

### 5. Tests
- `libs/promptenhance/prompt_enhance_test.go`:
  - Existing tests keep passing (`Enhance` wrapper). 
  - Add `TestEnhancer_EnhanceInContext_IncludesHistory`: fakeModel records the
    input; assert the user message contains rendered prior messages and the draft,
    and respects `MaxContextMessages` (older messages beyond the cap absent).
  - Add `TestEnhancer_EnhanceInContext_EmptyDraft` returns "".
- `components/middleware/promptenhance/middleware_test.go`:
  - **Regression for history corruption** — `TestMiddleware_AutoAccept_DoesNotMutateOriginal`:
    keep a reference `orig := state.Messages[0]`; run AutoAccept; assert
    `orig.Content` is unchanged, `!isEnhanced(orig)`, and
    `state.Messages[0] != orig` with `state.Messages[0].Content == "enhanced version"`
    and `isEnhanced(state.Messages[0])`.
  - `TestMiddleware_PassesConversationContext`: fakeModel records input; state has
    `[user("first"), assistant("reply"), user("last")]`; assert the model input
    contains "first"/"reply" context and rewrites only "last".
  - `TestMiddleware_EmptyEnhancedIsNoOp`: enhancer returns ""; assert original
    content preserved on the clone and marked enhanced (skip path).
  - Update existing tests that assert mutation on `state.Messages[0]` to account
    for element replacement (they check `state.Messages[0].Content`, which still
    works because we replace the element; verify each still holds).
  - Keep `TestMiddleware_BeforeModelRewriteState_FirstCall` asserting no mutation
    before interrupt (must remain true).

## Data flow after fix

```
turn user msg (caller-owned pointer P, Content="re run the command")
  -> runner state.Messages = [ ...history..., P ]
  -> BeforeModelRewriteState:
        history = state.Messages[:idx]
        enhanced = EnhanceInContext(history, "re run the command")  # context-aware
        state.Messages[idx] = clone(P){Content: enhanced, +marker}  # P untouched
  -> model sees enhanced clone
  -> caller persists P (original text)  # history integrity preserved
```

## Failure modes & handling

- Enhancer/model error → return wrapped error (unchanged); caller surfaces it.
- Empty or unchanged enhancement → skip (original clone used, marked).
- Model still refuses despite prompt → mitigated by pass-through instruction;
  additionally, empty-result guard prevents a refusal that collapses to empty from
  replacing the draft. (A refusal that is non-empty text is still possible; the
  prompt change is the primary mitigation. Optional hardening: if `MaxContextMessages==0`
  and draft is very short, skip — not included by default.)
- Context rendering must skip nil messages and messages with empty Content.

## Risks

- **Token/cost increase**: context is now sent to the small model each turn.
  Bounded by `MaxContextMessages` (default 6). Document in README.
- **Behavioral change**: the enhanced prompt is no longer persisted; only the
  user's original text is stored. This is intended (history integrity). Note it in
  the changelog for any consumer relying on persisted-enhanced text.
- **Default `MaxContextMessages=6`** changes default enhancement behavior vs.
  legacy (context-blind). Acceptable and desired; call it out in release notes. If
  strict backward-compat is required, default to `0` and let the consumer opt in.
- `cloneMessage` must deep-copy `Extra`; a shallow map copy would let the enhanced
  marker leak back to the caller's original.

## Validation

- `go test ./libs/promptenhance/... ./components/middleware/promptenhance/...`
- `go build ./...`, `go vet ./...`, `gofmt -l` on changed files.
- Consumer smoke test (rancher, after bumping the module):
  1. Open an existing conversation, send "re run the command".
  2. Confirm the persisted user message equals the typed text (not enhancer
     output) and the supervisor responds in-context (no "which command?" / no
     "this isn't a prompt" style reply).
  3. Answer a clarifying question with a bare identifier (e.g. a cluster id) and
     confirm it is passed through unchanged.

## Rollout

- Land as a normal PR on eino-ext; tag/commit.
- Consumer bumps the `eino-ext` pseudo-version in `go.mod` and redeploys.
- No config change required in the consumer if `MaxContextMessages` defaults to 6;
  otherwise set it where `NewEnhancer`/`Config` is constructed.

## Open questions (resolve during implementation)

1. Default `MaxContextMessages`: **6** (recommended) vs `0` (strict legacy).
2. Whether to also add the optional `ShouldEnhanceState` hook now or defer to the
   consumer-side gating follow-up (recommended: defer; core fix is sufficient).
