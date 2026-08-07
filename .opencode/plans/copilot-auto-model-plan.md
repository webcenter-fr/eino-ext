# Copilot "Auto Model" Support — Implementation Plan

## Goal

Add support for an **"auto" model** value on the `copilot` provider
(`components/model/copilot`) so that callers — especially **GitHub Copilot free-tier**
users — can let the Copilot API pick the best available model for their account
tier instead of hard-coding a model ID.

The Copilot API already exposes an **auto-selection mechanism** via
`POST /models/session` with body `{"auto_mode":{"model_hints":[...]}}`. The
response contains `selected_model`, `session_token`, `expires_at`, and
`available_models`. Today this package only calls that endpoint for GPT-5+
models that require a session token (`needsSessionToken`), and it always sends
a single explicit `model_hints` entry.

This plan introduces a first-class `ModelAuto = "auto"` value: when the
resolved model is `"auto"` (case-insensitive), the provider calls
`/models/session` with **empty `model_hints`** so Copilot selects the model,
then uses the returned `selected_model` for the actual chat/responses request.
The returned `session_token` is reused for the call (and refreshed in the
background), exactly like the existing GPT-5 session flow.

### Assumptions about the Copilot "auto" model API

The Copilot API is undocumented for this exact case; the following are
**clearly-documented assumptions** based on the existing `session.go` behavior
and the `auto_mode` request shape:

1. Sending `{"auto_mode":{"model_hints":[]}}` (empty hints) asks the API to
   select a model automatically from the caller's available catalog. This is
   the natural reading of the existing `model_hints` field: a hint is
   optional, and omitting all hints defers the choice to the API.
2. The `selected_model` field of the response is a valid model ID for the
   caller's tier (free-tier → a free-tier-available model; paid → a premium
   model). The provider does **not** hard-code a fallback model — it trusts
   `selected_model`. If the session call fails, the error is surfaced (no
   silent fallback, which would be surprising and tier-dependent).
3. The `session_token` returned alongside `selected_model` is valid for the
   selected model (the existing GPT-5 flow already relies on this).
4. Free-tier accounts authenticate with a fine-grained PAT (`github_pat_...`)
   in direct-bearer mode (already implemented). Auto-model works the same
   way — no auth changes are needed.
5. `"auto"` is matched **case-insensitively and after trimming whitespace**,
   but the resolved `selected_model` is sent to the API verbatim (case
   preserved) because model IDs are case-sensitive on the API side.

If assumption (1) turns out to be wrong during integration testing (e.g. the
API requires at least one hint), the fallback is to send
`{"auto_mode":{"model_hints":["auto"]}}` instead. The plan calls this out as
a single-line toggle in `acquireAutoSession`.

## Affected files (absolute paths from repo root)

### Modify
- `/projects/eino-ext/components/model/copilot/models.go` — add `ModelAuto`
  constant and `IsAutoModel` helper.
- `/projects/eino-ext/components/model/copilot/session.go` — add
  `acquireAutoSession`, `autoModelResolution` struct, `ensureAutoModel`, and
  `startAutoModelRefresh`.
- `/projects/eino-ext/components/model/copilot/copilot.go` — add `autoModel`
  field to `CopilotModel`; initialize it in `NewCopilotChatModel`.
- `/projects/eino-ext/components/model/copilot/copilot_chat.go` — modify
  `Generate` to resolve the auto model before building the request.
- `/projects/eino-ext/components/model/copilot/copilot_stream.go` — modify
  `Stream` to resolve the auto model before building the request.
- `/projects/eino-ext/components/model/copilot/README.md` — document the
  auto model feature, free-tier usage, and the `ModelAuto` constant.

### Create
- `/projects/eino-ext/components/model/copilot/auto_model_test.go` — unit
  tests for `IsAutoModel`, `acquireAutoSession`, `ensureAutoModel`, and the
  `Generate`/`Stream` auto-resolution path (httptest-based).
- `/projects/eino-ext/components/model/copilot/auto_model_integration_test.go`
  — `//go:build integration` test proving auto-model works against the real
  free-tier API (`TestIntegration_FreeTier_AutoModel`).

### No changes required
- `go.mod` — no new dependencies; everything uses existing `net/http`,
  `emperror.dev/errors`, `sync`, and the in-package session machinery.
