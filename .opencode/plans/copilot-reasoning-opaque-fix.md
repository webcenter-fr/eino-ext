# Copilot Streaming: Fix Base64 Reasoning Display Bug

## Goal

Fix a bug in `copilot_stream.go` where the Copilot streaming handler displays
**base64-encoded encrypted content** (`reasoning_opaque`) as human-readable
reasoning text in the UI when the model returns `reasoning_opaque` but not
`reasoning_text`.

### Root Cause

In `copilot_stream.go`, the streaming SSE handler processes Copilot delta
chunks. Some models (e.g., certain Claude/GPT-5 variants) return a
`reasoning_opaque` field — an **encrypted/binary blob** (base64-encoded) meant
for cross-turn round-tripping back to the API — but do **not** always return
the plaintext `reasoning_text` summary in the same chunk (or at all).

The current code at `copilot_stream.go:162-166` falls back to
`reasoning_opaque` when `reasoning_text` is empty:

```go
if delta.ReasoningText != "" || delta.ReasoningOpaque != "" {
    reasoningContent := delta.ReasoningText
    if reasoningContent == "" {
        reasoningContent = delta.ReasoningOpaque  // BUG: base64 binary, not human-readable
    }
    ...
    sw.Send(&schema.Message{
        Role:             schema.Assistant,
        ReasoningContent: reasoningContent,       // base64 garbage → UI
    }, nil)
}
```

This raw opaque content then flows through `schema.Message.ReasoningContent`,
gets picked up by the activity handler (`handler.go:356`), serialized into
`reasoning.delta` SSE events, and rendered in the browser as gibberish
base64 text.

By contrast:

- The **non-streaming** path (`copilot_chat.go:409`) correctly stores
  `ReasoningOpaque` only in `Extra` for round-trip, and only uses
  `ReasoningText` to populate `ReasoningContent`.
- The `/responses` streaming path (`copilot_responses_stream.go`) correctly
  stores `EncryptedContent` only in `Extra` and only uses the plaintext
  `summary_text` (item `Summary` or `reasoning_summary_text.delta`) for
  `ReasoningContent` — it never exposes encrypted content as display text.

## Affected files (absolute paths from repo root)

### Modify

- `/projects/eino-ext/components/model/copilot/copilot_stream.go` — fix the
  reasoning fallback logic (lines 158-178).

### No changes required

- `copilot_chat.go` — the non-streaming path is already correct.
- `copilot_responses_stream.go` — the /responses path is already correct.
- `copilot_stream_test.go` — no existing test covers both fields; add new
  tests in the new test file.
- `go.mod` — no dependency changes.

### Create

- (No new files strictly required; add a test case to
  `/projects/eino-ext/components/model/copilot/copilot_stream_test.go` if one
  exists, or add inline verification in the plan.)

## Data structures

No new types. The `copilotDelta` struct already has both fields:

```go
type copilotDelta struct {
    ...
    ReasoningText   string `json:"reasoning_text,omitempty"`
    ReasoningOpaque string `json:"reasoning_opaque,omitempty"`
}
```

And `schema.Message` already has `Extra map[string]any` for persisting
opaque/encrypted content.

## Code change (`copilot_stream.go`)

Replace the reasoning emission block (lines 158-178):

```go
// BEFORE (bug: falls back to opaque base64 content)
if delta.ReasoningText != "" || delta.ReasoningOpaque != "" {
    reasoningContent := delta.ReasoningText
    if reasoningContent == "" {
        reasoningContent = delta.ReasoningOpaque
    }
    // Persist opaque for multi-turn round-trip.
    if delta.ReasoningOpaque != "" {
        if msg.Extra == nil {
            msg.Extra = make(map[string]any)
        }
        msg.Extra["copilot_reasoning_opaque"] = delta.ReasoningOpaque
    }
    sw.Send(&schema.Message{
        Role:             schema.Assistant,
        ReasoningContent: reasoningContent,
    }, nil)
}
```

With:

