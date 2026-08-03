# Plan: Fix Copilot "421 Misdirected Request" by resolving the plan-correct API host

Target repo: `github.com/webcenter-fr/eino-ext`, package
`components/model/copilot` (currently pinned in `rancher-doc-chat-api-k8s` at
`v0.0.0-20260803072243-4aa68ca7c8a0`).

## Symptom

Any Copilot API call — `GET /models`, `POST /chat/completions`,
`POST /responses`, `POST /models/session` — made against the library's
hardcoded default host fails immediately with:

```
HTTP/1.1 421 Misdirected Request
```

This reproduces for **any** business/enterprise-plan token, with **any**
header combination (verified empirically — adding `Editor-Version`,
`X-GitHub-Api-Version`, `Openai-Intent`, `X-Initiator` did not change the
result). It is not a header problem; it is a wrong-host problem.

## Root cause

`token.go`:

```go
const (
	tokenURLPath       = "/copilot_internal/v2/token"
	defaultAPIBase     = "https://api.github.com"
	defaultCopilotBase = "https://api.individual.githubcopilot.com"
	...
)

// ResolveBaseURL returns the Copilot API base URL for the given enterprise URL.
// When enterpriseURL is empty, it returns the default public Copilot API base
// (https://api.githubcopilot.com). When enterpriseURL is set, it returns
// https://copilot-api.{enterpriseURL}.
func ResolveBaseURL(enterpriseURL string) string {
	if enterpriseURL != "" {
		return fmt.Sprintf("https://copilot-api.%s", enterpriseURL)
	}
	return defaultCopilotBase
}
```

Two bugs here:

1. **The doc comment is stale/wrong**: it says the default is
   `https://api.githubcopilot.com`, but the constant is actually
   `https://api.individual.githubcopilot.com`. This suggests the constant was
   changed at some point without updating the comment, and without realizing
   the new default only works for one Copilot plan tier.
2. **The real bug**: `https://api.individual.githubcopilot.com` only serves
   **individual**-plan Copilot subscriptions. GitHub's own
   `/copilot_internal/v2/token` exchange response already tells you the
   correct host for the token's actual plan, in an `endpoints` object that
   the library currently ignores entirely:

   ```json
   {
     "token": "tid=...",
     "expires_at": 1785754027,
     "refresh_in": 1500,
     "endpoints": {
       "api": "https://api.business.githubcopilot.com",
       "origin-tracker": "https://origin-tracker.business.githubcopilot.com",
       "proxy": "https://proxy.business.githubcopilot.com",
       "telemetry": "https://telemetry.business.githubcopilot.com"
     },
     ...
   }
   ```

   (Field values vary by plan: `individual`, `business`, `enterprise`, or a
   custom enterprise slug.) `copilotTokenResponse` in `token.go` only
   declares `Token`/`ExpiresAt`/`RefreshIn` and silently drops `endpoints`,
   so nothing downstream ever learns the right host — `NewCopilotChatModel`
   falls back to `ResolveBaseURL(cfg.EnterpriseURL)`, which returns the wrong
   hardcoded individual host whenever `cfg.BaseURL` isn't explicitly set by
   the caller.

   Confirmed by manual reproduction:
   ```bash
   # 1. Exchange raw GitHub token for a Copilot bearer token + endpoints
   curl https://api.github.com/copilot_internal/v2/token \
     -H "Authorization: token <ghu_...>" \
     -H "User-Agent: copilot/1.0.70 (client/github/cli linux v24.16.0) term/unknown" \
     -H "Accept: application/json"
   # -> 200, endpoints.api = "https://api.business.githubcopilot.com"

   # 2. Same exchanged bearer token against the WRONG (hardcoded) host
   curl https://api.individual.githubcopilot.com/models \
     -H "Authorization: Bearer <exchanged-token>" ... 
   # -> 421 Misdirected Request

   # 3. Same exchanged bearer token against the CORRECT host from endpoints.api
   curl https://api.business.githubcopilot.com/models \
     -H "Authorization: Bearer <exchanged-token>" ...
   # -> 200 OK, full model catalog

   # 4. Also confirmed for POST /chat/completions on the wrong host -> 421
   #    (not just /models — every endpoint is affected)
   ```

## Impact

This is not just a startup-warning cosmetic issue. For any consumer whose
GitHub account has a **business** or **enterprise** Copilot plan (not
individual) and does not explicitly set `Config.BaseURL`, **every real chat
completion silently targets the wrong host and fails with 421** — not just
the `ListModels` pre-flight check. Any downstream app relying on the
library's default should be treated as currently broken for non-individual
plans until this is fixed.

## Fix plan

### 1. `token.go` — capture `endpoints.api` from the exchange response

