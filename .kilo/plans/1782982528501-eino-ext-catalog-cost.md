# Plan A — eino-ext: models.dev catalog, per-model limits & cost (shared lib)

Repo: `github.com/webcenter-fr/eino-ext` (owned by us).
Consumed by Plan B (`rancher-doc-chat-api-k8s`). **Release/tag this before the app bumps its `go.mod`.**

## Goal
Provide reusable, provider-generic primitives, mirroring kilocode's `core/models-dev`:
1. A models.dev **catalog** (embedded snapshot + optional refresh) exposing per-model
   context/output limits and cost.
2. A **context/output resolver** so apps stop hardcoding context windows.
3. A **cost pricer** and an **activity handler pricing hook** so `StepEnded.Cost` is
   populated at the source for every consumer.
4. *(Optional)* a generic **Prometheus collector** for token/cost counters.

## Background (verified)
- kilocode sources per-model `limit.context`/`limit.output`/`cost` from
  `https://models.dev/api.json`, shape `providers[id].models[id]` with
  `limit{context,input?,output}` and `cost{input,output,cache_read?,cache_write?}`
  (USD per **million** tokens). See kilocode `packages/core/src/models-dev.ts`.
- The same model has different limits/cost per **provider bucket** (e.g. github-copilot
  opus ≈ 144k–160k ctx, input 128k, cost `{0,0}`; anthropic opus = 200k/1M ctx,
  cost input $5 / output $25 / cache_read $0.5 / cache_write $6.25).
- Existing `components/model/chatmodel` already has `OutputTokenMax = 32_000` and
  `CapOutputTokens(modelOutputLimit, ceiling)`; reuse them.
- Existing `callbacks/activity`:
  - `StepEnded{ Finish string; Cost float64; Tokens Tokens }` (`event.go:131`).
  - `Tokens{Input, Output, Reasoning int; Cache CacheTokens{Read, Write}}` (`event.go:104`).
  - `stepEnded(finish, usage)` (`handler.go:342`) fills Tokens but **never Cost**, and
    `NewHandler(bus)` (`handler.go:63`) exposes **no pricing hook** — this is what we add.
  - `StepStarted{Agent, Model}` (`handler.go:171`) carries the per-step model name.

## Design decisions (locked)
- Catalog fetch: **blocking with short timeout (~3–5s) + small retry**, invoked by the
  caller during init; **fallback to embedded snapshot** on any error/timeout. Hold parsed
  catalog in memory. No mandatory on-disk cache.
- Embed the **full** `api.json` (committed for reproducible builds); refresh at build via a
  `go generate`/Makefile target.
- URL override via env (e.g. `EINO_MODELS_URL`, default `https://models.dev`).
- **Limits provider** and **pricing provider** are independent inputs supplied by the caller
  (pricing may differ from limits, e.g. limits=github-copilot, pricing=anthropic).
- Model matching is **exact catalog id** (no fuzzy guessing); caller supplies the id(s).
  Context window value = `limit.input || limit.context` (input-budget semantics).
- Pricer cost = `Σ tokens.type/1e6 * cost.type` over input, output, cache_read, cache_write.
  **Reasoning tokens are a subset of output/CompletionTokens — do NOT price separately.**
  Unknown model/provider → cost `0` (and lib returns ok=false so callers can WARN).

## Tasks (ordered)
1. **New catalog package** `components/model/modelsdev` (or `.../catalog`):
   - `//go:embed api.json` (committed snapshot) + `api.json` refresh target
     (`go generate`/Makefile: curl models.dev → write file; keep committed copy as fallback).
   - Types matching models.dev: `Provider{ID, Name, Models map[string]Model}`,
     `Model{Limit{Context, Input, Output int}, Cost *Cost}`,
     `Cost{Input, Output, CacheRead, CacheWrite float64}`.
   - `Load(ctx, opts)`: try network (`<url>/api.json`, timeout+retry) → on failure parse
     embedded. Return an in-memory `Catalog` + a bool/flag indicating source (fresh vs embedded).
   - Lookups:
     - `(*Catalog) Model(provider, id string) (Model, bool)`.
     - `(*Catalog) Limits(provider, id string) (contextWindow, output int, ok bool)`
       where `contextWindow = input||context`; combine with `chatmodel.CapOutputTokens`
       for the effective output cap.