- `components/model/chatmodel/chatmodel.go` — `chatmodel.Config.Model` is
  `validate:"required"`, and `"auto"` is non-empty, so it already passes
  validation and is forwarded to `copilot.Config.Model` unchanged. The
  factory needs no code change; only a README note (optional, see below).
- `components/model/chatmodel/README.md` — optional one-line note that
  `Model: "auto"` is supported for the `github-copilot` provider.

## Data structures

### New constant and helper (`models.go`)

```go
// ModelAuto is a special Config.Model value that asks the Copilot API to
// select the model automatically via POST /models/session with empty
// model_hints. The selected model is returned in the session response's
// selected_model field and is used for the actual chat/responses request.
//
// This is the recommended value for free-tier accounts, where the available
// model catalog is smaller and may change over time.
const ModelAuto = "auto"

// IsAutoModel reports whether modelID is the auto-selection sentinel value
// ("auto", case-insensitive, surrounding whitespace trimmed). It returns
// false for the empty string.
func IsAutoModel(modelID string) bool {
    return strings.EqualFold(strings.TrimSpace(modelID), ModelAuto)
}
```

(`strings` is already imported in `models.go`.)

### New session-resolution types (`session.go`)

```go
// autoModelResolution holds the result of an auto-mode /models/session call:
// the API-selected model ID and the session token that authorizes its use.
// It is guarded by autoMu on the owning CopilotModel.
type autoModelResolution struct {
    selectedModel string
    sessionToken  string
    expiresAt     int64
}

// acquireAutoSession calls POST /models/session with empty model_hints so
// the Copilot API selects a model automatically from the caller's available
// catalog. It returns the full sessionResponse (selected_model +
// session_token + expires_at + available_models).
//
// If the API rejects empty hints, switch the body to
// `{"auto_mode":{"model_hints":["auto"]}}` (see plan assumption #1).
func acquireAutoSession(ctx context.Context, baseURL, copilotToken string, httpClient *http.Client, clientMachineID string) (*sessionResponse, error) {
    body := sessionRequestBody{
        AutoMode: sessionAutoMode{
            ModelHints: []string{}, // empty → API picks the model
        },
    }
    payload, err := json.Marshal(body)
    if err != nil {
        return nil, errors.Wrap(err, "copilot: failed to marshal auto session request")
    }

    req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+sessionPath, bytes.NewReader(payload))
    if err != nil {
        return nil, errors.Wrap(err, "copilot: failed to create auto session request")
    }
    req.Header.Set("Content-Type", "application/json")
    req.Header.Set("Authorization", "Bearer "+copilotToken)
    req.Header.Set("User-Agent", userAgentHeader)
    req.Header.Set("Openai-Intent", copilotOpenAIIntent)
    req.Header.Set("Copilot-Integration-Id", integrationID)
    req.Header.Set("Editor-Version", editorVersion)
    req.Header.Set("X-GitHub-Api-Version", copilotAPIVersion)
    req.Header.Set("X-Interaction-Id", newUUID())
    req.Header.Set("X-Client-Machine-Id", clientMachineID)
    req.Header.Set("X-Initiator", "user")
    req.Header.Set("Accept", "application/json")

    resp, err := httpClient.Do(req)
    if err != nil {
        return nil, errors.Wrap(err, "copilot: auto session request failed")
    }
    //nolint:errcheck // defer close in request path, error is irrelevant
    defer resp.Body.Close()

    respBody, err := io.ReadAll(resp.Body)
    if err != nil {
        return nil, errors.Wrap(err, "copilot: failed to read auto session response body")
    }
    if resp.StatusCode != http.StatusOK {
        return nil, errors.Errorf("copilot: auto session request returned status %d: %s", resp.StatusCode, redactErrorBody(respBody))
    }

    var sresp sessionResponse
    if err := json.Unmarshal(respBody, &sresp); err != nil {
        return nil, errors.Wrapf(err, "copilot: failed to decode auto session response (body: %s)", redactErrorBody(respBody))
    }
    if sresp.SessionToken == "" {
        return nil, errors.New("copilot: auto session response returned empty session_token")
    }
    if sresp.SelectedModel == "" {
        return nil, errors.New("copilot: auto session response returned empty selected_model")
    }
    return &sresp, nil
}
```

### New `CopilotModel` field (`copilot.go`)

Add to the `CopilotModel` struct:

