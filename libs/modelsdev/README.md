# modelsdev — models.dev catalog, limits & cost

`modelsdev` provides a provider-generic [models.dev](https://models.dev)
catalog: per-model context/output limits and USD-per-million-token cost,
mirroring kilocode's `core/models-dev`.

It also provides a `CatalogPricer` implementing the
`callbacks/activity.Pricer` hook so `StepEnded.Cost` can be populated at the
source for every consumer of the activity event stream.

The package is policy-free: it never guesses a model id, and an unknown
`(provider, id)` pair simply reports `ok=false` so callers can decide how to
react (e.g. WARN and price at 0).

## Catalog

```go
cat := modelsdev.Load(ctx, modelsdev.LoadOptions{}) // network refresh, falls
                                                     // back to the embedded
                                                     // snapshot on any error

ctxWindow, output, ok := cat.Limits("anthropic", "claude-opus-4-5")
// ctxWindow = limit.input || limit.context, output = limit.output
```

`Load` tries a bounded network fetch of `<URL>/api.json` (default
`https://models.dev`, ~5s timeout) and falls back to the embedded
`api.json` snapshot on any error or timeout. `Catalog.Fresh` reports which
source populated the catalog. It never returns an error.

Combine `Limits` with `components/model/chatmodel.CapOutputTokens` to derive
the effective output cap:

```go
_, catalogOutput, _ := cat.Limits(provider, id)
cap := chatmodel.CapOutputTokens(catalogOutput, myCeiling)
```

## Pricing

```go
pricer := modelsdev.CatalogPricer{
    Catalog: cat,
    Resolve: func(gatewayModel string) (provider, id string, ok bool) {
        // Map a per-step gateway model name to a (pricingProvider, catalog id)
        // pair. Pricing may use a different provider bucket than limits, e.g.
        // limits from "github-copilot", pricing from "anthropic".
        ...
    },
}

h := activity.NewHandlerWithConfig(bus, activity.WithPricer(pricer))
```

Cost is `Σ tokens.type/1e6 * cost.type` over input, output, cache_read and
cache_write. **Reasoning tokens are a subset of output (CompletionTokens) and
are never priced separately.** An unresolvable model, an unknown catalog
entry, or a nil `Cost` all price at `0`.

### Ollama auto-conversion

When a `NameResolver` returns `"ollama"` as the provider, `CatalogPricer.Cost`
auto-converts it to `"ollama-cloud"` before the catalog lookup. This means:

- **Ollama Cloud models** (listed in the ollama-cloud provider bucket) are
  priced at their catalog rates.
- **Local ollama models** not present in the ollama-cloud bucket price at
  `0` — local ollama has no price.

Callers do not need to special-case ollama in their `NameResolver`: returning
`"ollama"` is sufficient; the conversion and local-model gating are handled
automatically.

## Design: cost breakdown, cache savings & context-window usage (planned)

> Status: **designed, not yet implemented.** `CatalogPricer.Cost` today
> returns a single `float64` total. This section specs a superset API that
> exposes *where* the money went and *how much prompt caching saved*, plus
> first-class context-window usage/remaining helpers, without breaking the
> existing `Cost(gatewayModel, Tokens) float64` surface (it becomes a thin
> wrapper around the new breakdown).

### Cost breakdown

Debugging "why did this step cost $X", or building a cost-by-component
dashboard, currently requires recomputing the breakdown from raw `Tokens` +
`Cost` outside the lib — duplicating the pricing rule (input/output/
cache_read/cache_write summation, reasoning excluded) that `costOf` already
encodes.

- New type `CostBreakdown`:

  ```go
  type CostBreakdown struct {
      Input      float64 // tokens.Input/1e6 * cost.Input
      Output     float64 // tokens.Output/1e6 * cost.Output
      CacheRead  float64 // tokens.Cache.Read/1e6 * cost.CacheRead
      CacheWrite float64 // tokens.Cache.Write/1e6 * cost.CacheWrite
      Total      float64 // Input+Output+CacheRead+CacheWrite (== today's Cost())
      Savings    float64 // see below; NOT subtracted from Total
  }
  ```

- `CatalogPricer.Breakdown(gatewayModel string, t activity.Tokens) (CostBreakdown, bool)`
  — `ok` mirrors today's resolve/lookup/cost-nil failure modes (breakdown is
  all-zero on `ok=false`, same policy-free "unknown ⇒ 0" rule as `Cost`).