2. **Pricer** (in catalog package):
   - `type NameResolver func(gatewayModel string) (provider, id string, ok bool)` — supplied
     by caller to map per-step gateway model names → (pricingProvider, catalog id).
   - `type CatalogPricer struct { Catalog; Resolve NameResolver }` implementing
     `Cost(gatewayModel string, t activity.Tokens) float64`. Missing → 0.
   - Unit-price helper `costOf(Cost, Tokens) float64` with the summation rule above.
3. **Activity handler pricing hook** (`callbacks/activity`):
   - Add `Pricer` interface `Cost(model string, t Tokens) float64` and an option
     `WithPricer(Pricer)` (functional option or `NewHandlerWithConfig`). Keep `NewHandler(bus)`
     backward-compatible (nil pricer ⇒ Cost stays 0).
   - In `stepEnded`/its caller (`handler.go:207,342`), when a pricer is set, compute
     `se.Cost = pricer.Cost(stepModel, se.Tokens)`. Thread the step's model name through
     (already available from `StepStarted.Model`/`modelName(info)`).
4. *(Optional)* **Prometheus collector** subpackage `callbacks/activity/metrics`:
   - Subscribes to the activity `Bus`; on `StepEnded` increments
     `llm_tokens_total{model,agent,type=input|output|reasoning|cache_read}` and
     `llm_cost_usd_total{model,agent}` (provider label optional, from caller).
   - Register on a `prometheus.Registerer` passed by the app (no global side effects).
   - Keep label set small to bound cardinality.
5. **Tests**:
   - Catalog parse + lookup (limits, input||context, output).
   - Offline/embedded fallback (force network failure ⇒ embedded used).
   - Pricer math: input+output+cache_read(+write); reasoning NOT double-counted;
     unknown model ⇒ 0; copilot bucket (`cost {0,0}`) ⇒ 0.
   - Activity handler: with pricer, `StepEnded.Cost` populated; without pricer, stays 0.
   - `CapOutputTokens` interplay with catalog output.
6. **Release**: tag a new version; update CHANGELOG/README of the lib.

## Risks / notes
- Copilot bucket prices at $0 — that's a *caller* concern (Plan B uses a vendor
  `pricingProvider`); lib stays policy-free.
- Catalog key drift: exact-id only; lib returns `ok=false` so callers WARN.
- Keep the handler change backward-compatible (existing `NewHandler(bus)` callers unaffected).
- Bounded refresh timeout so app readiness probes aren't at risk.

## Validation
- `go test ./...` in eino-ext (new catalog + pricer + handler tests).
- Manual: load with network disabled ⇒ embedded snapshot; sample pricing for
  anthropic opus matches expected $ from token counts.

## Design enhancement — cost breakdown, cache savings & context-window usage

Status: **designed, not yet implemented.** `CatalogPricer.Cost` today returns a single
`float64` total; this section specs a superset API that exposes *where* the money went
and *how much prompt caching saved*, plus first-class context-window usage/remaining
helpers, without breaking the existing `Cost(gatewayModel, Tokens) float64` surface (it
becomes a thin wrapper around the new breakdown).

### 1. Cost breakdown

Problem: callers (and the optional Prometheus collector) currently only see one number.
Debugging "why did this step cost $X" or building a cost-by-component dashboard requires
recomputing the breakdown from raw `Tokens` + `Cost` outside the lib — duplicating the
pricing rule (input/output/cache_read/cache_write summation, reasoning excluded) that
`costOf` already encodes.

- New type `CostBreakdown`:
  ```go
  type CostBreakdown struct {
      Input      float64 // tokens.Input/1e6 * cost.Input
      Output     float64 // tokens.Output/1e6 * cost.Output
      CacheRead  float64 // tokens.Cache.Read/1e6 * cost.CacheRead
      CacheWrite float64 // tokens.Cache.Write/1e6 * cost.CacheWrite
      Total      float64 // Input+Output+CacheRead+CacheWrite (== current Cost())
      Savings    float64 // see below; NOT subtracted from Total
  }
  ```
- `(*Catalog) or CatalogPricer.Breakdown(gatewayModel string, t activity.Tokens) (CostBreakdown, bool)`
  — `ok` mirrors today's resolve/lookup/cost-nil failure modes (breakdown is all-zero on
  `ok=false`, same policy-free "unknown ⇒ 0" rule as `Cost`).
- `Cost(gatewayModel, t)` becomes `Breakdown(...).Total` (or 0 if `!ok`) — call-site
  compatible, zero behavior change for existing consumers.

### 2. Pricing *savings* (prompt-cache discount)