```go
autoModel     *autoModelResolution // nil when auto mode is not in use
autoMu        *sync.Mutex          // guards autoModel; shared across WithTools copies
cancelAutoRefresh context.CancelFunc
```

Initialize in `NewCopilotChatModel` (alongside the existing `sessionMu` init):

```go
autoMu: &sync.Mutex{},
```

`autoModel` and `cancelAutoRefresh` start as their zero values (nil) and are
populated lazily on the first auto-mode call.

### New methods (`session.go`)

```go
// ensureAutoModel resolves the "auto" model sentinel to a concrete model ID
// via POST /models/session (empty model_hints). It caches the result
// (selected_model + session_token + expires_at) on the model and starts a
// background refresh goroutine. It returns the resolved model ID.
//
// It is a no-op fast path when a cached, unexpired resolution already exists.
// Concurrent calls are serialized via autoMu; the first call performs the
// network round-trip, subsequent calls reuse the cache.
func (m *CopilotModel) ensureAutoModel(ctx context.Context) (string, error) {
    if m.autoMu == nil {
        return "", errors.New("copilot: auto model resolution not initialized (nil autoMu)")
    }

    // Fast path: cached and not near expiry.
    if modelID, ok := m.cachedAutoModel(); ok {
        return modelID, nil
    }

    m.autoMu.Lock()
    defer m.autoMu.Unlock()

    // Re-check after acquiring the lock (double-checked locking).
    if modelID, ok := m.cachedAutoModelLocked(); ok {
        return modelID, nil
    }

    copilotToken := m.lockedToken.get()
    sresp, err := acquireAutoSession(ctx, m.baseURL, copilotToken, m.httpClient, m.clientMachineID)
    if err != nil {
        return "", errors.Wrap(err, "copilot: failed to resolve auto model")
    }

    m.applyAutoSession(sresp)
    m.cancelAutoRefresh = m.startAutoModelRefresh(ctx, sresp)
    return sresp.SelectedModel, nil
}

// cachedAutoModel returns the cached selected_model and ok=true when an
// auto resolution exists and is not near expiry. Safe for concurrent use.
func (m *CopilotModel) cachedAutoModel() (string, bool) {
    m.autoMu.Lock()
    defer m.autoMu.Unlock()
    return m.cachedAutoModelLocked()
}

// cachedAutoModelLocked is the lock-held fast path. Caller must hold autoMu.
func (m *CopilotModel) cachedAutoModelLocked() (string, bool) {
    if m.autoModel == nil || m.autoModel.selectedModel == "" {
        return "", false
    }
    if m.autoModel.expiresAt <= 0 {
        return m.autoModel.selectedModel, true // no expiry known → trust until refreshed
    }
    if time.Now().Unix()+refreshBufSecs >= m.autoModel.expiresAt {
        return "", false // near expiry → re-resolve
    }
    return m.autoModel.selectedModel, true
}

// applyAutoSession stores the auto session result and propagates the session
// token to the shared sessionToken field so setCommonRequestHeaders sends it
// on the chat/responses call. Caller must hold autoMu.
func (m *CopilotModel) applyAutoSession(sresp *sessionResponse) {
    m.autoModel = &autoModelResolution{
        selectedModel: sresp.SelectedModel,
        sessionToken:  sresp.SessionToken,
        expiresAt:     sresp.ExpiresAt,
    }
    // Propagate to the shared session-token field so the existing
    // Copilot-Session-Token header logic picks it up unchanged.
    if m.sessionToken != nil {
        m.sessionToken.mu.Lock()
        m.sessionToken.token = sresp.SessionToken
        m.sessionToken.expiresAt = sresp.ExpiresAt
        m.sessionToken.mu.Unlock()
    }
}

// startAutoModelRefresh launches a background goroutine that refreshes the
// auto model resolution (selected_model + session_token) before it expires,
// mirroring startSessionRefresh. Returns a cancel func.
func (m *CopilotModel) startAutoModelRefresh(ctx context.Context, sresp *sessionResponse) context.CancelFunc {
    ctx, cancel := context.WithCancel(ctx)
    if sresp == nil || sresp.SessionToken == "" || sresp.ExpiresAt <= 0 {
        cancel()
        return cancel
    }
    go func() {
        currentExpiresAt := sresp.ExpiresAt
        for {
            sleepSecs := currentExpiresAt - time.Now().Unix() - refreshBufSecs
            if sleepSecs < refreshMinSecs {
                sleepSecs = refreshMinSecs
            }
            select {
            case <-ctx.Done():
                return
            case <-time.After(time.Duration(sleepSecs) * time.Second):
            }
            copilotToken := m.lockedToken.get()
            newResp, err := acquireAutoSession(ctx, m.baseURL, copilotToken, m.httpClient, m.clientMachineID)
            if err == nil {
                m.autoMu.Lock()
                m.applyAutoSession(newResp)
                m.autoMu.Unlock()
                currentExpiresAt = newResp.ExpiresAt
                continue
            }
            if m.logger != nil {
                m.logger.Warnf("copilot: auto model refresh failed: %v", err)
            }
            // Reuse the existing exponential-backoff loop pattern from startSessionRefresh.
            backoffSecs := backoffInitialSecs
            for {
                jitter := time.Duration(cryptoRandIntn(backoffJitterSecs*2)-backoffJitterSecs) * time.Second
                select {
                case <-ctx.Done():
                    return
                case <-time.After(time.Duration(backoffSecs)*time.Second + jitter):
                }
                copilotToken = m.lockedToken.get()
                newResp, err = acquireAutoSession(ctx, m.baseURL, copilotToken, m.httpClient, m.clientMachineID)
                if err == nil {
                    m.autoMu.Lock()
                    m.applyAutoSession(newResp)
                    m.autoMu.Unlock()
                    currentExpiresAt = newResp.ExpiresAt
                    break
                }
                if m.logger != nil {
                    m.logger.Warnf("copilot: auto model refresh retry failed: %v", err)
                }
                backoffSecs *= 2
                if backoffSecs > backoffMaxSecs {
                    backoffSecs = backoffMaxSecs
                }
            }
        }
    }()
    return cancel
}

// ResolvedAutoModel returns the currently cached auto-resolved model ID and
// ok=true, or ""/false when auto mode is not in use or not yet resolved.
// Exposed for diagnostics and tests.
func (m *CopilotModel) ResolvedAutoModel() (string, bool) {
    if m.autoMu == nil {
        return "", false
    }
    m.autoMu.Lock()
    defer m.autoMu.Unlock()
    if m.autoModel == nil {
        return "", false
    }
    return m.autoModel.selectedModel, true
}
```