```go
type copilotTokenResponse struct {
	Token     string                   `json:"token"`
	ExpiresAt int64                    `json:"expires_at"`
	RefreshIn int                      `json:"refresh_in"`
	Endpoints *copilotTokenEndpoints   `json:"endpoints,omitempty"`
}

type copilotTokenEndpoints struct {
	API           string `json:"api,omitempty"`
	OriginTracker string `json:"origin-tracker,omitempty"`
	Proxy         string `json:"proxy,omitempty"`
	Telemetry     string `json:"telemetry,omitempty"`
}
```

No other change needed in `exchangeGitHubTokenWithBase`/`exchangeGitHubToken`
— the existing `json.NewDecoder(...).Decode(&tokenResp)` call will populate
the new field automatically since it's already decoding into
`copilotTokenResponse`.

Also fix the stale doc comment on `ResolveBaseURL` to stop claiming
`https://api.githubcopilot.com` is the default (it isn't — the constant is
`api.individual.githubcopilot.com`); document that it's the **individual**
plan default and callers should prefer the exchange response's
`endpoints.api` when available.

### 2. `copilot.go` — prefer the exchange response's endpoint over the hardcoded default

In `NewCopilotChatModel`, `baseURL` is currently resolved *before* the token
exchange happens:

```go
baseURL := cfg.BaseURL
if baseURL == "" {
	baseURL = ResolveBaseURL(cfg.EnterpriseURL)
}
lockedToken := &copilotLockedToken{}
var cancelRefresh context.CancelFunc

if cfg.CopilotToken != "" {
	lockedToken.set(cfg.CopilotToken)
} else {
	tokenResp, err := exchangeGitHubToken(ctx, cfg.GitHubToken, cfg.EnterpriseURL, cfg.Timeout)
	if err != nil {
		return nil, errors.Wrap(err, "copilot: initial token exchange failed")
	}
	lockedToken.set(tokenResp.Token)
	cancelRefresh = startTokenRefresh(ctx, cfg, tokenResp, func(newToken string) {
		lockedToken.set(newToken)
	})
}
```

