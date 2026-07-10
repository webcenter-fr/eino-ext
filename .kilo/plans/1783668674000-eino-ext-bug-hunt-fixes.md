# Plan: eino-ext bug-hunt fixes (streaming usage, memory stream race, conversation timestamp)

> INTENDED LOCATION: `/projects/eino-ext/.kilo/plans/1783668674000-eino-ext-bug-hunt-fixes.md`
> Written here instead because this session's edit permissions are scoped to the
> `rancher-doc-chat-api-k8s` workspace. Move/copy this file into the eino-ext repo,
> or reopen `/projects/eino-ext` as the workspace to have it written there directly.
> This plan is self-contained and targets ONLY `/projects/eino-ext`.

## Goal

Fix functional/performance defects found during a cross-repo bug hunt that live
in `/projects/eino-ext`. These were surfaced by the `rancher-doc-chat-api-k8s`
consumer (provider `github-copilot`, streaming enabled) but the fixes belong in
this library.

## Relationship to existing plans

This plan is **additive to** `/projects/eino-ext/.kilo/plans/1783671485912-copilot-parity-fix.md`,
whose items appear already implemented in the current tree (finish-reason
mapper, `tool_choice`, `max_tokens`, vision, reasoning round-trip, Responses
API, `x-initiator`). None of the tasks below duplicate that plan:

- **E1 (streaming token usage) is NOT covered by the parity plan** and is the
  highest-impact defect here.
- E2/E3/E4 are hardening on top of the now-implemented streaming/Responses code.
- E5/E6 are in `components/agent/memory`; C1 is in `components/memory`.

All claims below were verified against the current source and the vendored eino
`v0.9.12` in the module cache.

---

## Task list (ordered)

### E1 — HIGH. Copilot chat streaming never produces token usage

**Files**: `components/model/copilot/copilot_chat.go`, `copilot_stream.go`

**Evidence**:
- `streamEvents()` (`copilot_stream.go:73-200`) parses each SSE chunk into
  `copilotChatChunk`, which has a `Usage *copilotUsage` field
  (`copilot_chat.go:118-123`), but **never references `chunk.Usage`** (grep for
  `chunk.Usage` in the package returns nothing).
- `copilotChatRequest` (`copilot_chat.go:89-100`) has **no `stream_options`
  field**, so the streaming request never opts into usage. OpenAI-compatible
  streaming omits usage unless `stream_options.include_usage=true` is sent.
- With `EnableStreaming: true` the ADK calls `Stream()`, not `Generate()`
  (verified: `eino@v0.9.12/adk/chatmodel.go:1057`). So streaming is the live
  path and its messages carry no `TokenUsage`/`ResponseMeta.Usage`.
- Consumer impact: any `activity` Handler / metrics collector reading
  `ResponseMeta.Usage` sees nil → zero tokens and zero cost for every streamed
  step. (Non-streaming `Generate` already maps usage via `usageToTokenUsage`,
  `copilot_chat.go:617-619`; only streaming is broken.)

**Fix (both parts required)**:
1. Add streaming usage opt-in to the request:
   ```go
   type copilotStreamOptions struct {
       IncludeUsage bool `json:"include_usage"`
   }
   // in copilotChatRequest:
   StreamOptions *copilotStreamOptions `json:"stream_options,omitempty"`
   ```
   In `buildChatRequest`, when `stream == true`, set
   `req.StreamOptions = &copilotStreamOptions{IncludeUsage: true}`.
2. In `streamEvents`, emit usage when a chunk carries it. A usage-only final
   chunk typically has an empty `Choices` array, so handle it independently of
   the choice loop:
   ```go
   if chunk.Usage != nil {
       sw.Send(&schema.Message{
           Role:         schema.Assistant,
           ResponseMeta: &schema.ResponseMeta{Usage: usageToTokenUsage(chunk.Usage)},
       }, nil)
   }
   ```

**Watch-out**: confirm the GitHub Copilot gateway honors
`stream_options.include_usage`. The opt-in is harmless if ignored; if the
gateway never returns streaming usage, a tokenizer-based estimate is a
follow-up (out of scope here).

**Validation**: unit test with an SSE body whose final chunk includes a `usage`
object → assert the emitted stream yields a message with non-zero
`ResponseMeta.Usage`. Extend `copilot_stream_test.go`.

---