## Function signatures (summary of new/modified)

New (package-level):
- `const ModelAuto = "auto"` — `models.go`
- `func IsAutoModel(modelID string) bool` — `models.go`
- `func acquireAutoSession(ctx context.Context, baseURL, copilotToken string, httpClient *http.Client, clientMachineID string) (*sessionResponse, error)` — `session.go`

New (methods on `*CopilotModel`):
- `func (m *CopilotModel) ensureAutoModel(ctx context.Context) (string, error)` — `session.go`
- `func (m *CopilotModel) cachedAutoModel() (string, bool)` — `session.go`
- `func (m *CopilotModel) cachedAutoModelLocked() (string, bool)` — `session.go`
- `func (m *CopilotModel) applyAutoSession(sresp *sessionResponse)` — `session.go`
- `func (m *CopilotModel) startAutoModelRefresh(ctx context.Context, sresp *sessionResponse) context.CancelFunc` — `session.go`
- `func (m *CopilotModel) ResolvedAutoModel() (string, bool)` — `session.go`

Modified:
- `func NewCopilotChatModel(ctx context.Context, cfg *Config) (*CopilotModel, error)` — initialize `autoMu`.
- `func (m *CopilotModel) Generate(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.Message, error)` — resolve auto model.
- `func (m *CopilotModel) Stream(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error)` — resolve auto model.

## How "auto" flows through to the request

`Generate` and `Stream` today begin with:

```go
resolvedModel := m.resolveModel(opts...)
if err := m.ensureSessionToken(ctx, resolvedModel); err != nil {
    return nil, err
}
```

The new flow inserts an auto-resolution step **before** `ensureSessionToken`,
because `ensureSessionToken` keys off the model ID (`needsSessionToken` checks
`gpt-5*`). For auto mode the real model is unknown until the session call
returns, and that same session call already yields the session token — so we
skip the separate `ensureSessionToken` for the auto path.

Concrete change in `Generate` (`copilot_chat.go`):

