# Plan A — eino-ext: agent attribution for the activity stream

**Repository:** `github.com/webcenter-fr/eino-ext` (external; implement in its own IDE/checkout)
**Package:** `callbacks/activity` (+ `callbacks/activity/sse`)
**Depends on / consumed by:** Plan B (`rancher-doc-chat-api-k8s`). Publish a new tagged/pseudo-version after these changes so Plan B can bump `go.mod`.

## Goal

The activity `Handler` currently emits `text.*`, `tool.*`, `reasoning.*`, `step.*`
events that carry only the **model** name (`RunInfo.Type/Name`). In a multi-agent
(supervisor + sub-agents) run there is no way for a UI to tell which `text.*` is
the supervisor's final answer versus a sub-agent's intermediate chatter, because
there is no adk bridge (`agent.switched` is never produced).

Add lightweight **agent attribution** so every event can be routed by agent name,
without introducing an adk bridge. Keep the package transport-agnostic.

## Context (current state, verified)

- `event.go`: `Event` envelope has `ID, SessionID, Type, Timestamp, Data`.
  `StepStarted` already has an unused `Agent` field; `AgentSwitched` type exists
  but is never emitted. `MarshalSSEData` marshals **only** `e.Data`.
- `context.go`: `WithSession`/`SessionFromContext` use an unexported context key.
- `handler.go`: `publish(ctx, t, data)` reads `SessionFromContext` and calls
  `bus.Publish`. `OnStart/OnEnd/OnError` and the two streaming timings handle
  ChatModel and Tool components.
- `sse/sse.go`: writes one frame per event (`id`, `event: <Type>`,
  `data: MarshalSSEData(e)`).

## Decisions

- Attribution is carried on the **Event envelope** (`Agent string`), populated
  from context — not duplicated onto every payload struct.
- The agent name is injected via an `adk.ChatModelAgentMiddleware` helper that
  wraps the agent's model/tool execution context. This keeps the single
  `callbacks.Handler`; no adk event bridge.
- The SSE `data` JSON must expose the agent so the browser can route without
  reading a non-standard SSE field. `MarshalSSEData` will merge an `agent` key
  into the emitted payload object (preserving existing payload fields like
  `delta`, `tool`, `content`).

## Tasks

1. **`context.go` — add agent context helpers.**
   - Add `agentKeyType struct{}` + `var agentKey`.
   - `WithAgent(ctx context.Context, name string) context.Context`.
   - `AgentFromContext(ctx context.Context) (string, bool)`.

2. **`event.go` — envelope + serialization.**
   - Add `Agent string \`json:"agent,omitempty"\`` to `Event`.
   - Change `MarshalSSEData(e Event)` so the JSON body includes the agent:
     marshal `e.Data` to a generic object (`map[string]any` via
     `json.Marshal` → `json.Unmarshal`) and, when `e.Agent != ""`, set
     `obj["agent"] = e.Agent`, then marshal `obj`. If `e.Data` is nil, emit
     `{"agent": "..."}` (or `{}`/`null` when agent empty, preserving current
     behavior). Keep the function tolerant of non-object payloads (fall back to
     marshalling `e.Data` unchanged when it is not a JSON object).
   - Document the wire shape change in the `event.go` and `sse/README.md` docs
     (UI now reads `JSON.parse(e.data).agent`).

3. **`handler.go` — populate agent + emit `agent.switched`.**
   - In `publish`, read `AgentFromContext(ctx)` and set `Event.Agent`.
   - Populate `StepStarted.Agent` from context in `OnStart`
     (`ComponentOfChatModel`).
   - Emit `TypeAgentSwitched{Agent: name}` the first time a given session sees a
     new agent name. Track last-seen agent **per session** in a small
     concurrency-safe map on `Handler` (e.g. `sync.Map` keyed by sessionID),
     pruned is optional (bounded by Bus session cap). Emit before the first
     `step.started` of that agent.
   - Do **not** mutate callback Input/Output; only read context. Preserve the
     existing stream-reader `Close()` and timing-`Needed` invariants.

4. **adk attribution middleware (new file, e.g. `adk.go` or `middleware.go`).**
   - Provide `func AgentMiddleware(name string) adk.ChatModelAgentMiddleware`
     that returns a middleware wrapping the agent run so that
     `WithAgent(ctx, name)` is set on the context passed down to the model and
     tool callbacks for that agent.
   - Verify against the installed `github.com/cloudwego/eino/adk`
     `ChatModelAgentMiddleware` signature how to thread context to the inner
     model/tool calls (the middleware must set the value on the ctx that reaches
     `callbacks`). If the adk middleware cannot influence the callback context
     directly, fall back to exposing only `WithAgent` and document that callers
     set it on the run context per sub-agent (Plan B will wire whichever
     mechanism is supported).
   - Keep this in the core package only if it does not add a web dependency; adk
     is acceptable (already an eino dependency).

5. **Tests.**
   - `handler_test.go`: assert `Event.Agent` is set from `WithAgent`, and that a
     single `agent.switched` is emitted per session per new agent.
   - `event` test (or extend existing): assert `MarshalSSEData` includes
     `"agent"` and preserves payload fields (e.g. `text.delta` keeps `delta`),
     and that nil/empty-agent cases keep current output.
   - `sse_test.go`: assert the streamed `data:` frame contains `agent`.

6. **Docs.**
   - Update `callbacks/activity/README.md` event-catalog note and
     `callbacks/activity/sse/README.md` UI-consumption snippet to show
     `const { agent } = JSON.parse(e.data)` routing.

7. **Release.**
   - Commit, push, and produce a new module version (tag or pseudo-version) so
     Plan B can pin it in `go.mod`.

## Validation

- `go build ./...` and `go test ./callbacks/activity/...` in the eino-ext repo.
- Manual/unit check: a run with two `WithAgent` scopes produces events whose
  `data` JSON carries the correct `agent`, with `agent.switched` on transitions.

## Risks / notes

- **adk middleware context threading** is the main unknown: confirm the
  `ChatModelAgentMiddleware` contract actually lets the injected ctx reach the
  component callbacks. If not, ship `WithAgent` + document run-scoped usage and
  let Plan B attach per-agent handlers/context.
- Keep `MarshalSSEData` backward-compatible for non-object/nil payloads to avoid
  breaking existing consumers.
- Per-session last-agent tracking must be bounded; rely on the Bus session cap
  semantics and avoid unbounded growth.

## Out of scope

- A full adk `AgentEvent` bridge (richer lifecycle). Can be added later behind
  the same `Agent` envelope field.