Definition: **Savings is the USD amount a cache hit avoided**, i.e. the delta between
what the cached-read tokens *would have cost* at the full input rate and what they
*actually* cost at the (cheaper) `cache_read` rate:

```
Savings = tokens.Cache.Read/1e6 * max(0, cost.Input - cost.CacheRead)
```

- Clamped at 0 (`max(0, …)`) so a pathological catalog entry where `cache_read >
  input` (shouldn't happen, but the lib is policy-free about upstream data quality)
  never reports a negative "savings" or inflates `Total`.
- Savings is **informational only** — it does NOT get subtracted from `Total`/`Cost()`.
  `Total` already reflects the actual (discounted) cache_read price billed; `Savings` is
  the counterfactual "vs. no caching" delta, useful for a "prompt caching saved you
  $X this session" metric.
- Cache *write* tokens are priced at `cost.CacheWrite` (often a premium over `cost.Input`,
  e.g. Anthropic's ~1.25×) and are **excluded from Savings** — writing to cache is a cost,
  not a discount, on the step that pays it; the payoff is the *later* cache_read discount,
  already captured above.
- Session/run-level aggregation (sum of `Savings` across steps) is a caller concern (e.g.
  fold `CostBreakdown.Savings` into a running total alongside `StepEnded` handling); the
  lib only computes the per-step value.

### 3. Prometheus surfacing (optional, in `callbacks/activity/metrics`)

- Extend `llm_cost_usd_total{model,agent}` accounting to also expose
  `llm_cost_savings_usd_total{model,agent}` (from `CostBreakdown.Savings`), and optionally
  a `llm_cost_usd_total{model,agent,component=input|output|cache_read|cache_write}` labeled
  variant instead of (or alongside) the single unlabeled counter, so a dashboard can chart
  cost composition over time. Keep the existing low-cardinality label discipline (see
  `libs/modelsdev/README.md`'s cardinality note) — `component` is a small fixed enum, safe
  to add.

### 4. Context-window usage helpers

Problem: `Catalog.Limits` returns the *static* window/output caps; callers must still
hand-roll "how much of the window is used" / "am I close to the limit" logic themselves.

- New type:
  ```go
  type ContextUsage struct {
      Used   int // tokens actually consumed (typically usage.PromptTokens, i.e. Tokens.Input)
      Window int // resolved context window (limit.input || limit.context)
  }
  func (u ContextUsage) Remaining() int       // max(0, Window-Used)
  func (u ContextUsage) Fraction() float64    // Used/Window, 0 when Window<=0 (avoid div/0)
  func (u ContextUsage) NearLimit(threshold float64) bool // Fraction() >= threshold, e.g. 0.9
  ```
- `(*Catalog) Usage(provider, id string, usedTokens int) (ContextUsage, bool)` — thin
  wrapper combining `Limits` + the constructor above; `ok` mirrors `Limits`'s.
- Intended call site: after `stepEnded`/`OnEnd`, callers can do
  `usage, ok := cat.Usage(provider, id, se.Tokens.Input)` and decide whether to warn/compact
  (e.g. wire into `contextopt`/compaction triggers) using `usage.NearLimit(0.9)` instead of
  a hardcoded threshold against a hardcoded window constant.
- This stays a pure query helper (no side effects, no coupling to `contextopt`): it only
  answers "given this window and this usage, how full are we", leaving *what to do about
  it* (compact, warn, refuse) to the caller — same policy-free posture as the rest of the
  package.

### Tests to add when implementing
- `costBreakdownOf`/`Breakdown`: component sums match `costOf`'s existing total; `Savings`
  math against a fixture with `cost.Input > cost.CacheRead` (e.g. anthropic opus:
  input $5, cache_read $0.5 → savings = reads/1e6 * 4.5); `Savings == 0` when
  `cost.CacheRead >= cost.Input` or `Tokens.Cache.Read == 0`.
- `ContextUsage`: `Fraction`/`NearLimit` at 0, mid, and over-limit usage; `Window<=0` ⇒
  `Fraction() == 0` (no panic/NaN); `Catalog.Usage` `ok=false` propagation for unknown
  model, mirroring `Limits`.
- Backward compatibility: `Cost(...)` numeric result unchanged before/after introducing
  `Breakdown` (property-test style: `Cost(m,t) == Breakdown(m,t).Total` for a table of
  fixtures spanning known/unknown models and zero/non-zero cache tokens).