```go
func (m *CopilotModel) Generate(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.Message, error) {
    resolvedModel := m.resolveModel(opts...)

    if IsAutoModel(resolvedModel) {
        selected, err := m.ensureAutoModel(ctx)
        if err != nil {
            return nil, err
        }
        // Inject the resolved model so buildChatRequest/buildResponsesRequest
        // and useResponsesAPI see the concrete ID, not "auto".
        opts = append([]model.Option{model.WithModel(selected)}, opts...)
        resolvedModel = selected
        // Session token was acquired by ensureAutoModel; skip ensureSessionToken.
    } else {
        if err := m.ensureSessionToken(ctx, resolvedModel); err != nil {
            return nil, err
        }
    }

    if m.useResponsesAPI(resolvedModel) {
        return m.generateResponses(ctx, in, opts...)
    }
    // ... unchanged chat-completions path
}
```

The same change is applied to `Stream` in `copilot_stream.go`.

Notes:
- `model.WithModel(selected)` is prepended to `opts` so it wins over any
  per-call `model.WithModel("auto")` (eino's `GetCommonOptions` applies later
  options on top, so prepending makes the resolved model the lowest-priority
  default while still allowing an explicit non-auto per-call override to
  win). If a caller explicitly passes `model.WithModel("gpt-4o")` per-call
  while `Config.Model == "auto"`, `resolveModel` returns `"gpt-4o"` (not
  auto), so the auto path is skipped entirely — no conflict.
- `useResponsesAPI(resolvedModel)` now sees the concrete selected model, so
  GPT-5-class selections are correctly routed to `/responses`.
- `buildChatRequest`/`buildResponsesRequest` receive the concrete model via
  the injected option; their existing "model must not be empty" guard
  passes because `selected` is non-empty (enforced by `acquireAutoSession`).

## Edge cases

| Case | Behavior |
|------|----------|
| `Config.Model = "auto"` (any case, with whitespace) | `IsAutoModel` → true; resolves via session. |
| `Config.Model = "AUTO"`, `"Auto "`, `" auto"` | Matched case-insensitively after trim. |
| Per-call `model.WithModel("auto")` while `Config.Model` is something else | `resolveModel` returns `"auto"` → auto path taken. |
| Per-call `model.WithModel("gpt-4o")` while `Config.Model == "auto"` | `resolveModel` returns `"gpt-4o"` → auto path skipped; uses gpt-4o directly. |
| Empty `Config.Model` and no per-call model | `resolveModel` returns `""`; auto path NOT taken (empty ≠ "auto"); existing "model must not be empty" error fires at build time. |
| Empty API key / token | Already rejected in `NewCopilotChatModel` ("at least one of GitHubToken or CopilotToken must be set"). |
| Session endpoint offline / 5xx | `acquireAutoSession` returns a wrapped error; `ensureAutoModel` wraps again with "failed to resolve auto model". No silent fallback. |
| Session endpoint returns 402/403 (free-tier quota exhausted) | Surfaced as error from `acquireAutoSession` (status %d: ...). Caller sees the quota message. |
| `selected_model` not supported on the caller's tier | Cannot happen by construction — the API selects from the caller's own catalog. |
| `selected_model` is a GPT-5-class model | `useResponsesAPI` routes to `/responses`; the session token from the auto call is sent via `Copilot-Session-Token`. |
| `selected_model` is a non-reasoning model (e.g. `gpt-4o`) | Routed to `/chat/completions`; temperature applied normally. |
| Concurrent `Generate` calls with `Model=auto` | `autoMu` serializes the first resolution; subsequent calls hit the cache fast path. `WithTools` copies share the `autoMu` pointer (like `sessionMu`), so tool-bound copies reuse the same resolution. |
| Session token near expiry | `cachedAutoModelLocked` returns false → `ensureAutoModel` re-resolves; background refresh also keeps it fresh. |
| `expires_at <= 0` in the response | Treated as "no expiry known"; the cached model is trusted until a refresh succeeds. Background refresh is not started (matches `startSessionRefresh`). |
| `WithTools` copy | `autoMu` is a `*sync.Mutex` initialized at construction, so copies share it (same pattern as `sessionMu`, `modelsMu`). `autoModel` and `cancelAutoRefresh` are pointer-shared indirectly via the mutex; copies read the same `*autoModelResolution`. |

## Error handling

Follows the repo convention (`emperror.dev/errors` wrapping, `copilot:`
prefix):