### E2 — LOW (defensive). Accumulated tool calls can be dropped on `[DONE]`

**File**: `components/model/copilot/copilot_stream.go`

**Evidence**: `streamEvents` emits accumulated tool calls only inside the
`choice.FinishReason != nil` branch (`:168-187`). On `[DONE]` it returns
immediately (`:96-98`). If a gateway ever sends tool-call deltas then `[DONE]`
without a finish-reason chunk, `toolAccum` is silently discarded. Normal
OpenAI-compatible flows send `finish_reason: tool_calls` first, so this is
defensive.

**Fix**: before `return nil` on `[DONE]`, flush remaining `toolAccum` entries
(sorted by index) as a final assistant message with `ToolCalls`.

**Validation**: existing streaming tool-call tests still pass; add a
`[DONE]`-without-finish-reason regression case.

---

### E3 — LOW (GPT-5 Responses path only). Responses-stream function-call accumulation is fragile

**File**: `components/model/copilot/copilot_responses_stream.go`

**Evidence**: accumulators are created keyed by item ID
(`funcArgsAccum[evt.Item.ID]`, `:151`), but the
`response.function_call_arguments.delta` handler ignores the key and instead
linearly scans with a two-branch `acc.id`/`acc.callID` match (`:181-196`). With
parallel function calls this can append a delta to the wrong accumulator. Only
reachable for GPT-5-class models (`useResponsesAPI`, `copilot_chat.go:697-712`);
the current consumer config uses none.

**Fix**: replace the scan with a direct keyed lookup:
```go
case "response.function_call_arguments.delta":
    if evt.Item == nil || evt.Item.ID == "" {
        continue
    }
    if acc, ok := funcArgsAccum[evt.Item.ID]; ok {
        acc.args += evt.Delta
    }
```
Use the same `funcArgsAccum[evt.Item.ID]` key in `response.output_item.done`.

**Validation**: responses-stream tests; add a two-parallel-tool-call test.

---

### E4 — LOW (GPT-5 Responses path only). Un-emitted function calls leak at end of Responses stream

**File**: `components/model/copilot/copilot_responses_stream.go`

**Evidence**: function calls are emitted only on `response.output_item.done`
(`:216-241`). If the stream ends (scanner end or `[DONE]`) before that event,
entries left in `funcArgsAccum` are never emitted.

**Fix**: before the final `return nil`, flush remaining `funcArgsAccum` entries
as assistant tool-call messages (preserving `copilot_item_id` in `Extra` as the
`done` path does).

**Validation**: responses-stream tests; add a truncated-stream test.

---

### E5 — MEDIUM. MemoryAgent shares one stream reader with the downstream consumer

**File**: `components/agent/memory/agent.go`

**Evidence**: In `monitorRun` (`:234-262`) each event is forwarded with
`outGen.Send(event)` (`:242`) and the **same** `mo.MessageStream` is then read
by `collectStream(mo.MessageStream)` (`:253`). Neither `AsyncGenerator.Send`
(`eino@v0.9.12/adk/utils.go:43`) nor the runner's forwarding loop
(`adk/runner.go:286-341`) copies the stream, so the forwarded event and
`collectStream` hold the same `*schema.StreamReader`. The two consumers race;
whichever drains first wins and the other gets EOF. Net effect: memory
extraction from streaming assistant messages is non-deterministic (often
empty). (`MessageOutput` is a pointer — `adk/interface.go:73-107` — so it can be
mutated before `Send`.)

**Fix**: split the stream before forwarding, mirroring the framework's own
pattern (`adk/utils.go:266` uses `.Copy(2)`):
```go
if mo.IsStreaming && mo.MessageStream != nil {
    copies := mo.MessageStream.Copy(2)
    mo.MessageStream = copies[0] // forwarded downstream
    outGen.Send(event)
    if msg, err := a.collectStream(copies[1]); err == nil && msg != nil {
        assistantMsgs = append(assistantMsgs, msg)
    }
    continue
}
outGen.Send(event) // non-streaming path unchanged
```
Restructure `monitorRun` so the streaming branch forwards + collects from the
two copies and the non-streaming branch keeps today's behavior. Do **not** call
`outGen.Send(event)` twice for the same event.

**Validation**: unit test with a streaming assistant message asserting both the
forwarded stream and the collected message contain the full text (`agent_test.go`).

---

### E6 — LOW (quality). Memory retrieval query uses only the last user message