- `Cost(gatewayModel, t)` becomes `Breakdown(...).Total` (or 0 if `!ok`) —
  call-site compatible, zero behavior change for existing consumers.

### Pricing savings (prompt-cache discount)

**Savings is the USD amount a cache hit avoided**, i.e. the delta between what
the cached-read tokens *would have cost* at the full input rate and what they
*actually* cost at the (cheaper) `cache_read` rate:

```
Savings = tokens.Cache.Read/1e6 * max(0, cost.Input - cost.CacheRead)
```

- Clamped at `0` so a catalog entry where `cache_read > input` never reports
  negative "savings" or inflates `Total`.
- Savings is **informational only** — it does NOT get subtracted from
  `Total`/`Cost()`. `Total` already reflects the actual (discounted)
  cache_read price billed; `Savings` is the counterfactual "vs. no caching"
  delta, useful for a "prompt caching saved you $X this session" metric.
- Cache *write* tokens are priced at `cost.CacheWrite` (often a premium over
  `cost.Input`, e.g. Anthropic's ~1.25×) and are **excluded from Savings** —
  writing to cache is a cost, not a discount, on the step that pays it; the
  payoff is the *later* cache_read discount, already captured above.
- Session/run-level aggregation (summing `Savings` across steps) is a caller
  concern; the lib only computes the per-step value.

### Prometheus surfacing (optional, in `callbacks/activity/metrics`)

Extend the existing `llm_cost_usd_total{model,agent}` accounting to also
expose `llm_cost_savings_usd_total{model,agent}` (from
`CostBreakdown.Savings`), and optionally a `llm_cost_usd_total{model,agent,
component=input|output|cache_read|cache_write}` labeled variant instead of
(or alongside) the single unlabeled counter, so a dashboard can chart cost
composition over time. `component` is a small fixed enum, safe to add without
breaking the collector's low-cardinality discipline.

### Context-window usage helpers

`Catalog.Limits` returns the *static* window/output caps; callers must still
hand-roll "how much of the window is used" / "am I close to the limit" logic
themselves.

- New type:

  ```go
  type ContextUsage struct {
      Used   int // tokens actually consumed (typically Tokens.Input)
      Window int // resolved context window (limit.input || limit.context)
  }
  func (u ContextUsage) Remaining() int    // max(0, Window-Used)
  func (u ContextUsage) Fraction() float64 // Used/Window, 0 when Window<=0
  func (u ContextUsage) NearLimit(threshold float64) bool // Fraction() >= threshold
  ```

- `(*Catalog) Usage(provider, id string, usedTokens int) (ContextUsage, bool)`
  — thin wrapper combining `Limits` + the constructor above; `ok` mirrors
  `Limits`'s.
- Intended call site: after a step ends, callers can do
  `usage, ok := cat.Usage(provider, id, se.Tokens.Input)` and decide whether
  to warn/compact (e.g. wire into `contextopt`/compaction triggers) using
  `usage.NearLimit(0.9)` instead of a hardcoded threshold against a hardcoded
  window constant. This stays a pure query helper (no side effects, no
  coupling to `contextopt`): it only answers "given this window and this
  usage, how full are we", leaving *what to do about it* to the caller — same
  policy-free posture as the rest of the package.

### Tests to add when implementing

- `costBreakdownOf`/`Breakdown`: component sums match `costOf`'s existing
  total; `Savings` math against a fixture with `cost.Input > cost.CacheRead`
  (e.g. anthropic opus: input $5, cache_read $0.5 → savings =
  reads/1e6 * 4.5); `Savings == 0` when `cost.CacheRead >= cost.Input` or
  `Tokens.Cache.Read == 0`.
- `ContextUsage`: `Fraction`/`NearLimit` at 0, mid, and over-limit usage;
  `Window<=0` ⇒ `Fraction() == 0` (no panic/NaN); `Catalog.Usage` `ok=false`
  propagation for unknown model, mirroring `Limits`.
- Backward compatibility: `Cost(...)` numeric result unchanged before/after
  introducing `Breakdown` (table-driven check that `Cost(m,t) ==
  Breakdown(m,t).Total` across known/unknown models and zero/non-zero cache
  tokens).

## Refreshing the snapshot

The catalog is embedded from a committed `api.json` snapshot for
reproducible, offline-capable builds:

```sh
make models-dev-refresh   # or: go generate ./libs/modelsdev
```

Review the diff before committing — this is a deliberate, reviewed refresh,
not an automatic one.