- `acquireAutoSession`: wraps marshal/create/request/read/decode errors with
  `errors.Wrap`; uses `errors.Errorf` for non-200 statuses; uses
  `errors.New` for empty `session_token`/`selected_model`.
- `ensureAutoModel`: wraps `acquireAutoSession` failure with
  `errors.Wrap(err, "copilot: failed to resolve auto model")`.
- `NewCopilotChatModel`: no new error paths; `autoMu` init cannot fail.
- All error messages redact response bodies via the existing
  `redactErrorBody` helper (500-char truncation).

## Validation plan

- `Config.Model` keeps `validate:"omitempty"` — `"auto"` is a non-empty
  string and passes. We deliberately do **not** add a `oneof=` enum because
  the Copilot model catalog is dynamic and any catalog ID (plus `"auto"`)
  must be accepted.
- `validate.Struct(cfg)` is already called in `NewCopilotChatModel`; no new
  validation call is needed.
- `IsAutoModel` is the single source of truth for the sentinel match; it is
  case-insensitive and trims whitespace, so `"AUTO"` / `" Auto "` are
  accepted. The resolved `selected_model` is sent verbatim (case preserved).
- `chatmodel.Config.Model` is `validate:"required"`; `"auto"` is non-empty
  and passes. No factory change needed.

## Test plan

### New unit tests — `auto_model_test.go` (package `copilot`, no build tag)

Uses `net/http/httptest` to stub `/models/session` and `/chat/completions`.

1. `TestIsAutoModel` — table: `"auto"`, `"AUTO"`, `" Auto "`, `"auto "` →
   true; `""`, `"gpt-4o"`, `"automatic"` → false.
2. `TestAcquireAutoSessionSuccess` — server asserts the request body is
   `{"auto_mode":{"model_hints":[]}}` and returns a `sessionResponse` with
   `selected_model="gpt-4o"`, `session_token="jwt"`, `expires_at` in the
   future. Asserts the returned `*sessionResponse` fields.
3. `TestAcquireAutoSessionEmptySelectedModel` — server returns
   `selected_model=""` → expect error containing "empty selected_model".
4. `TestAcquireAutoSessionNon200` — server returns 402 → expect error
   containing "status 402".
5. `TestEnsureAutoModelResolvesAndCaches` — build a `CopilotModel` via
   `newTestModel` with `Model: "auto"`; stub `/models/session` to return
   `selected_model="gpt-4o"`; call `ensureAutoModel` twice; assert the
   session endpoint is hit exactly once (cache fast path) and both calls
   return `"gpt-4o"`.
6. `TestGenerateAutoModel` — `Config{Model: "auto"}`; stub
   `/models/session` → `selected_model="gpt-4o"`, then stub
   `/chat/completions` to echo a message; assert `Generate` returns
   non-empty content and that the `/chat/completions` request body's
   `model` field is `"gpt-4o"` (not `"auto"`).
7. `TestGenerateAutoModelRoutesToResponses` — same as above but
   `selected_model="gpt-5"`; assert the `/responses` endpoint is hit
   (proves `useResponsesAPI` sees the resolved model).
8. `TestStreamAutoModel` — streaming variant of #6; assert the stream
   yields content and the request `model` is `"gpt-4o"`.
9. `TestGenerateAutoModelSessionFailure` — `/models/session` returns 500;
   assert `Generate` returns an error wrapping "failed to resolve auto
   model" and does **not** fall back to a hardcoded model.
10. `TestResolvedAutoModelAccessor` — after a successful `Generate`,
    `m.ResolvedAutoModel()` returns `("gpt-4o", true)`; before any call it
    returns `("", false)`.
11. `TestEnsureAutoModelConcurrent` — launch 10 goroutines calling
    `ensureAutoModel` simultaneously; assert the session endpoint is hit
    exactly once (double-checked locking works).

### New integration test — `auto_model_integration_test.go` (`//go:build integration`)

`TestIntegration_FreeTier_AutoModel`:
- Guarded by `requireFreeTierIntegration(t)` (reuse the existing helper in
  `free_tier_integration_test.go`).
- Builds `Config{GitHubToken: github_pat_...}` (no `Model`).
- Calls `m.Generate(ctx, [...], model.WithModel(copilot.ModelAuto))`.
- Asserts no error (or `t.Skip` on 402/403 quota).
- Asserts `m.ResolvedAutoModel()` returns a non-empty model ID.
- Logs the resolved model for visibility.

