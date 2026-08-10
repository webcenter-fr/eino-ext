# Plan: Enrich `agent.switched` events with model + description

## Goal

The UI timeline renders `agent.switched` banners as `Agent: <internal_name>` — no
description of what the agent does, nor which model powers it. This plan enriches
the `agent.switched` event payload with `model` and `description` metadata so the
frontend can render a richer banner, while keeping every existing name-only
caller working unchanged.

Two repos are touched:
- `/projects/eino-ext` — the library (context helpers, event struct, handler,
  agentattr middleware + tests).
- `/projects/rancher-doc-chat-api-k8s` — the application (agent metadata table,
  `NewChatParams` wiring, server.go caller, frontend banner).

## Design decisions

1. **Backward compatibility is load-bearing.** The existing `WithAgent` /
   `AgentFromContext` helpers and the `agentKey` context value stay exactly as
   they are. `WithAgentMeta` sets a *new* context key carrying the full
   `AgentMeta` struct **and** also sets the legacy `agentKey` (so `AgentFromContext`
   and the Handler's `publish` path keep working without change). Existing
   callers that only call `WithAgent` continue to work; `AgentMetaFromContext`
   returns `ok=false` for them and the handler leaves `Model`/`Description` empty
   (omitempty → no wire change).

2. **`AgentMeta` is a struct, not three loose keys.** One context value carries
   `{Name, Model, Description}` atomically — no risk of reading model from one
   agent and description from another during a context swap.

3. **`AgentSwitched.Model`/`Description` are `omitempty`.** Old SSE replay logs
   (stored before this change) and events from name-only callers serialize
   exactly as before; the frontend treats missing fields as "not available".

4. **`ResolvedSupervisorModel` / `ResolvedAgentModel` in `NewChatParams`.** The
   raw `SupervisorModel`/`AgentModel` fields may be `"auto"` (copilot), which is
   a poor banner label. The new `Resolved*` fields carry the concrete,
   display-ready model name. `NewChat` falls back to the raw field when the
   resolved one is empty, so existing callers that don't set it are unaffected.

5. **Agent descriptions live in a single table in `chat.go`.** A
   `map[string]agentMeta` keyed by the canonical `AgentName*` constants keeps
   every description next to the names already declared there. The table is the
   single source of truth; `withAgentAttr` looks it up.

6. **`withAgentAttr` signature grows to `(base, name, model, description)`.**
   Every existing call site passes the new two args. The model is
   `ResolvedAgentModel` for sub-agents and `ResolvedSupervisorModel` for the
   supervisor.

## Affected files

### eino-ext

- `callbacks/activity/context.go` — add `AgentMeta`, `WithAgentMeta`,
  `AgentMetaFromContext`.
- `callbacks/activity/event.go` — extend `AgentSwitched` with `Model`,
  `Description`.
- `callbacks/activity/handler.go` — `maybeEmitAgentSwitched` reads
  `AgentMetaFromContext` and populates the payload.
- `callbacks/activity/context_test.go` — NEW, unit tests for the new helpers.
- `components/middleware/agentattr/agentattr.go` — extend `Config` + `Middleware`,
  swap `WithAgent` → `WithAgentMeta`.
- `components/middleware/agentattr/agentattr_test.go` — assert model+description
  thread through context.

### rancher-doc-chat-api-k8s

- `internal/server/agent/chat.go` — agent metadata table, extend
  `NewChatParams`, extend `withAgentAttr`, pass model+description at every
  `withAgentAttr` call.
- `internal/server/server.go` — populate `ResolvedSupervisorModel` and
  `ResolvedAgentModel` in the `agent.NewChatParams` literal.
- `internal/server/web/activity.js` — render model + description in the
  `agent.switched` banner.

## Task 1 — `callbacks/activity/context.go`

Add the following after the existing `AgentFromContext` (keep `WithAgent` /
`AgentFromContext` / `agentKey` untouched):

```go
// AgentMeta carries the display metadata for the active agent: its canonical
// name, the model that powers it, and a short human-readable description of
// what the agent does. It is set on the run context by WithAgentMeta so the
// Handler can enrich agent.switched events (and any other payload that wants
// the metadata) without callers having to thread it through each event.
type AgentMeta struct {
    Name        string
    Model       string
    Description string
}

// agentMetaKeyType is an unexported context key type to avoid collisions.
type agentMetaKeyType struct{}

var agentMetaKey = agentMetaKeyType{}

// WithAgentMeta returns a copy of ctx carrying the active agent's full
// metadata (name, model, description). It ALSO sets the legacy agentKey to
// meta.Name so existing AgentFromContext callers and the Handler's publish
// path keep working unchanged. meta.Name must be non-empty; if it is empty the
// function is a no-op (returns ctx unchanged) to preserve the "empty agent =
// unattributed" invariant the Handler relies on.
func WithAgentMeta(ctx context.Context, meta AgentMeta) context.Context {
    if meta.Name == "" {
        return ctx
    }
    ctx = context.WithValue(ctx, agentMetaKey, meta)
    return context.WithValue(ctx, agentKey, meta.Name)
}

// AgentMetaFromContext reads the agent metadata set by WithAgentMeta. The
// boolean is false when no metadata was set (name-only WithAgent callers, or
// single-agent runs); in that case the returned AgentMeta is the zero value.
// Callers that only need the name should prefer the cheaper AgentFromContext.
func AgentMetaFromContext(ctx context.Context) (AgentMeta, bool) {
    meta, ok := ctx.Value(agentMetaKey).(AgentMeta)
    return meta, ok
}
```

**Edge cases:**
- `meta.Name == ""` → no-op (preserves the empty-agent invariant; avoids
  setting `agentKey` to "" which `AgentFromContext` would misread as "not set"
  vs "set to empty").
- `WithAgent` called *after* `WithAgentMeta` on the same ctx → `agentKey` is
  overwritten with the new name but `agentMetaKey` still holds the old meta.
  This is fine: agentattr always calls `WithAgentMeta` (never `WithAgent`) and
  does so once per method, so the meta and name stay consistent in practice.
  Document this ordering expectation in the `WithAgentMeta` doc comment is NOT
  required — the agentattr middleware is the only caller.

## Task 2 — `callbacks/activity/event.go`

Replace the `AgentSwitched` struct (lines 157–160):

```go
// AgentSwitched carries the newly active agent's name plus optional display
// metadata: the model that powers it and a short description of its role.
// Model and Description are omitted from the wire when empty (name-only
// callers, or old replay logs) so existing consumers keep working.
type AgentSwitched struct {
    Agent       string `json:"agent"`
    Model       string `json:"model,omitempty"`
    Description string `json:"description,omitempty"`
}
```

No other event types change.

## Task 3 — `callbacks/activity/handler.go`

Modify `maybeEmitAgentSwitched` (lines 139–155) to read the meta from context
and pass it into the `AgentSwitched` payload. The function signature stays the
same (it already receives `ctx`):

```go
func (h *Handler) maybeEmitAgentSwitched(ctx context.Context, sessionID, agent string) {
    if agent == "" {
        return
    }
    prev, _ := h.lastAgent.Load(sessionID)
    if p, ok := prev.(string); ok && p == agent {
        return
    }
    if actual, loaded := h.lastAgent.Swap(sessionID, agent); loaded {
        if p, ok := actual.(string); ok && p == agent {
            return
        }
    }
    payload := AgentSwitched{Agent: agent}
    if meta, ok := AgentMetaFromContext(ctx); ok {
        payload.Model = meta.Model
        payload.Description = meta.Description
    }
    h.bus.Publish(ctx, Event{SessionID: sessionID, Agent: agent, Type: TypeAgentSwitched, Data: payload})
}
```

**Why read from ctx and not from a new parameter:** `maybeEmitAgentSwitched` is
called from `publish`, which already has `ctx`. Threading meta as a parameter
would require changing `publish` and every `publish` call site; reading from
ctx is the same pattern the handler already uses for the agent name and keeps
the diff minimal.

**Edge case — name-only caller:** when `WithAgent` (not `WithAgentMeta`) was
used, `AgentMetaFromContext` returns `ok=false`, so `Model`/`Description` stay
empty → wire output unchanged → existing `TestHandlerAgentAttribution` and
`TestHandlerAgentSwitchedOncePerAgent` still pass.

## Task 4 — `components/middleware/agentattr/agentattr.go`

### 4a. Extend `Config`

```go
type Config struct {
    AgentName   string `validate:"required" jsonschema:"description=Name tagged onto activity events produced during this agent's run"`
    // Model is the display name of the model powering this agent, surfaced
    // on the agent.switched banner. Optional: empty leaves the banner's
    // model field blank (backward compatible).
    Model string `jsonschema:"description=Display name of the model powering this agent"`
    // Description is a short, human-readable summary of what this agent does,
    // surfaced on the agent.switched banner. Optional for the same reason.
    Description string `jsonschema:"description=Short human-readable description of this agent's role"`
}
```

`validate:"required"` stays only on `AgentName`. `Model`/`Description` are
optional; `validate.Struct` in `New` already runs over the whole struct and
will pass when they are empty.

### 4b. Extend `Middleware` and `New`

```go
type Middleware struct {
    *adk.BaseChatModelAgentMiddleware
    name        string
    model       string
    description string
}

func New(cfg *Config) (*Middleware, error) {
    if cfg == nil {
        cfg = &Config{}
    }
    c := *cfg
    if err := validate.Struct(&c); err != nil {
        return nil, errors.Wrap(err, "invalid agentattr.Config")
    }
    return &Middleware{
        BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{},
        name:                         c.AgentName,
        model:                        c.Model,
        description:                  c.Description,
    }, nil
}
```

### 4c. Replace every `activity.WithAgent(ctx, m.name)` with `activity.WithAgentMeta`

There are 5 call sites: `BeforeAgent`, `BeforeModelRewriteState`,
`WrapInvokableToolCall`, `WrapStreamableToolCall`,
`WrapEnhancedInvokableToolCall`, `WrapEnhancedStreamableToolCall` (6 total —
count them in the current file). Each becomes:

```go
return activity.WithAgentMeta(ctx, activity.AgentMeta{
    Name:        m.name,
    Model:       m.model,
    Description: m.description,
}), ...
```

For the two `Wrap*ToolCall` methods the call is inside the returned closure:
`activity.WithAgentMeta(ctx, activity.AgentMeta{Name: m.name, Model: m.model, Description: m.description})`.

**Backward compat note:** `WithAgentMeta` sets `agentKey` too, so
`AgentFromContext` (used by the handler's `publish`) still returns `m.name`.
No handler change needed for the envelope `Agent` field.

## Task 5 — `callbacks/activity/context_test.go` (NEW)

New file, `package activity`. Cover:

1. `TestWithAgentMetaSetsBothKeys` — `WithAgentMeta(ctx, AgentMeta{Name:"x", Model:"m", Description:"d"})` →
   `AgentFromContext` returns `("x", true)` AND `AgentMetaFromContext` returns
   the full struct.
2. `TestWithAgentMetaEmptyNameIsNoOp` — `WithAgentMeta(ctx, AgentMeta{})` →
   both `AgentFromContext` and `AgentMetaFromContext` return false (ctx
   unchanged).
3. `TestAgentMetaFromContextAbsent` — plain `context.Background()` →
   `AgentMetaFromContext` returns `(AgentMeta{}, false)`.
4. `TestWithAgentStillWorks` — `WithAgent(ctx, "x")` → `AgentFromContext`
   returns `("x", true)`, `AgentMetaFromContext` returns `false` (legacy
   callers don't accidentally produce a zero-value meta).
5. `TestWithAgentMetaThenWithAgentOverridesName` — `WithAgentMeta` then
   `WithAgent(ctx, "y")` → `AgentFromContext` returns `"y"`, `AgentMetaFromContext`
   still returns the original meta (documents the ordering behavior).

Use plain `if` checks + `t.Fatalf` matching the existing test style in
`handler_test.go` (no testify).

## Task 6 — `components/middleware/agentattr/agentattr_test.go`

Extend the existing tests:

1. In `TestNewRequiresAgentName`, add a case: `New(&Config{AgentName:"x", Model:"m", Description:"d"})`
   succeeds and the returned middleware has `m.model=="m"`, `m.description=="d"`
   (access via a small helper or by adding a read accessor — see note below).
   Since `model`/`description` are unexported, assert via behavior: call
   `BeforeAgent` and read the meta back with `activity.AgentMetaFromContext`.

2. Add `TestMiddlewareInjectsAgentMetaOnContext` mirroring
   `TestMiddlewareInjectsAgentOnContext`: build with
   `&Config{AgentName:"supervisor", Model:"claude-sonnet-5", Description:"orchestrates sub-agents"}`,
   then for `BeforeAgent` and `BeforeModelRewriteState` assert via
   `activity.AgentMetaFromContext(ctx)` that `Name`, `Model`, `Description`
   all match. Also assert `AgentFromContext` still returns the name (backward
   compat).

3. For `WrapInvokableToolCall`, assert inside the endpoint that
   `AgentMetaFromContext` returns the full meta (the existing test already
   asserts the name via `AgentFromContext`; add the meta assertion).

## Task 7 — `rancher-doc-chat-api-k8s/internal/server/agent/chat.go`

### 7a. Agent metadata table

Add near the `AgentName*` constants (after line 59):

```go
// agentMetaEntry is the display metadata for one agent, looked up by name in
// agentMetas and passed to withAgentAttr so the agent.switched banner can
// render a description alongside the name.
type agentMetaEntry struct {
    Model       string
    Description string
}

// agentMetas holds the human-readable description for every agent. The Model
// field is left empty here; withAgentAttr fills it from the resolved model
// name passed by the caller (sub-agents use ResolvedAgentModel, the
// supervisor uses ResolvedSupervisorModel) so a single table works for both
// trees and stays in sync with llm.agentModel/supervisorModel config.
var agentMetas = map[string]agentMetaEntry{
    AgentNameSupervisor:    {Description: "Orchestrates sub-agents and synthesises the final answer"},
    AgentNameWeb:           {Description: "Searches the web and fetches pages for up-to-date information"},
    AgentNameKubernetes:    {Description: "Inspects and manages Kubernetes clusters"},
    AgentNameArgocd:        {Description: "Inspects and manages Argo CD applications and resources"},
    AgentNameDocumentation: {Description: "Retrieves Rancher documentation and inventory from OpenSearch"},
    AgentNameLog:           {Description: "Queries OpenSearch log indices for troubleshooting"},
    AgentNamePrometheus:    {Description: "Queries Prometheus metrics for monitoring and diagnostics"},
    AgentNameGithub:        {Description: "Searches and reads GitHub repositories"},
    AgentNameShell:         {Description: "Runs commands in an ephemeral shell sandbox"},
    AgentNameS3:            {Description: "Lists and reads objects from S3 buckets"},
}
```

**Edge case — unknown agent name:** `withAgentAttr` (below) falls back to a
zero `agentMetaEntry` when the name is not in the map, so description is empty
and the banner degrades gracefully to name-only. This keeps the table robust to
future agents being added before their entry.

### 7b. Extend `withAgentAttr`

Change the signature and body (lines 222–230):

```go
// withAgentAttr returns a copy of base with an agentattr middleware appended
// that tags this agent's activity events with name, model and description.
// base is never mutated so the shared contextopt middleware slice can be
// reused across agents. model is the display model name (resolved, not
// "auto"); description is looked up from agentMetas when empty.
func withAgentAttr(base []adk.ChatModelAgentMiddleware, name, model, description string) ([]adk.ChatModelAgentMiddleware, error) {
    mw, err := agentattr.New(&agentattr.Config{
        AgentName:   name,
        Model:       model,
        Description: description,
    })
    if err != nil {
        return nil, errors.Wrapf(err, "failed to build agentattr middleware for %q", name)
    }
    out := make([]adk.ChatModelAgentMiddleware, 0, len(base)+1)
    out = append(out, base...)
    return append(out, mw), nil
}
```

### 7c. Extend `NewChatParams`

Add two fields to the `NewChatParams` struct (after `AgentModel` at line 155):

```go
    // ResolvedSupervisorModel is the concrete, display-ready supervisor model
    // name (e.g. "claude-sonnet-5"), used for the agent.switched banner. When
    // empty, NewChat falls back to SupervisorModel. Distinct from
    // SupervisorModel because the latter may be "auto" (copilot), which is a
    // poor banner label.
    ResolvedSupervisorModel string
    // ResolvedAgentModel is the concrete, display-ready sub-agent model name,
    // used for every sub-agent's agent.switched banner. Empty falls back to
    // AgentModel.
    ResolvedAgentModel string
```

### 7d. Resolve defaults inside `NewChat`

Near the top of `NewChat` (after the embedded-prompt sanity loop, before
`stabilize`), normalize the resolved names so the rest of the function can
use them unconditionally:

```go
supervisorDisplay := params.ResolvedSupervisorModel
if supervisorDisplay == "" {
    supervisorDisplay = params.SupervisorModel
}
agentDisplay := params.ResolvedAgentModel
if agentDisplay == "" {
    agentDisplay = params.AgentModel
}
```

### 7e. Update every `withAgentAttr` call site

There is one call per sub-agent + one for the supervisor. Each currently is:

```go
xxxHandlers, err := withAgentAttr(handlers, AgentNameXxx)
```

Replace with (sub-agents use `agentDisplay`; supervisor uses
`supervisorDisplay`):

```go
meta := agentMetas[AgentNameXxx]
xxxHandlers, err := withAgentAttr(handlers, AgentNameXxx, agentDisplay, meta.Description)
```

For the supervisor (line 796):

```go
meta := agentMetas[AgentNameSupervisor]
supervisorHandlers, err := withAgentAttr(handlers, AgentNameSupervisor, supervisorDisplay, meta.Description)
```

The full list of call sites to update (line numbers from the current file):
- Web: line 574
- Kubernetes: line 592
- Argocd: line 614
- Documentation: line 637
- Log: line 692
- Prometheus: line 715
- Github: line 733
- S3: line 756
- Shell: line 775
- Supervisor: line 796

**Note:** the `Model` passed to every sub-agent is the same `agentDisplay`
because all sub-agents share `params.AgentModel` (each gets its own model
*instance* via `newAgentModel()`, but the display name is identical). The
supervisor gets `supervisorDisplay`.

## Task 8 — `rancher-doc-chat-api-k8s/internal/server/server.go`

Populate the two new fields in the `agent.NewChatParams` literal (lines
510–539). The supervisor's resolved name is already computed as
`supervisorCatalogID` (line 155–158, auto-resolved at line 381–391). The agent
model has no equivalent catalog resolution today, so `ResolvedAgentModel`
falls back to the raw `agentModel` (which is fine unless it is "auto"; see
Edge cases).

```go
runners, sumModelBuilt, memAgents, err := agent.NewChat(context.Background(), agent.NewChatParams{
    // ... existing fields ...
    SupervisorModel:       supervisorModel,
    AgentModel:            llmCfg.GetString("agentModel"),
    ResolvedSupervisorModel: supervisorCatalogID,
    ResolvedAgentModel:     agentModel,
    // ... rest unchanged ...
})
```

**Edge case — `agentModel == "auto"`:** there is no agent-side auto-resolution
today (only `supervisorModelCatalogId` exists). If `agentModel` is `"auto"`,
the banner will show `"auto"`. This is a pre-existing display gap, not a
regression (the banner previously showed only the agent name and no model at
all). Resolving agent "auto" is explicitly out of scope for this change; note
it as a follow-up in the plan's Open questions.

## Task 9 — `rancher-doc-chat-api-k8s/internal/server/web/activity.js`

Update the `T.AgentSwitched` dispatch case (line 417). Currently:

```js
case T.AgentSwitched: return onBanner('agent', 'Agent: ' + (d.agent || agent || '?'));
```

Replace with a richer banner that renders name, model, and description. The
banner is a single text line today; keep it a single line but append the
metadata when present:

```js
case T.AgentSwitched: {
    const name = d.agent || agent || '?';
    const parts = ['Agent: ' + name];
    if (d.model) parts.push('· ' + d.model);
    if (d.description) parts.push('— ' + d.description);
    return onBanner('agent', parts.join(' '));
}
```

**Edge cases:**
- Old replay logs (stored before this change) have no `model`/`description`
  keys → `d.model`/`d.description` are `undefined` → banner renders exactly as
  before (`Agent: <name>`).
- Name-only backend callers (no `WithAgentMeta`) → same as old replay logs.
- The `replay()` path (line 475–481) dispatches through the same `dispatch`
  function, so replayed history renders the richer banner automatically when
  the stored event carries the new fields.

No CSS/template change needed: the banner already renders `item.text` as a
single line in the `act-banner` block (lines 568–570).

## Verification

Run from each repo root.

### eino-ext

```sh
go build ./callbacks/activity/... ./components/middleware/agentattr/...
go vet  ./callbacks/activity/... ./components/middleware/agentattr/...
go test ./callbacks/activity/... ./components/middleware/agentattr/...
golangci-lint run ./callbacks/activity/... ./components/middleware/agentattr/...
```

Expected:
- `TestHandlerAgentAttribution` and `TestHandlerAgentSwitchedOncePerAgent`
  still pass unchanged (name-only `WithAgent` path).
- New `context_test.go` tests pass.
- New agentattr meta test passes.

### rancher-doc-chat-api-k8s

```sh
go build ./internal/server/agent/... ./internal/server/...
go vet  ./internal/server/agent/... ./internal/server/...
go test ./internal/server/agent/... ./internal/server/...
golangci-lint run ./internal/server/agent/... ./internal/server/...
```

Manual / smoke check: start the server, open the chat UI, send a prompt that
delegates to a sub-agent, and confirm the `agent.switched` banner now reads
e.g. `Agent: documentation_agent · claude-sonnet-5 — Retrieves Rancher
documentation and inventory from OpenSearch`.

## Risks

1. **`WithAgent` after `WithAgentMeta` desyncs meta.Name from agentKey.** Only
   the agentattr middleware calls `WithAgentMeta`, and it never calls
   `WithAgent` afterward, so this cannot happen in practice. Documented in the
   `WithAgentMeta` comment.
2. **Stale replay logs.** Old stored activities lack `model`/`description`.
   `omitempty` + the frontend's `if (d.model)` guard make this a non-issue.
3. **Agent model "auto".** Banner shows "auto" for the model when
   `agentModel` is "auto" and no resolution is configured. Pre-existing gap,
   not a regression. See Open questions.
4. **Forgetting a `withAgentAttr` call site.** The plan lists all 10 call sites
   by line number; the implementer must update every one. A missed site
   compiles (Go rejects unused vars, but `withAgentAttr` with the old arity
   won't compile — so a missed site is a *build* error, not a silent bug).
   This is the desired failure mode.

## Open questions / out of scope

- **Agent "auto" model resolution.** Mirroring the supervisor's
  `supervisorModelCatalogId` + copilot auto-resolution for `agentModel` is
  out of scope. If desired, add an `agentModelCatalogId` config key and resolve
  it the same way `supervisorCatalogID` is resolved (server.go lines 155–158,
  381–391), then pass it as `ResolvedAgentModel`.
- **Per-sub-agent model override.** Today every sub-agent uses the same
  `params.AgentModel`. If a future change lets individual sub-agents use
  different models, `withAgentAttr`'s `model` argument is already per-call, so
  no further API change is needed — only the caller changes.