Change the ordering so the exchange (when it happens) can override the
resolved base URL, but an **explicit `cfg.BaseURL` always wins** (don't
surprise callers who deliberately pointed at a proxy/gateway, e.g. this
repo's `llm.url` override):

```go
baseURL := cfg.BaseURL

var lockedToken = &copilotLockedToken{}
var cancelRefresh context.CancelFunc

if cfg.CopilotToken != "" {
	lockedToken.set(cfg.CopilotToken)
	// No exchange response available for a pre-obtained bearer token, so we
	// can't learn the plan-correct endpoint here — fall back to the
	// hardcoded/enterprise-derived default. Callers using CopilotToken with
	// a business/enterprise-plan token MUST set cfg.BaseURL explicitly.
	if baseURL == "" {
		baseURL = ResolveBaseURL(cfg.EnterpriseURL)
	}
} else {
	tokenResp, err := exchangeGitHubToken(ctx, cfg.GitHubToken, cfg.EnterpriseURL, cfg.Timeout)
	if err != nil {
		return nil, errors.Wrap(err, "copilot: initial token exchange failed")
	}
	lockedToken.set(tokenResp.Token)

	if baseURL == "" {
		if tokenResp.Endpoints != nil && tokenResp.Endpoints.API != "" {
			baseURL = tokenResp.Endpoints.API
		} else {
			baseURL = ResolveBaseURL(cfg.EnterpriseURL)
		}
	}

	cancelRefresh = startTokenRefresh(ctx, cfg, tokenResp, func(newToken string) {
		lockedToken.set(newToken)
	})
}
```

Notes:
- `baseURL` stays a plain field set once at construction (matches existing
  design — no need to make it mutable/refreshable; a token's plan/endpoint
  doesn't change across refreshes in practice, and `startTokenRefresh`
  already discards the refreshed response's other fields today).
- **Do NOT add "log on refresh host change" hardening in this change.** The
  existing `startTokenRefresh` callback signature is `func(newToken string)`
  (token.go:91) and discards every other field of the refreshed response.
  Detecting a changed `endpoints.api` mid-session would require widening the
  callback to receive the full `*copilotTokenResponse` (or threading a
  separate `onEndpointsChange` hook), which is a larger refactor than this fix
  warrants and risks changing the host under live requests. Leave it out of
  scope; if desired later, track it as a separate task that widens the
  callback and audits every `onRefresh` call site.

### 3. `token.go` — public helper for external pre-flight callers (no live `CopilotModel` needed)

External consumers (e.g. `rancher-doc-chat-api-k8s`'s
`internal/server/server.go`, which does a lightweight pre-flight
`ListModels` call before constructing the real `CopilotModel`) currently
have no supported way to get a validated bearer token + correct base URL
without either (a) duplicating the exchange logic, which isn't exported, or
(b) passing a raw un-exchanged GitHub token straight to `ListModels` against
a guessed base URL — which is exactly the bug that produced the 421 in the
first place. Add a new exported helper in `token.go` (alongside
`exchangeGitHubToken` and `ResolveBaseURL`, which it wraps — keeping
token-resolution primitives in one file):

```go
// ResolvedToken is the result of exchanging a GitHub token for a Copilot
// bearer token, including the plan-correct API base URL to use for all
// subsequent Copilot API calls (models, chat/completions, responses).
type ResolvedToken struct {
	Token     string
	BaseURL   string
	ExpiresAt int64
}

// ResolveCopilotToken exchanges a raw GitHub token for a short-lived Copilot
// bearer token and the API base URL matching the token's actual Copilot plan
// (individual/business/enterprise), so callers that only need a one-off API
// call (e.g. a pre-flight ListModels check) don't have to guess the host or
// construct a full CopilotModel.
//
// Precedence of the returned BaseURL (mirrors NewCopilotChatModel):
//   1. explicit baseURL, when non-empty (caller override — e.g. a proxy/gateway);
//   2. endpoints.api from the token exchange response (plan-correct host);
//   3. ResolveBaseURL(enterpriseURL) fallback.
//
// When githubToken is empty the function returns an error: it does not read
// environment variables. Callers wanting env-var discovery should populate
// githubToken from os.Getenv("GITHUB_TOKEN") first, as NewCopilotChatModel does.
func ResolveCopilotToken(ctx context.Context, githubToken, enterpriseURL, baseURL string, timeout time.Duration) (*ResolvedToken, error) {
	if githubToken == "" {
		return nil, errors.New("copilot: githubToken must not be empty")
	}
	tokenResp, err := exchangeGitHubToken(ctx, githubToken, enterpriseURL, timeout)
	if err != nil {
		return nil, err
	}
	resolved := baseURL
	if resolved == "" {
		if tokenResp.Endpoints != nil && tokenResp.Endpoints.API != "" {
			resolved = tokenResp.Endpoints.API
		} else {
			resolved = ResolveBaseURL(enterpriseURL)
		}
	}
	return &ResolvedToken{
		Token:     tokenResp.Token,
		BaseURL:   resolved,
		ExpiresAt: tokenResp.ExpiresAt,
	}, nil
}
```

Notes:
- The `baseURL` parameter is added so the "explicit override always wins"
  rule from step 2 is consistent across both entry points. The consumer
  example below threads the operator's `llm.url` override through it; passing
  `""` enables auto-detection from `endpoints.api`.
- This requires exporting `exchangeGitHubToken`'s behavior without exporting
  its internals — the wrapper above is enough; no need to export
  `copilotTokenResponse` itself.
- The returned error from `exchangeGitHubToken` is already wrapped with
  "copilot:" context by the inner function, so it is returned as-is here.
  If a future refactor removes that inner wrapping, wrap here with
  `errors.Wrap(err, "copilot: token exchange failed")`.

Consumer-side follow-up (in `rancher-doc-chat-api-k8s`, after bumping the
pin): change `internal/server/server.go`'s pre-flight block from directly
calling `copilot.ListModels(fetchCtx, token, baseURL, timeout)` with the raw
`llm.apiKey` to:

```go
resolved, err := copilot.ResolveCopilotToken(fetchCtx, token, enterpriseURL, llmURL, 15*time.Second)
if err != nil {
    log.Warnf("copilot token exchange failed (context-window fallback and validation skipped): %v", err)
} else {
    live, err := copilot.ListModels(fetchCtx, resolved.Token, resolved.BaseURL, 15*time.Second)
    ...
}
```
where `llmURL` is the operator's explicit base-URL override (empty string when
unset, to enable auto-detection). Only relevant when `llm.apiKey` is a raw
GitHub token rather than an already-exchanged `GITHUB_COPILOT_TOKEN`/
`CopilotToken`-style bearer token — keep the existing raw-`ListModels` path as
a fallback for that case, mirroring `NewCopilotChatModel`'s `CopilotToken` vs
`GitHubToken` branching.

### 4. `models.go` — header parity (secondary, not the root fix)

While not the cause of the 421 (verified — headers alone don't fix it),
bring `listModelsWithClient`'s headers in line with the chat/session request
headers for consistency and to avoid a *different* future failure mode:

```go
req.Header.Set("Authorization", "Bearer "+copilotToken)
req.Header.Set("User-Agent", userAgentHeader)
req.Header.Set("Accept", "application/json")
req.Header.Set("Copilot-Integration-Id", integrationID)
req.Header.Set("Editor-Version", editorVersion)
req.Header.Set("X-GitHub-Api-Version", copilotAPIVersion)
```

### 5. `check.go` — use `endpoints.api` and stop double-exchanging (same bug class)

`Check()` has the same wrong-host bug and is **not** fixed by steps 1–2: it
resolves `baseURL` *before* any exchange (check.go:36-39) and `probeModels`
runs its own `exchangeGitHubToken` (check.go:81) but discards `endpoints.api`,
then calls `ListModels` against the hardcoded `ResolveBaseURL` host. So the
checkup still 421s for business/enterprise plans without an explicit
`BaseURL`, contradicting the plan's "fix every endpoint" goal. It also
exchanges the token **twice** (once in `probeTokenExchange`, once again in
`probeModels`) — wasteful and a behavior the fix should collapse.

Restructure `Check` so the exchange happens once and its `endpoints.api`
feeds both the token-exchange probe and the models probe, while keeping the
explicit `cfg.BaseURL` override on top:

```go
func Check(ctx context.Context, cfg *Config) checkup.Results {
	if cfg == nil {
		return checkup.Results{{
			Component: "copilot",
			Status:    checkup.StatusError,
			Error:     "config must not be nil",
		}}
	}

	if cfg.GitHubToken == "" && cfg.CopilotToken == "" {
		return checkup.Results{{
			Component: "copilot",
			Status:    checkup.StatusError,
			Error:     "at least one of GitHubToken or CopilotToken must be set",
		}}
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}

	var results checkup.Results
	var resolvedBase string

	if cfg.CopilotToken != "" {
		results = append(results, probeTokenExchangeSkipped())
		resolvedBase = cfg.BaseURL
		if resolvedBase == "" {
			resolvedBase = ResolveBaseURL(cfg.EnterpriseURL)
		}
	} else {
		// Single exchange feeds both the token probe and the models probe,
		// and lets endpoints.api override the hardcoded default when the
		// caller hasn't set cfg.BaseURL.
		resolved, err := exchangeGitHubToken(ctx, cfg.GitHubToken, cfg.EnterpriseURL, timeout)
		if err != nil {
			results = append(results, checkup.Result{
				Component: "copilot_token_exchange",
				Status:    checkup.StatusError,
				Error:     errors.Wrap(err, "failed to exchange GitHub token").Error(),
			})
			results = append(results, checkup.Result{
				Component: "copilot_models",
				Status:    checkup.StatusError,
				Error:     "dependency failed: token exchange required for /models probe",
			})
			return results
		}
		results = append(results, checkup.Result{
			Component: "copilot_token_exchange",
			Status:    checkup.StatusOK,
			Message:   fmt.Sprintf("token obtained, expires at %d", resolved.ExpiresAt),
		})
		resolvedBase = cfg.BaseURL
		if resolvedBase == "" {
			if resolved.Endpoints != nil && resolved.Endpoints.API != "" {
				resolvedBase = resolved.Endpoints.API
			} else {
				resolvedBase = ResolveBaseURL(cfg.EnterpriseURL)
			}
		}
	}

	// probeModels keeps using CopilotToken when present, otherwise reuses the
	// already-exchanged token (see updated signature below).
	token := cfg.CopilotToken
	if token == "" {
		token = resolved.Token
	}
	results = append(results, probeModels(ctx, resolvedBase, token, cfg, timeout))
	return results
}
```

Adjust `probeModels` to take the already-resolved `token` so it no longer
re-exchanges:

```go
func probeModels(ctx context.Context, baseURL, token string, cfg *Config, timeout time.Duration) checkup.Result {
	models, err := ListModels(ctx, token, baseURL, copilotCheckTimeout)
	// ... unchanged error/no-models/ok handling ...
}
```

Notes:
- `probeTokenExchange` and `probeTokenExchangeSkipped` stay exported-ish
  (package-level) for the existing `check_test.go` direct calls; the
  restructured `Check` inlines the success path but `probeTokenExchange` can
  remain as a helper or be removed if `check_test.go` is updated to call the
  new flow. Prefer keeping `probeTokenExchange` to minimize churn and keep
  `TestCheckTokenExchangeSkipped`/its sibling tests compiling.
- The early-return on exchange failure (two results: exchange error + models
  "dependency failed") preserves the existing "dependency failed" message
  shape that `check_test.go` implicitly relies on, while avoiding a second
  failed exchange attempt.

### 6. `copilot_embedding.go` — don't silently fall back to the individual host

`NewEmbedder` falls back to `ResolveBaseURL("")` (copilot_embedding.go:61-63)
when `baseURL` is empty — the same silent-wrong-host footgun. The embedder
takes explicit `copilotToken`/`baseURL` params (no exchange happens inside
it), so it cannot auto-detect `endpoints.api` itself; the fix is therefore to
**require** a non-empty `baseURL` rather than guess, and document that callers
should obtain it from `ResolveCopilotToken` (or `ResolveBaseURL` only when
they know the plan is individual).

```go
if baseURL == "" {
	return nil, errors.New("copilot: baseURL must not be empty; pass the plan-correct host from ResolveCopilotToken, or set it explicitly for individual-plan tokens via ResolveBaseURL(\"\")")
}
```

Update the existing `copilot_embedding_test.go` "default baseURL" case
(copilot_embedding_test.go:49-59), which currently asserts the empty-`baseURL`
path silently yields `defaultCopilotBase`: change it to assert a non-nil error
instead, and add a positive case that passes an explicit `defaultCopilotBase`
to confirm individual-plan usage still works when the caller is explicit.

This is a **behavior break** for callers that relied on the empty-`baseURL`
default, but that default was already broken for non-individual plans; making
it explicit forces callers to pick the right host. Call this out in the
README (step 10).

### 7. `integration_test.go` — acceptance tests that prove the fix against the real API

The existing integration suite has two problems relative to this fix:

1. `TestIntegration_ListModels` (integration_test.go:121-138) uses
   `ResolveBaseURL("")` and, in the `GITHUB_TOKEN` path, calls
   `exchangeGitHubToken` but discards `endpoints.api`. For a
   business/enterprise account this still 421s, so the suite cannot validate
   the fix as written.
2. There is **no test that actually proves** `NewCopilotChatModel` auto-detects
   the plan-correct host from the exchange. The model-based tests
   (`newIntegrationModel`) read `COPILOT_API_URL` into `cfg.BaseURL`
   (integration_test.go:38-40), which *overrides* auto-detection — so even
   after the fix they would pass by luck of the override, not because the
   fix works.

Add a dedicated acceptance-test layer. Crucially, `integration_test.go` is
in-package (`package copilot`, integration_test.go:3), so it can read the
unexported `m.baseURL` field directly — this makes the auto-detection
assertion **deterministic** (no debug proxy needed) and lets a negative
control prove the fix is what made the difference.

#### 7a. New helper `requireGitHubTokenIntegration`

The acceptance tests exercise the token-exchange path, so they need a raw
PAT, not a pre-obtained bearer token. Add a stricter gate (alongside the
existing `requireIntegration`):

```go
// requireGitHubTokenIntegration skips unless a raw GitHub PAT is available
// to exercise the /copilot_internal/v2/token exchange. The acceptance tests
// below depend on the exchange response's endpoints.api field, which only
// exists when a real exchange happens — a pre-obtained CopilotToken skips
// the exchange entirely (NewCopilotChatModel's CopilotToken branch).
func requireGitHubTokenIntegration(t *testing.T) {
	t.Helper()
	requireIntegration(t)
	if os.Getenv("GITHUB_TOKEN") == "" {
		t.Skip("GITHUB_TOKEN (raw PAT) not set — acceptance tests need the exchange path")
	}
}

// exchangeForTest performs a real token exchange for the test's GitHub PAT
// and returns the full response (including endpoints.api), as ground truth.
func exchangeForTest(t *testing.T) *copilotTokenResponse {
	t.Helper()
	resp, err := exchangeGitHubToken(context.Background(), os.Getenv("GITHUB_TOKEN"), "", 30*time.Second)
	if err != nil {
		t.Fatalf("exchangeGitHubToken: %v", err)
	}
	if resp.Endpoints == nil || resp.Endpoints.API == "" {
		t.Fatalf("exchange returned no endpoints.api — cannot validate plan-correct host detection; got %+v", resp)
	}
	return resp
}
```

The `endpoints.api`-present assertion in `exchangeForTest` is intentional: if
GitHub ever stops returning `endpoints.api`, the fix's auto-detection is moot
and the acceptance tests must surface that regression loudly rather than
silently passing via the fallback.

**Imports:** `integration_test.go` (integration_test.go:5-15) currently
imports `context`, `fmt`, `os`, `strings`, `testing`, `emperror.dev/errors`,
`model`, `schema` — but **not** `time` or `checkup`. The acceptance tests use
`time.Second`/`time.Now()` (7a, 7c) and `checkup.StatusOK`/`checkup.Result`
(7d), so add `"time"` and
`"github.com/webcenter-fr/eino-ext/libs/toolkit/checkup"` to the import block.
The build-tagged file already has the `//go:build integration` guard so
adding these imports won't affect the normal `go test` build.

#### 7b. `TestIntegration_AutoDetectBaseURL` — the primary acceptance test

Proves `NewCopilotChatModel` (with `GitHubToken`, no `BaseURL`) resolves the
plan-correct host from the exchange and that a real request succeeds (200,
not 421). It must construct `Config` directly so `COPILOT_API_URL` cannot
mask the bug:

```go
// TestIntegration_AutoDetectBaseURL proves the 421 fix: when a raw GitHub
// PAT is used with no explicit BaseURL, NewCopilotChatModel must resolve the
// plan-correct host from the exchange response's endpoints.api and a real
// Generate must succeed (not 421).
func TestIntegration_AutoDetectBaseURL(t *testing.T) {
	requireGitHubTokenIntegration(t)

	// Ground truth: a direct exchange tells us the plan-correct host.
	truth := exchangeForTest(t)
	t.Logf("plan-correct host (endpoints.api): %s", truth.Endpoints.API)

	// Construct the model with NO BaseURL and NO COPILOT_API_URL, so the
	// only way it can reach the right host is the fix in step 2.
	ctx := context.Background()
	m, err := NewCopilotChatModel(ctx, &Config{
		GitHubToken: os.Getenv("GITHUB_TOKEN"),
		Model:       "gpt-4o",
	})
	if err != nil {
		t.Fatalf("NewCopilotChatModel: %v", err)
	}

	// Deterministic field assertion (in-package access to m.baseURL):
	// the model must have auto-detected endpoints.api, not the hardcoded
	// individual default.
	if m.baseURL != truth.Endpoints.API {
		t.Fatalf("baseURL not auto-detected: got %q, want endpoints.api %q (hardcoded default is %q)",
			m.baseURL, truth.Endpoints.API, defaultCopilotBase)
	}
	if m.baseURL == defaultCopilotBase && truth.Endpoints.API != defaultCopilotBase {
		t.Fatalf("regression: model fell back to the individual-only host %q instead of %q",
			m.baseURL, truth.Endpoints.API)
	}

	// Real-API success proof: a Generate must return content, not a 421 error.
	msg, err := m.Generate(ctx, []*schema.Message{
		{Role: schema.User, Content: "Reply with the single word: ok"},
	}, model.WithMaxTokens(10))
	if err != nil {
		t.Fatalf("Generate against auto-detected host failed (expected 200, got: %v)", err)
	}
	if msg == nil || msg.Content == "" {
		t.Fatal("expected non-empty Generate content")
	}
	t.Logf("Generate OK on %s: %q", m.baseURL, truncate(msg.Content, 40))

	// Negative control — proves the fix is what made the difference. Only
	// meaningful for non-individual plans: on the individual plan the
	// hardcoded default IS the plan-correct host, so a 421 cannot be
	// reproduced there.
	if truth.Endpoints.API != defaultCopilotBase {
		_, err := ListModels(ctx, truth.Token, defaultCopilotBase, 30*time.Second)
		if err == nil {
			t.Fatalf("negative control failed: ListModels against the WRONG host %q unexpectedly succeeded (expected 421); the fix may be masking a real routing problem", defaultCopilotBase)
		}
		if !strings.Contains(err.Error(), "421") {
			t.Errorf("negative control: expected a 421 error against %q, got: %v", defaultCopilotBase, err)
		} else {
			t.Logf("negative control confirmed: same token against %q -> 421 (proves the auto-detection is what avoids the 421)", defaultCopilotBase)
		}
	} else {
		t.Logf("individual plan detected (endpoints.api == default %q): 421 negative control not applicable; auto-detection still asserted via field equality", defaultCopilotBase)
	}
}
```

This single test proves the complete fix chain: exchange → `endpoints.api`
captured → `baseURL` auto-detected → real request 200 → and (for non-individual
plans) that the old hardcoded host would have 421'd with the same token.

#### 7c. `TestIntegration_ResolveCopilotToken` — acceptance for the new helper

```go
// TestIntegration_ResolveCopilotToken proves ResolveCopilotToken returns a
// usable (token, plan-correct host) pair against the real API.
func TestIntegration_ResolveCopilotToken(t *testing.T) {
	requireGitHubTokenIntegration(t)
	ctx := context.Background()

	truth := exchangeForTest(t)

	// Auto-detect: empty baseURL -> endpoints.api wins.
	resolved, err := ResolveCopilotToken(ctx, os.Getenv("GITHUB_TOKEN"), "", "", 30*time.Second)
	if err != nil {
		t.Fatalf("ResolveCopilotToken: %v", err)
	}
	if resolved.Token == "" {
		t.Fatal("resolved.Token is empty")
	}
	if resolved.BaseURL != truth.Endpoints.API {
		t.Fatalf("resolved.BaseURL = %q, want endpoints.api %q", resolved.BaseURL, truth.Endpoints.API)
	}
	if resolved.ExpiresAt <= time.Now().Unix() {
		t.Errorf("resolved.ExpiresAt %d not in the future", resolved.ExpiresAt)
	}

	// The returned pair must actually work end-to-end.
	models, err := ListModels(ctx, resolved.Token, resolved.BaseURL, 30*time.Second)
	if err != nil {
		t.Fatalf("ListModels with resolved pair failed (expected 200): %v", err)
	}
	if len(models) < 20 {
		t.Errorf("expected ≥20 models from resolved pair, got %d", len(models))
	}
	t.Logf("ResolveCopilotToken -> %s, %d models", resolved.BaseURL, len(models))

	// Explicit override wins over endpoints.api (proves precedence on real API).
	const override = "https://copilot-override.example.invalid"
	resolved2, err := ResolveCopilotToken(ctx, os.Getenv("GITHUB_TOKEN"), "", override, 30*time.Second)
	if err != nil {
		t.Fatalf("ResolveCopilotToken with override: %v", err)
	}
	if resolved2.BaseURL != override {
		t.Fatalf("override lost: resolved2.BaseURL = %q, want explicit %q", resolved2.BaseURL, override)
	}
}
```

The override sub-case deliberately uses an invalid host and does **not** make
a request — it only asserts the field precedence. The exchange is real, so
this exercises the real API for the override-wins logic.

#### 7d. `TestIntegration_Check_GitHubToken` — acceptance for the checkup fix

```go
// TestIntegration_Check_GitHubToken proves copilot.Check uses the
// plan-correct host (from endpoints.api) when BaseURL is unset, so the
// /models probe reports OK instead of error/421 for non-individual plans.
func TestIntegration_Check_GitHubToken(t *testing.T) {
	requireGitHubTokenIntegration(t)
	ctx := context.Background()

	results := Check(ctx, &Config{GitHubToken: os.Getenv("GITHUB_TOKEN")})
	for _, r := range results {
		t.Logf("check: %s = %s (%s)", r.Component, r.Status, r.Message)
	}

	var exchange, models *checkup.Result
	for i := range results {
		switch results[i].Component {
		case "copilot_token_exchange":
			exchange = &results[i]
		case "copilot_models":
			models = &results[i]
		}
	}
	if exchange == nil || models == nil {
		t.Fatalf("expected copilot_token_exchange and copilot_models results, got %+v", results)
	}
	if exchange.Status != checkup.StatusOK {
		t.Errorf("copilot_token_exchange = %s, want OK", exchange.Status)
	}
	// The core acceptance assertion: /models probe must NOT 421. Before the
	// fix this was StatusError with a 421 message for non-individual plans.
	if models.Status == checkup.StatusError {
		t.Errorf("copilot_models = error (expected OK): %s — checkup is likely still using the wrong host", models.Error)
	}
	if models.Status != checkup.StatusOK {
		t.Errorf("copilot_models = %s, want OK", models.Status)
	}
}
```

#### 7e. Fix `TestIntegration_ListModels` for the `GITHUB_TOKEN` path

The existing test must also prefer `endpoints.api` so it stops 421-ing for
business/enterprise accounts when run with a raw PAT. Update the
`GITHUB_TOKEN` branch (integration_test.go:131-138):

```go
} else {
	resp, err := exchangeGitHubToken(context.Background(), os.Getenv("GITHUB_TOKEN"), "", 30*time.Second)
	if err != nil {
		t.Fatalf("exchangeGitHubToken: %v", err)
	}
	token = resp.Token
	if baseURL == "" {
		if resp.Endpoints != nil && resp.Endpoints.API != "" {
			baseURL = resp.Endpoints.API
		} else {
			baseURL = ResolveBaseURL("")
		}
	}
}
```

`newIntegrationModel` (integration_test.go:30-40) keeps reading
`COPILOT_API_URL` into `cfg.BaseURL`; that override path is correct and
unaffected. The step-2 fix handles the auto-detect path for model-based tests,
**but only when `COPILOT_API_URL` is unset** — so to actually exercise
auto-detection through `newIntegrationModel`, the acceptance test 7b above
constructs its own `Config` without `COPILOT_API_URL`. Do not point
`COPILOT_API_URL` at the real host when running 7b (it would mask the bug).

#### 7f. Running the acceptance tests

```bash
# Raw GitHub PAT from a business/enterprise-plan account (the bug only
# reproduces for non-individual plans; the individual-plan path is covered
# by the field-equality assertion in 7b).
COPILOT_INTEGRATION=1 GITHUB_TOKEN=ghp_... \
  go test -tags=integration -run 'TestIntegration_AutoDetectBaseURL|TestIntegration_ResolveCopilotToken|TestIntegration_Check_GitHubToken' \
  ./components/model/copilot/...
```

Do **not** set `COPILOT_API_URL` for these three tests — it would override
auto-detection and hide the bug. `GITHUB_COPILOT_TOKEN` is also unused by
them (they require the raw PAT exchange path).

### 8. Tests

- `token_test.go`:
  - Extend the token-exchange mock server test(s) to return an `endpoints.api`
    field and assert `ResolveCopilotToken` picks it up over the hardcoded
    default. Add a sub-case passing a non-empty `baseURL` to `ResolveCopilotToken`
    and assert it wins over `endpoints.api`.
  - Add a case where `endpoints` is absent from the response (older/different
    API shape) and confirm the fallback to `ResolveBaseURL` still works
    (backward compatibility).
  - Add a case with `githubToken == ""` asserting `ResolveCopilotToken`
    returns an error (new guard).
  - Add a case asserting `NewCopilotChatModel`'s internal baseURL resolution
    picks `endpoints.api` over the hardcoded default. Since `baseURL` is an
    unexported field, test this by pointing a mock server at a custom
    `endpoints.api` URL (use `httptest.NewServer` and put its URL in the
    exchange response's `endpoints.api`), constructing a model with a
    `GitHubToken` and `BaseURL: ""`, then issuing a `Generate`/`ListModels`
    call and asserting the request lands on the mock server (not on
    `api.individual.githubcopilot.com`).
  - Add a case with `cfg.BaseURL` explicitly set — confirm it always wins over
    both the hardcoded default and any `endpoints.api` value from the exchange.
- `check_test.go`:
  - Add a case (mock token-exchange server returning `endpoints.api` pointing
    at a second mock `/models` server) asserting `Check` probes `/models`
    against the `endpoints.api` host, not the hardcoded default. Use two
    separate `httptest.Server` instances to make the routing observable.
  - Update the existing direct-token tests to confirm they still pass after
    the `probeModels` signature change (they set `BaseURL` explicitly, so they
    are unaffected, but re-run to be sure).
- `copilot_embedding_test.go`: update per step 6 — the "default baseURL" case
  now expects an error; add an explicit-`defaultCopilotBase` positive case.
- `models_test.go`: no behavior change needed to existing tests other than
  the added headers in step 4; re-run to confirm.

### 9. Docs

- `README.md` (or package doc comment): document that the default Copilot
  API host only applies to **individual**-plan tokens, and that
  `NewCopilotChatModel` now auto-detects business/enterprise hosts from the
  token exchange response when `GitHubToken` (not a pre-obtained
  `CopilotToken`) is used. Call out the `CopilotToken`-without-`BaseURL`
  caveat explicitly (no exchange response available in that path → caller
  must set `BaseURL` themselves for non-individual plans).
- Document the new `ResolveCopilotToken` helper (signature, precedence, and
  the recommendation to use it for pre-flight/one-off callers), and note that
  `NewEmbedder` now **requires** an explicit `baseURL` (step 6 break).
- Note the enterprise-URL behavior change: when `EnterpriseURL` is set but
  the exchange response returns an `endpoints.api` that differs from
  `https://copilot-api.{enterpriseURL}`, the exchange value now wins (unless
  `BaseURL` is explicitly set). This is almost always correct but is a
  behavior change for enterprise-URL users.
- Add a short troubleshooting note: "`421 Misdirected Request` on any
  Copilot endpoint usually means the wrong plan-specific API host is being
  used — verify via a manual `/copilot_internal/v2/token` exchange and check
  the `endpoints.api` field, or set `BaseURL`/`CopilotToken`'s caller-side
  config explicitly."

## Validation checklist (after applying in the eino-ext repo)

### Offline (no network, no token)

```bash
go build ./...
go vet ./...
go test ./components/model/copilot/...
```

All must pass, including the new `token_test.go`/`check_test.go`/
`copilot_embedding_test.go` cases from steps 8 and 6.

### Acceptance (real GitHub Copilot API) — the proof

The acceptance tests in step 7 are the proof that the fix works against the
real API. Run them with a raw `GITHUB_TOKEN` (the bug only reproduces via the
exchange path; `GITHUB_COPILOT_TOKEN` skips it). Use a token from a
**business or enterprise** plan to exercise the non-individual negative
control; an individual-plan token still passes via the field-equality
assertion but skips the 421 negative control.

```bash
COPILOT_INTEGRATION=1 GITHUB_TOKEN=ghp_... \
  go test -tags=integration -v -run \
    'TestIntegration_AutoDetectBaseURL|TestIntegration_ResolveCopilotToken|TestIntegration_Check_GitHubToken' \
  ./components/model/copilot/...
```

Do **not** set `COPILOT_API_URL` or `GITHUB_COPILOT_TOKEN` for these three
tests — they would override auto-detection (`COPILOT_API_URL` → `cfg.BaseURL`)
or skip the exchange (`GITHUB_COPILOT_TOKEN` → `CopilotToken` branch), masking
the bug.

**Pass criteria (each must hold):**

1. `TestIntegration_AutoDetectBaseURL`:
   - `m.baseURL == endpoints.api` (field assertion) — auto-detection works.
   - `Generate` returns non-empty content (200, not 421).
   - For non-individual plans: `ListModels` against `defaultCopilotBase`
     fails with a `421` error (negative control proves the fix is what avoids
     the 421). For individual plans this sub-assertion logs "not applicable".
   - **This is the single test that proves the complete fix chain.**
2. `TestIntegration_ResolveCopilotToken`:
   - `resolved.BaseURL == endpoints.api` with empty override.
   - `ListModels(resolved.Token, resolved.BaseURL)` returns ≥20 models.
   - Explicit `baseURL` override wins over `endpoints.api`.
3. `TestIntegration_Check_GitHubToken`:
   - `copilot_token_exchange` = `OK`.
   - `copilot_models` = `OK` (before the fix this was `error`/421 for
     non-individual plans).

### Full integration suite (regression check)

```bash
COPILOT_INTEGRATION=1 GITHUB_TOKEN=ghp_... \
  go test -tags=integration ./components/model/copilot/...
```

Confirms the existing model/streaming/routing tests still pass and
`TestIntegration_ListModels` (fixed in 7e) now returns ≥20 models for
business/enterprise accounts instead of 421.

### Manual / behavior-break checks

- An account known to be on the **individual** plan: run the acceptance
  suite; `m.baseURL == defaultCopilotBase` and all tests pass (no regression
  for the common case).
- `NewEmbedder` with an empty `baseURL` now returns a construction error
  (behavior break from step 6) — confirm any in-repo callers of
  `NewEmbedder` pass an explicit `baseURL` or update them.
