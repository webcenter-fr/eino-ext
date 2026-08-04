# Plan: Fix `CopilotModel.IsCallbacksEnabled()` false-claim breaking all ChatModel activity events (context-window bar stuck at 0 tokens)

Target repo: `github.com/webcenter-fr/eino-ext`, package
`components/model/copilot` (currently pinned in `rancher-doc-chat-api-k8s` at
`v0.0.0-20260803143630-d1579a077466`, file `copilot.go:194`).

This is a different/deeper bug than the one already tracked in
`.kilo/plans/1783679308471-ui-context-cost-not-displayed.md` (that plan
assumed `step.ended` fires but with zero tokens; this plan shows
`step.ended` — and every other ChatModel-scoped activity event — never
fires at all for Copilot models, root-caused in `eino-ext`, not
`rancher-doc-chat-api-k8s`).

## Symptom (as seen in the consumer app)

In `rancher-doc-chat-api-k8s`'s chat UI, the context-window usage bar at the
top of the chat panel only ever shows the max/limit token count (e.g.
"128K"); the "used" count never appears and the bar never fills, for every
run, on every model, every time — not intermittent.

## Root cause (confirmed empirically, not just by code reading)

`copilot.go:194`:

```go
func (m *CopilotModel) IsCallbacksEnabled() bool { return true }
```

This implements `eino`'s `components.Checker` interface, which is eino's way
for a component to say *"I already emit my own `callbacks.Handler`
invocations internally — don't wrap me in another instrumentation layer."*
`components.IsCallbacksEnabled` (`cloudwego/eino@v0.9.12`
`components/interface.go:55`) trusts this at face value:

```go
func IsCallbacksEnabled(i any) bool {
    if checker, ok := i.(Checker); ok {
        return checker.IsCallbacksEnabled()
    }
    return false
}
```

`compose`'s node-building path (`toChatModelNode` →
`toComponentNode` → `parseExecutorInfoFromComponent`,
`compose/component_to_graph_node.go:93` /
`compose/graph_node.go:151`) uses this to decide whether the graph itself
needs to wrap the model's `Generate`/`Stream` with callback injection:

```go
meta := parseExecutorInfoFromComponent(componentType, node) // isComponentCallbackEnabled = IsCallbacksEnabled(node)
run := runnableLambda(invoke, stream, collect, transform, !meta.isComponentCallbackEnabled)
```

`runnableLambda` → `newRunnablePacker` (`compose/runnable.go:340-365`) only
wraps `invoke`/`stream` with `invokeWithCallbacks`/`streamWithCallbacks` —
the functions that actually call `callbacks.OnStart` /
`OnEndWithStreamOutput` / etc. with `RunInfo{Component:
components.ComponentOfChatModel, ...}` — **when `enableCallback` (i.e.
`!isComponentCallbackEnabled`) is true**. Since `CopilotModel` claims
`IsCallbacksEnabled() == true`, `enableCallback` is `false`, so eino's graph
layer deliberately does **not** instrument the model — it trusts
`CopilotModel` to do it itself.

**`CopilotModel` never does.** Confirmed by an exhaustive grep across the
entire package:

```bash
$ grep -rln "callbacks\." components/model/copilot/*.go
# (no output — zero matches, in copilot.go, copilot_chat.go,
#  copilot_stream.go, copilot_responses.go, token.go, models.go, check.go)
```

There is no embedding of an already-instrumented model either (`CopilotModel`
is a plain struct with its own `Generate`/`Stream`/etc., not a wrapper around
`cloudwego/eino-ext/components/model/openai`). So **no layer — neither eino's
graph wrapper nor the component itself — ever calls a `callbacks.Handler`
for a Copilot model invocation.** Every `ComponentOfChatModel`-scoped
activity event is silently dropped: `step.started`, `text.started`,
`text.delta`, `text.ended`, `reasoning.*`, and — the one the consumer app's
UI depends on — `step.ended` (which is what carries `Tokens`/`Cost`/
`Estimated`).

This is **not** a "gateway doesn't report usage" problem (that's what
`Handler.WithTokenCounter`'s chars/4 fallback in `handler.go` already covers,
and is a different, already-fixed issue). The gateway usage is fine — the
callback that would read it never runs at all.

## Empirical reproduction (live, against the real Copilot gateway)

Ran a standalone harness (not part of either repo, ad hoc, deleted after use)
that:

1. Built a real `chatmodel.New(ctx, &chatmodel.Config{Provider:
   CopilotProvider, BaseURL: "https://api.business.githubcopilot.com", Model:
   "claude-sonnet-5", APIKey: <real token>})`.
2. Registered `activity.NewHandlerWithConfig(bus,
   activity.WithTokenCounter(counter.DefaultTokenCounter))` and a second,
   plain debug `callbacks.Handler` (prints `RunInfo` on every timing) via
   `callbacks.AppendGlobalHandlers(...)` — mirroring exactly how
   `rancher-doc-chat-api-k8s`'s `internal/server/server.go` wires the global
   handler.
3. Wrapped the model in a single-node `compose.NewChain[[]*schema.Message,
   *schema.Message]().AppendChatModel(cm)`, compiled it, and called
   `r.Stream(ctx, msgs)` with a trivial prompt ("Reply with exactly one
   word: hi").

Result:

```
DEBUG OnStartWithStreamInput &{  Chain}
DEBUG OnEndWithStreamOutput &{  Chain}
chunk 1: role=assistant content="h" meta=<nil>
chunk 2: role=assistant content="i" meta=<nil>
chunk 3: role=assistant content="" meta=&{FinishReason: Usage:0x2a8aa50ea540 LogProbs:<nil>}
chunk 4: role=assistant content="" meta=&{FinishReason:stop Usage:<nil> LogProbs:<nil>}
recv ended, err= EOF n= 4
```

- The call succeeded end-to-end: real streamed content, and — notably —
  `chunk 3`'s `ResponseMeta.Usage` is a **non-nil** pointer, i.e. the real
  Copilot gateway **does** report usage for this token/model on
  `/chat/completions` streaming (ruling out "Cause B: gateway omits usage"
  from the earlier plan, at least for this business-plan token/model combo).
- Only **two** callback invocations fired for the whole run, both tagged
  `RunInfo{Component: "Chain"}` — the outer `compose.Chain`'s own generic
  wrapper (unrelated to component-specific instrumentation; `activity.Handler`
  ignores it because it only switches on `ComponentOfChatModel`/
  `ComponentOfTool`). **Zero** `RunInfo{Component: "ChatModel"}` events ever
  fired — not `step.started`, not `text.delta`, not `step.ended`. This is the
  direct, reproducible confirmation of the bug above: eino trusted
  `CopilotModel.IsCallbacksEnabled()==true` and skipped instrumenting the
  model; the model itself never instruments.

## Why this was hard to notice in the consumer app

`rancher-doc-chat-api-k8s`'s supervisor is designed so the final answer is
delivered as the argument stream of a terminal **tool** call
(`attempt_completion`), not as raw chat-model text — so `ComponentOfTool`
events (which are unaffected by this bug; tools are a separate code path)
still populate the chat UI and most of the activity timeline (tool calls,
`agent.switched`, etc.), giving the impression the activity stream works.
Only the ChatModel-specific signals — token/cost accounting, raw
text/reasoning deltas, and the context-window progress bar that depends on
`step.ended.tokens.input` — are silently empty for every run, on every
model, since the app's `llm.provider` is `github-copilot`.

It also explains why `rancher-doc-chat-api-k8s`'s existing
`callbacksAwareModel` wrapper (`internal/server/agent/chat.go:205-247`) does
not help: that wrapper was built to solve the *opposite* problem — some
provider (Ollama or `cloudwego/eino-ext`'s native `openai` component) that
**does** self-instrument, causing double-emitted events when eino's own
graph wrapper ALSO instrumented on top. The fix there is to forward
`components.IsCallbacksEnabled(cm)` so eino trusts the model's own claim.
For Copilot, that stored/forwarded claim (`true`) is simply wrong, so the
consumer-side wrapper faithfully preserves the eino-ext bug instead of
working around it.

## Fix

### 1. `copilot.go` — make `IsCallbacksEnabled` reflect reality

```go
// IsCallbacksEnabled implements components.Checker. CopilotModel does not
// self-instrument eino callbacks (Generate/Stream call the Copilot HTTP API
// directly with no callbacks.OnStart/OnEnd/OnEndWithStreamOutput calls), so
// this must be false: it tells eino's compose/adk graph layer to wrap this
// model with its own callback injection instead of trusting a
// self-instrumentation that never happens. Returning true here (the
// previous, incorrect behavior) silently drops every ComponentOfChatModel
// activity/callback event for every Copilot call — see
// rancher-doc-chat-api-k8s's context-window usage bar bug for the consumer
// impact.
func (m *CopilotModel) IsCallbacksEnabled() bool { return false }
```

Grep confirms no other code in this package (or its tests) depends on the
`true` value's semantics beyond the one test below, so this is a pure,
self-contained fix.

### 2. `copilot_test.go` — fix the now-inverted assertion

```go
func TestCopilotModelIsCallbacksEnabled(t *testing.T) {
    ctx := context.Background()
    cfg := &Config{
        CopilotToken: "token",
        BaseURL:      "http://localhost:0",
        Timeout:      10 * time.Second,
    }
    m, err := NewCopilotChatModel(ctx, cfg)
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    // CopilotModel does not self-instrument eino callbacks (Generate/Stream
    // hit the HTTP API directly), so it must report false here — true would
    // make eino's compose/adk layer skip its own instrumentation, silently
    // dropping every ChatModel-scoped callback/activity event (see
    // TestCopilotModelCallbacksFireThroughComposeChain below for the
    // end-to-end regression this previously broke).
    if m.IsCallbacksEnabled() {
        t.Error("expected IsCallbacksEnabled to return false")
    }
}
```

### 3. New regression test — assert the actual eino callback contract, not just the flag

The bug wasn't really "the flag has the wrong literal value" — it's "eino
never calls back into any handler for a Copilot chat model". Add a test that
exercises this through the real `compose` graph path (the same path
`adk.NewChatModelAgent` uses), so a future regression (e.g. someone flips the
flag back to `true` "to be safe", or eino changes its trust contract) is
caught even if `TestCopilotModelIsCallbacksEnabled` is missed:

```go
// copilot_callbacks_integration_test.go (or appended to copilot_stream_test.go)

package copilot

import (
    "context"
    "fmt"
    "net/http"
    "net/http/httptest"
    "sync/atomic"
    "testing"
    "time"

    "github.com/cloudwego/eino/callbacks"
    "github.com/cloudwego/eino/components"
    "github.com/cloudwego/eino/compose"
    "github.com/cloudwego/eino/schema"
)

// spyHandler records every RunInfo.Component seen across all timings.
type spyHandler struct {
    chatModelSeen atomic.Bool
}

func (s *spyHandler) OnStart(ctx context.Context, info *callbacks.RunInfo, _ callbacks.CallbackInput) context.Context {
    if info != nil && info.Component == components.ComponentOfChatModel {
        s.chatModelSeen.Store(true)
    }
    return ctx
}
func (s *spyHandler) OnEnd(ctx context.Context, info *callbacks.RunInfo, _ callbacks.CallbackOutput) context.Context {
    if info != nil && info.Component == components.ComponentOfChatModel {
        s.chatModelSeen.Store(true)
    }
    return ctx
}
func (s *spyHandler) OnError(ctx context.Context, _ *callbacks.RunInfo, _ error) context.Context { return ctx }
func (s *spyHandler) OnStartWithStreamInput(ctx context.Context, info *callbacks.RunInfo, in *schema.StreamReader[callbacks.CallbackInput]) context.Context {
    in.Close()
    return ctx
}
func (s *spyHandler) OnEndWithStreamOutput(ctx context.Context, info *callbacks.RunInfo, out *schema.StreamReader[callbacks.CallbackOutput]) context.Context {
    if info != nil && info.Component == components.ComponentOfChatModel {
        s.chatModelSeen.Store(true)
    }
    out.Close()
    return ctx
}

// TestCopilotModelCallbacksFireThroughComposeChain is the end-to-end
// regression for the IsCallbacksEnabled bug: it drives a CopilotModel
// through a real compose.Chain (the same wrapping adk.NewChatModelAgent
// uses) against a mock streaming /chat/completions endpoint, and asserts
// that a globally-registered callbacks.Handler actually observes a
// ComponentOfChatModel-scoped OnEndWithStreamOutput. Before the fix, this
// callback never fired (IsCallbacksEnabled()==true made eino trust
// CopilotModel to self-instrument, which it never does), so
// spy.chatModelSeen stayed false forever.
func TestCopilotModelCallbacksFireThroughComposeChain(t *testing.T) {
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "text/event-stream")
        for _, line := range []string{
            `data: {"choices":[{"delta":{"content":"hi"}}]}`,
            `data: {"choices":[{"delta":{}}],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}`,
            `data: [DONE]`,
        } {
            fmt.Fprintf(w, "%s\n\n", line)
        }
    }))
    defer srv.Close()

    ctx := context.Background()
    cm, err := NewCopilotChatModel(ctx, &Config{
        CopilotToken: "test-token",
        BaseURL:      srv.URL,
        Timeout:      10 * time.Second,
    })
    if err != nil {
        t.Fatalf("NewCopilotChatModel: %v", err)
    }

    spy := &spyHandler{}
    callbacks.AppendGlobalHandlers(spy) // mirrors server.go's callbacks.AppendGlobalHandlers wiring

    chain := compose.NewChain[[]*schema.Message, *schema.Message]()
    chain.AppendChatModel(cm)
    r, err := chain.Compile(ctx)
    if err != nil {
        t.Fatalf("Compile: %v", err)
    }

    sr, err := r.Stream(ctx, []*schema.Message{{Role: schema.User, Content: "hi"}})
    if err != nil {
        t.Fatalf("Stream: %v", err)
    }
    defer sr.Close()
    for {
        if _, err := sr.Recv(); err != nil {
            break
        }
    }

    if !spy.chatModelSeen.Load() {
        t.Fatal("expected a ComponentOfChatModel callback to fire through the compose chain; " +
            "got none — IsCallbacksEnabled() is likely (re)claiming self-instrumentation it doesn't do")
    }
}
```

Note: `callbacks.AppendGlobalHandlers` is process-global and cumulative in
eino (no unregister API), so if other tests in the same package/binary also
call it, run this test in its own file/package-level `TestMain` isolation
if `go test ./...` shows cross-test interference; a lighter alternative is
`compose.WithCallbacks(spy)` passed directly to `r.Stream(ctx, msgs,
compose.WithCallbacks(spy))` instead of the global registration — prefer
that if the package's existing tests already rely on a clean global handler
list.

### 4. Validation checklist (in the eino-ext repo)

```bash
go build ./...
go vet ./...
go test ./components/model/copilot/...
```

Confirm:
- `TestCopilotModelIsCallbacksEnabled` passes with the flipped assertion.
- The new `TestCopilotModelCallbacksFireThroughComposeChain` passes (fails
  before the fix, on the pre-fix `IsCallbacksEnabled()==true` code — verify
  this by temporarily reverting the fix and confirming the new test goes
  red, to prove it actually catches the regression).
- No other test in the package regresses (in particular
  `copilot_stream_test.go`, `copilot_responses_test.go`,
  `copilot_chat.go`'s own tests, which construct/call `CopilotModel`
  directly without going through `compose` — those are unaffected by this
  change since they never relied on `IsCallbacksEnabled`'s value).

## Downstream (in `rancher-doc-chat-api-k8s`, after the eino-ext fix lands and is released/tagged)

1. Bump the pin:
   ```bash
   go get github.com/webcenter-fr/eino-ext@<new-commit-or-tag>
   go mod tidy
   go build ./... && go vet ./... && go test ./internal/server/...
   ```
2. No source change needed in `rancher-doc-chat-api-k8s` itself:
   `internal/server/agent/chat.go`'s existing `callbacksAwareModel` wrapper
   already forwards `components.IsCallbacksEnabled(cm)` faithfully
   (`chat.go:319`); once the underlying `CopilotModel` correctly reports
   `false`, eino's own graph-level instrumentation takes over automatically
   for every supervisor/sub-agent model, with no double-emission (single
   layer either way — eino's, since Copilot never had its own).
3. Manual validation: start a real chat turn and confirm, in this order:
   - The context-window progress bar's "used" count now appears and updates
     (no longer stuck showing only the max).
   - The bar's used segment grows across turns within a session and turns
     red past the configured `summarization.thresholdPercent` threshold.
   - The Activity timeline's per-step token counts (`↑X ↓Y`, the `act-tokens`
     span in `activity.js`) show non-zero, non-"~" (non-estimated) values —
     i.e. real gateway usage, not the chars/4 fallback — confirming this was
     purely a "callback never fires" bug, not a "gateway omits usage" one.
   - No tool-call or agent-switch event in the timeline is duplicated (the
     regression `callbacksAwareModel` was originally built to prevent for
     other providers) — a quick manual run comparing event counts before/
     after is enough; there is no automated test for this in
     `rancher-doc-chat-api-k8s` today.
   - `curl -s localhost:<port>/metrics | grep llm_tokens_total` (internal
     Prometheus endpoint) shows a non-zero counter after one real run.