### Existing tests — no modification required

The existing `copilot_test.go`, `copilot_responses_test.go`,
`copilot_stream_test.go`, `models_test.go`, `session`-related tests, and
`free_tier_integration_test.go` are unaffected because the auto path is
gated on `IsAutoModel` and is opt-in.

## README update plan (`components/model/copilot/README.md`)

Add a new top-level section "## Auto model selection (`ModelAuto`)" after the
"## Free-tier Copilot support" section, covering:

- What `ModelAuto` (`"auto"`) does: defers model selection to the Copilot
  API via `POST /models/session` with empty `model_hints`.
- Why it's useful for free-tier: the available catalog is smaller and may
  change; `"auto"` always picks a tier-available model without code changes.
- Usage example (direct + via `chatmodel` factory):
  ```go
  m, err := copilot.NewCopilotChatModel(ctx, &copilot.Config{
      GitHubToken: os.Getenv("GITHUB_TOKEN"), // github_pat_...
      Model:       copilot.ModelAuto,
  })
  ```
  ```go
  m, err := chatmodel.New(ctx, &chatmodel.Config{
      Provider: "github-copilot",
      APIKey:   pat,
      Model:    "auto",
  })
  ```
- Per-call override: `model.WithModel(copilot.ModelAuto)` or
  `model.WithModel("gpt-4o")` to bypass auto for one call.
- Behavior notes: case-insensitive match; resolved model is cached and
  refreshed in the background; session token from the auto call is reused;
  no silent fallback on session failure (error is surfaced).
- Add `ModelAuto` to the Configuration table row for `Model`.

Optionally add a one-line note to `components/model/chatmodel/README.md`
that `Model: "auto"` is supported for the `github-copilot` provider.

## Dependencies

None. No `go.mod` changes. All new code uses existing imports already
present in `session.go` (`bytes`, `context`, `encoding/json`, `io`,
`net/http`, `sync`, `time`, `emperror.dev/errors`, `logrus`) and `models.go`
(`strings`).

## Verification checklist

Run from the repo root (`/projects/eino-ext`):

```bash
# Build everything
go build ./...

# Vet
go vet ./components/model/copilot/...
go vet ./components/model/chatmodel/...

# Unit tests for the copilot package (no integration tag)
go test ./components/model/copilot/...

# Unit tests for the chatmodel factory (regression)
go test ./components/model/chatmodel/...

# Lint (repo has .golangci.yml)
golangci-lint run ./components/model/copilot/... ./components/model/chatmodel/...

# Free-tier integration test (requires a real github_pat_ with Copilot
# Requests permission; skip if unavailable)
COPILOT_INTEGRATION=1 COPILOT_FREE_TIER=1 GITHUB_TOKEN=github_pat_... \
  go test -tags=integration -run 'TestIntegration_FreeTier_AutoModel' \
  ./components/model/copilot/...
```

Acceptance criteria:
- `go build ./...` succeeds.
- `go vet ./...` reports no issues.
- All new unit tests pass.
- All pre-existing unit tests still pass (no regressions).
- `golangci-lint` is clean for the touched packages.
- The free-tier integration test passes (or skips cleanly on quota
  exhaustion), and logs a non-empty resolved model ID.

## Open questions / risks

1. **Empty `model_hints` acceptance** — Assumption #1. If the live API
   rejects `{"auto_mode":{"model_hints":[]}}`, switch `acquireAutoSession`
   to send `{"auto_mode":{"model_hints":["auto"]}}`. This is a one-line
   change in `acquireAutoSession` and the unit test's body assertion. The
   integration test will catch this on first run.
2. **`selected_model` validity for non-GPT-5 models** — The existing
   `needsSessionToken` only returns true for `gpt-5*`. For auto mode we
   always acquire a session token (regardless of the selected model) and
   send it via `Copilot-Session-Token`. If the API rejects the session
   token for a non-premium model, the header should be harmless (it is
   already sent on every request when present). If it causes 400s, the
   fix is to skip sending `Copilot-Session-Token` when the resolved model
   is non-premium — but this contradicts the existing GPT-5 flow which
   always sends it. Defer to integration testing.
3. **Refresh of `selected_model` changing mid-conversation** — The
   background refresh may select a different model when the token
   refreshes. This is acceptable for auto mode (the user asked for "any
   available model") but should be documented. The cache fast path uses
   the most recently resolved model.