**File**: `components/agent/memory/agent.go:177-184`

**Evidence**: `buildQuery` returns only the most recent `schema.User` message,
so multi-turn context is dropped from retrieval matching.

**Fix**: build the query from the last N (e.g. 2) user messages joined in
chronological order; keep N small to bound embedding/query cost.

**Validation**: multi-turn test confirming earlier-turn context influences
retrieval.

---

### C1 — MEDIUM. Conversation `UpdatedAt` is stored but not exposed (breaks consumer "time ago")

**Files**: `components/memory/conversation.go`, `components/memory/opensearch/opensearch.go`, `components/memory/file/file.go`

**Evidence**: `conversationDoc.UpdatedAt` is persisted
(`opensearch/opensearch.go:62`, stamped at `:75`), but the in-memory
`OpenSearchConversation` struct (`:42-55`) has **no `UpdatedAt` field**, and
`GetConversation` (`:205-220`) / `Load` (`:402-421`) unmarshal the doc yet copy
only `Messages`/`Activities` — `doc.UpdatedAt` is dropped. The
`memory.Conversation` interface (`conversation.go:28-62`) exposes no getter, so
a consumer cannot render a last-updated timestamp for the OpenSearch backend.

**Fix**:
- Add `UpdatedAt string` to `OpenSearchConversation`.
- Set `conv.UpdatedAt = doc.UpdatedAt` in `GetConversation` (`:215-218`) and
  `Load` (`:420-421`).
- Add `GetUpdatedAt() string` to the `Conversation` interface.
- Implement `GetUpdatedAt()`:
  - `OpenSearchConversation` → return `c.UpdatedAt` (RFC3339 as stored).
  - `FileConversation` (`file/file.go`) → return the backing `.jsonl` file's
    mod time (RFC3339), or a tracked timestamp; `""` is acceptable if a backend
    genuinely has none.
- The `session.Turn` (`components/memory/session/session.go:199,213`) only wraps
  a `Conversation` reference, so it needs **no** change. Grep confirms the only
  in-tree implementers are the file + opensearch conversations, so this is the
  full blast radius of the interface change.
- Update the memory package README / doc comments for the new interface method
  (per CONTRIBUTING doc-sync).

**Note**: adding a method to an exported interface is a breaking change for any
out-of-tree implementer; call this out in the PR description.

**Validation**: `opensearch_test.go` — save then reload a conversation and
assert `GetUpdatedAt()` returns the persisted RFC3339 timestamp; file backend
test asserts a non-empty timestamp (or documented `""`).

---

## Implementation order

1. **E1** — streaming token usage (highest impact; unblocks consumer cost/token
   telemetry).
2. **E5** — MemoryAgent stream copy (deterministic memory extraction).
3. **C1** — `Conversation.GetUpdatedAt` interface + backend implementations.
4. **E6** — multi-message memory query.
5. **E2** — chat-stream tool-call flush (defensive).
6. **E3 + E4** — Responses-stream fixes (GPT-5 only; apply together).

## Validation (whole plan)

```bash
cd /projects/eino-ext && go build ./... && go vet ./... \
  && go test ./components/model/copilot/... \
  && go test ./components/agent/memory/... \
  && go test ./components/memory/... \
  && go test ./callbacks/activity/...
```

All tests must pass with mock servers only (no live network).

## Cross-cutting conventions

- Wrap new errors with `emperror.dev/errors` including operation context.
- Keep `schema.Pipe`/goroutine back-pressure semantics identical in streaming
  paths.
- Preserve exported API compatibility except the intentional `Conversation`
  interface addition (C1), which must be documented.
- Any config/behavior/interface change must update the relevant `README.md`
  and package docs in the same change (CONTRIBUTING doc-sync).

## Downstream coordination

After these land, the consumer `rancher-doc-chat-api-k8s` must bump its
`webcenter-fr/eino-ext` pin and complete its side of C1 (add `updatedAt` to its
`conversationResponse` and populate it from `Conversation.GetUpdatedAt()`), plus
verify the E1-dependent cost/context UI. Those consumer tasks are tracked in
`rancher-doc-chat-api-k8s/.kilo/plans/1783674630402-bug-hunt-fixes.md` and are
out of scope for this repo's plan.

> Implementation requires source-file edits; switch to an implementation-capable
> agent to execute this plan.
