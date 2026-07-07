# GitHub Copilot Chat Model Provider

## Summary

Add `components/model/copilot/` — a `model.ToolCallingChatModel` backed by the GitHub Copilot API (`https://api.githubcopilot.com`). The Copilot API is OpenAI-compatible, so the implementation wraps the existing `openai.ChatModel` and injects Copilot-specific auth + headers. Also update `components/model/chatmodel/` to route its existing `"github-copilot"` plan through the new component, and add an `APIKey` field to `chatmodel.Config`.

## Auth Architecture

```
GitHub PAT ──→ GET /copilot_internal/v2/token ──→ Copilot Bearer Token
                     (token header)                       │
                                                          ▼
                                                 Bearer token for /chat/completions
```

Two config fields, at least one required:

| Field | Source | Purpose |
|-------|--------|---------|
| `GitHubToken` | GitHub PAT with `read:user` scope | Auto-exchanged for a Copilot token. Enables background refresh. |
| `CopilotToken` | Pre-obtained Copilot bearer | Used directly. No automatic refresh. |

Environment variables fallback: `GITHUB_COPILOT_TOKEN` → `CopilotToken`, `GITHUB_TOKEN` → `GitHubToken`, `GITHUB_COPILOT_ENTERPRISE_URL` → `EnterpriseURL`.

## Files

```
components/model/copilot/
├── copilot.go          # Config, CopilotModel, NewCopilotChatModel, model.ToolCallingChatModel impl
├── copilot_test.go     # Table-driven tests (mock HTTP)
├── token.go            # Token exchange, background refresh loop, backoff
├── token_test.go       # Token exchange + refresh tests
├── models.go           # Model discovery: GET /models, parse/export ModelInfo
├── models_test.go      # Model discovery tests
├── check.go            # Checkup: token exchange + /models probe
├── check_test.go       # Checkup tests
└── README.md           # Documentation
```

Also:
- `components/model/chatmodel/chatmodel.go` — add `APIKey`, route `github-copilot` to copilot
- `components/model/chatmodel/chatmodel_test.go` — copilot construction tests

## Task List

### 1. `token.go` — Copilot Token Acquisition and Refresh

- [ ] **`exchangeGitHubToken(ctx, githubToken, enterpriseURL string, timeout time.Duration) (*copilotTokenResponse, error)`**
  - POST/GET `https://api.github.com/copilot_internal/v2/token` (or enterprise: `https://api.{enterprise}/copilot_internal/v2/token`)
  - Header: `Authorization: token {githubToken}`, `User-Agent: GitHubCopilotChat/0.52.0`, `X-GitHub-Api-Version: 2025-04-01`
  - Parse JSON response: `{token, expires_at (unix seconds), refresh_in (seconds)}`
  - HTTP timeout from config; wrap errors with context

- [ ] **`startTokenRefresh(ctx, cfg *Config, tokenResp *copilotTokenResponse, onRefresh func(newToken string)) context.CancelFunc`**
  - Calculate sleep = `expires_at - now - 60s_buf`; minimum 1s
  - Sleep until refresh window, then call `exchangeGitHubToken` again
  - On success: call `onRefresh(newToken)`, update `expires_at`, loop
  - On failure: exponential backoff starting at 15s, doubling to 600s max, ±15s jitter
  - Return `context.CancelFunc` to stop the loop
  - Skip entirely if `GitHubToken` is empty (no way to refresh from `CopilotToken`)

### 2. `copilot.go` — CopilotModel Wrapper

- [ ] **`Config` struct**
  ```go
  type Config struct {
      GitHubToken   string `validate:"omitempty" jsonschema:"description=GitHub PAT with read:user scope"`
      CopilotToken  string `validate:"omitempty" jsonschema:"description=Pre-obtained Copilot bearer token"`
      EnterpriseURL string `validate:"omitempty" jsonschema:"description=GitHub Enterprise domain"`
      Timeout       time.Duration `validate:"omitempty,gte=1000000000" jsonschema:"description=API request timeout"`
      TLSSkipVerify bool `validate:"omitempty" jsonschema:"description=Skip TLS certificate verification"`
  }
  ```