```go
// AFTER: only emit human-readable reasoning_text; store opaque in Extra only
// reasoning_opaque is encrypted/binary content for cross-turn round-trip
// only — it is NOT human-readable and must never be used as the displayed
// reasoning text.
if delta.ReasoningText != "" {
    // Persist opaque for multi-turn round-trip.
    if delta.ReasoningOpaque != "" {
        if msg.Extra == nil {
            msg.Extra = make(map[string]any)
        }
        msg.Extra["copilot_reasoning_opaque"] = delta.ReasoningOpaque
    }
    sw.Send(&schema.Message{
        Role:             schema.Assistant,
        ReasoningContent: delta.ReasoningText,
    }, nil)
}
```

`ReasoningOpaque` is still persisted in `msg.Extra["copilot_reasoning_opaque"]`
so that the existing `convertMessage` (`copilot_chat.go:252-255`) can send it
back to the API on the next turn — the round-trip contract is preserved. Only
the display path is fixed.

## Test plan

Add the following test cases to `copilot_stream_test.go` (or a new test file):

1. **`TestStreamReasoningTextOnly`** — stub SSE events with only
   `reasoning_text` ("I should think about this..."); assert the stream emits
   messages with `ReasoningContent` set and no `Extra["copilot_reasoning_opaque"]`.

2. **`TestStreamReasoningOpaqueOnly`** — stub SSE events with only
   `reasoning_opaque` (a base64 string like `"ZXhhbXBsZQ=="`); assert the
   stream emits **no** messages with `ReasoningContent` set (the opaque
   content is never shown), but the opaque value IS stored in
   `Extra["copilot_reasoning_opaque"]`.

3. **`TestStreamReasoningBothFields`** — stub SSE events with both
   `reasoning_text` ("Hello") and `reasoning_opaque` ("Z29vZGJ5ZQ==");
   assert `ReasoningContent == "Hello"` and
   `Extra["copilot_reasoning_opaque"] == "Z29vZGJ5ZQ=="`.

4. **`TestStreamReasoningOpaqueOnlyNonEmptyContent`** — stub SSE events where
   `reasoning_text=""` and `reasoning_opaque="dGVzdA=="` (base64 of "test");
   assert the stream emits no reasoning content (no messages with non-empty
   `ReasoningContent`).

## Non-streaming path verification

The non-streaming path in `convertChoiceToMessage` (`copilot_chat.go:402-430`)
is already correct:

```go
// Only ReasoningText populates ReasoningContent:
if msg.ReasoningText != "" {
    out.ReasoningContent = msg.ReasoningText
}
// ReasoningOpaque is stored in Extra for round-trip, never shown:
if msg.ReasoningOpaque != "" {
    if out.Extra == nil {
        out.Extra = make(map[string]any)
    }
    out.Extra["copilot_reasoning_opaque"] = msg.ReasoningOpaque
}
```

No change needed there.

## Verification checklist

Run from the repo root (`/projects/eino-ext`):

```bash
# Build
go build ./components/model/copilot/...

# Vet
go vet ./components/model/copilot/...

# Unit tests
go test ./components/model/copilot/...

# Lint
golangci-lint run ./components/model/copilot/...
```

Acceptance criteria:
- `go build ./...` succeeds
- `go vet ./...` reports no issues
- All existing tests still pass (no regressions)
- New test cases pass
- `golangci-lint` is clean for the copilot package

## Risk assessment

| Risk | Mitigation |
|------|------------|
| Models that ONLY return `reasoning_opaque` will now show no reasoning in the UI | This is the correct behavior — opaque content is encrypted binary and was never intended for display. The `/responses` path already behaves this way. |
| Breaking the round-trip for multi-turn conversations with reasoning | Not affected — `reasoning_opaque` is still stored in `Extra["copilot_reasoning_opaque"]` and `convertMessage` still sends it back to the API. Only the display path changes. |
| Existing callers relying on `ReasoningContent` for opaque content | The non-streaming `convertChoiceToMessage` already uses only `ReasoningText` (not `ReasoningOpaque`) for `ReasoningContent`. The streaming path was the outlier. |