- [ ] **`NewCopilotChatModel(ctx, cfg) (*CopilotModel, error)`**
  1. `cfg == nil` → error
  2. Populate from env vars if fields are empty: `GITHUB_COPILOT_TOKEN`, `GITHUB_TOKEN`, `GITHUB_COPILOT_ENTERPRISE_URL`
  3. Programmatic check: at least one of `GitHubToken` or `CopilotToken` is set
  4. Call `validate.Struct(cfg)`
  5. Resolve initial copilot token:
     - If `CopilotToken` set → use directly
     - Else → call `exchangeGitHubToken(ctx, cfg.GitHubToken, ...)`
  6. Resolve `baseURL`: enterprise → `https://copilot-api.{domain}`, else → `https://api.githubcopilot.com`
  7. Build inner `openai.ChatModel`:
     - `BaseURL` = resolved Copilot base URL
     - `APIKey` = `""` (empty, so `setCommonHeaders` won't overwrite our Authorization header)
     - `Timeout` = config timeout or 60m default
     - `HTTPClient` = insecure if `TLSSkipVerify`
     - `Model` is NOT set here — it's set per-request by chatmodel or the caller
  8. If using `GitHubToken`, start background refresh with `startTokenRefresh`
  9. Return `CopilotModel{inner, token, cfg, ...}`

- [ ] **`CopilotModel` struct**
  ```go
  type CopilotModel struct {
      inner         model.ToolCallingChatModel
      copilotToken  string
      tokenMu       sync.RWMutex
      baseURL       string
      cfg           *Config
      cancelRefresh context.CancelFunc
  }
  ```

- [ ] **Per-request header injection** — method `authHeaders() model.Option`
  ```go
  func (m *CopilotModel) authHeaders() model.Option {
      m.tokenMu.RLock()
      token := m.copilotToken
      m.tokenMu.RUnlock()
      return openai.WithExtraHeader(map[string]string{
          "Authorization":           "Bearer " + token,
          "Copilot-Integration-ID":  "vscode-chat",
          "Editor-Version":          "vscode/1.100.0",
          "Editor-Plugin-Version":   "copilot-chat/0.52.0",
          "User-Agent":              "GitHubCopilotChat/0.52.0",
          "Openai-Intent":           "conversation-agent",
      })
  }
  ```

- [ ] **Interface implementation** — each method appends `m.authHeaders()` to opts
  ```go
  func (m *CopilotModel) Generate(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.Message, error)
  func (m *CopilotModel) Stream(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error)
  func (m *CopilotModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error)
  func (m *CopilotModel) BindTools(tools []*schema.ToolInfo) error
  func (m *CopilotModel) GetType() string
  func (m *CopilotModel) IsCallbacksEnabled() bool
  ```

- [ ] **Compile-time checks**
  ```go
  var _ model.ToolCallingChatModel = (*CopilotModel)(nil)
  var _ model.ChatModel = (*CopilotModel)(nil)
  ```

### 3. `models.go` — Model Discovery

- [ ] **`ModelInfo` struct** — subset of `/models` response relevant to callers
  ```go
  type ModelInfo struct {
      ID                     string
      Name                   string
      MaxContextWindowTokens int
      MaxOutputTokens        int
      SupportsToolCalls      bool
      SupportsStreaming      bool
      SupportsReasoning      bool
      SupportsVision         bool
      MaxPromptImageSize     int
      IsMessagesAPI          bool // supported_endpoints contains /v1/messages
  }
  ```

- [ ] **`ListModels(ctx, copilotToken, baseURL string, timeout time.Duration) ([]ModelInfo, error)`**
  - `GET {baseURL}/models` with `Authorization: Bearer {copilotToken}`
  - Filter: only models where `model_picker_enabled == true` and `policy.state != "disabled"`
  - Parse and return `[]ModelInfo`

- [ ] **Package-level comment**: `// Package copilot provides a GitHub Copilot chat model implementation.`

### 4. `check.go` — Connectivity Checkup

- [ ] **`Check(ctx, cfg *Config) checkup.Results`**
  1. If `GitHubToken` set: exchange for Copilot token, result = `"ok"` if successful, `"error"` if fails
  2. Call `ListModels` to probe `/models` endpoint
  3. Result: `"ok"` if models returned, `"limited"` if 200 but empty, `"error"` if HTTP error
  4. Return `[]checkup.Result` wrapping each step with name + status

- [ ] Follow existing checkup patterns from `components/tool/argocd/check.go`

### 5. `chatmodel.go` — Factory Changes

- [ ] Add `APIKey` field to `chatmodel.Config`:
  ```go
  APIKey string `validate:"omitempty" jsonschema:"description=Provider API key or token (Copilot bearer token for github-copilot)"`
  ```

- [ ] Add `newCopilot` function:
  - Map `cfg.BaseURL` → copilot base URL, `cfg.APIKey` → `Config.CopilotToken`
  - Map timeout, TLS, model, temperature, reasoning effort, max output tokens
  - Call `copilot.NewCopilotChatModel(ctx, copilotCfg)`

- [ ] Change `New()` switch:
  ```go
  case "github-copilot":
      return newCopilot(ctx, cfg)
  ```

- [ ] Update `newOpenAI` to pass `cfg.APIKey` through to `openai.ChatModelConfig.APIKey` (was previously always empty)

### 6. `chatmodel_test.go` — Test Updates

- [ ] Test `New("github-copilot", ...)` constructs without error
- [ ] Test that missing APIKey with `github-copilot` plan fails at copilot.NewCopilotChatModel (via nil config handling)
- [ ] Table-driven test for copilot config mapping

### 7. Tests (mocked HTTP)

- [ ] **`token_test.go`**: Mock `/copilot_internal/v2/token` endpoint
  - Success: returns valid token JSON
  - Error: non-200 status, malformed JSON, missing fields
  - Refresh: verify backoff behavior, verify token update callback

- [ ] **`copilot_test.go`**: Mock `/chat/completions` via fake HTTP server
  - Model construction with `CopilotToken`
  - Model construction with `GitHubToken` (mock exchange)
  - Model construction with neither → error
  - `Generate` injects correct headers
  - `Stream` injects correct headers
  - `WithTools` propagates to inner model

- [ ] **`models_test.go`**: Mock `/models` endpoint
  - Filters disabled models
  - Parses capabilities correctly

### 8. `README.md`

- [ ] Component description, which eino abstraction it implements
- [ ] Constructor snippet with both auth modes
- [ ] Environment variables table
- [ ] Header list and rationale
- [ ] Enterprise setup example

## Design Decisions

1. **No OAuth device flow in the library.** The device flow requires user interaction (visiting a URL, entering a code), which is a UI concern. Providing GitHub PAT or pre-obtained Copilot token covers both use cases: CLI apps can use copilot-api to obtain a token, web apps can implement their own device flow.

2. **Per-request token injection via `WithExtraHeader`.** The inner `openai.ChatModel` has `APIKey=""`, so `setCommonHeaders` skips setting `Authorization`. Our `authHeaders()` method injects `Authorization` + Copilot-specific headers on every `Generate`/`Stream` call, reading the latest token under `RLock`. This handles token refresh transparently.

3. **Copilot-specific headers are hardcoded.** VS Code-specific headers (`copilot-integration-id`, `editor-version`, `editor-plugin-version`) are used because they are required by the Copilot API. These are well-known from copilot-api and KiloCode implementations. The `Openai-Intent: conversation-agent` header mirrors what Copilot CLI/VS Code sends.

4. **No `Model` field in copilot Config.** The model is set per-request by the caller (typically via `chatmodel.Config.Model` or by the user directly on the openai wrapper). This avoids duplication.

5. **`GitHubToken` path enables automatic refresh.** The token exchange response includes `expires_at` and `refresh_in`. The background goroutine refreshes before expiry. If refresh fails, it retries with exponential backoff.

## Key Risks

- **Token exchange endpoint may change.** The `/copilot_internal/v2/token` endpoint is undocumented. Mitigation: wrap in a clear error, and `CopilotToken` bypass exists.
- **Header requirements may drift.** Copilot API may reject requests with unexpected headers. Mitigation: follow copilot-api's header set which is battle-tested.
- **`go-openai` update could change `setCommonHeaders` behavior.** If the guard `if c.config.authToken != ""` changes, our header injection via ExtraHeader would break. Mitigation: the test suite verifies headers reach the server.

## Open Questions

- None. All design decisions resolved above.
