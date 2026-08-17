package modelsdev

import "github.com/webcenter-fr/eino-ext/callbacks/activity"

// OllamaProvider is the catalog key for local ollama when supplied by a
// NameResolver. CatalogPricer.Cost auto-converts it to OllamaCloudProvider
// before catalog lookup.
const OllamaProvider = "ollama"

// OllamaCloudProvider is the catalog key for the ollama-cloud pricing bucket.
const OllamaCloudProvider = "ollama-cloud"

// NameResolver maps a per-step gateway model name (as carried on
// activity.StepStarted.Model) to a (pricingProvider, catalog id) pair. It is
// supplied by the caller because the mapping is deployment-specific: the same
// gateway model name may need to resolve to a different pricing provider than
// the one used for limits (e.g. limits from "github-copilot", pricing from
// "anthropic").
//
// When the resolved provider is "ollama", CatalogPricer.Cost auto-converts it
// to "ollama-cloud" before the catalog lookup. If the model is not in the
// ollama-cloud bucket (local-only model), Cost returns 0 — local ollama has
// no price.
//
// ok is false when gatewayModel cannot be resolved; CatalogPricer.Cost then
// returns 0.
type NameResolver func(gatewayModel string) (provider, id string, ok bool)

// CatalogPricer implements a Cost(model, tokens) hook (e.g. for
// callbacks/activity.WithPricer) backed by a Catalog and a caller-supplied
// NameResolver.
type CatalogPricer struct {
	Catalog *Catalog
	Resolve NameResolver
}

// CostBreakdown is where one step's money went. Total equals today's Cost().
// Savings is the counterfactual USD a cache hit avoided (NOT subtracted from
// Total) — informational only, for "prompt caching saved you $X" metrics.
type CostBreakdown struct {
	Input      float64
	Output     float64
	CacheRead  float64
	CacheWrite float64
	Total      float64
	Savings    float64
}

// ContextUsage reports how full the context window is.
type ContextUsage struct {
	Used   int
	Window int
}

// Remaining returns the number of unused context-window slots (never negative).
func (u ContextUsage) Remaining() int {
	if u.Window > u.Used {
		return u.Window - u.Used
	}
	return 0
}

// Fraction returns Used/Window (0..1), or 0 when Window <= 0.
func (u ContextUsage) Fraction() float64 {
	if u.Window <= 0 {
		return 0
	}
	return float64(u.Used) / float64(u.Window)
}

// NearLimit reports whether the context window is at least threshold full.
func (u ContextUsage) NearLimit(threshold float64) bool {
	return u.Fraction() >= threshold
}

// Breakdown computes the per-component cost breakdown for gatewayModel and t,
// plus the cache-hit savings delta. ok mirrors today's resolve/lookup/cost-nil
// failure modes (breakdown all-zero on ok=false).
func (p CatalogPricer) Breakdown(gatewayModel string, t activity.Tokens) (CostBreakdown, bool) {
	if p.Catalog == nil || p.Resolve == nil {
		return CostBreakdown{}, false
	}
	provider, id, ok := p.Resolve(gatewayModel)
	if !ok {
		return CostBreakdown{}, false
	}
	if provider == OllamaProvider {
		provider = OllamaCloudProvider
	}
	m, ok := p.Catalog.Model(provider, id)
	if !ok || m.Cost == nil {
		return CostBreakdown{}, false
	}
	return costBreakdownOf(*m.Cost, t), true
}

// Cost computes the USD cost of t for gatewayModel, resolving it via Resolve
// and pricing via the Catalog. It returns 0 when the model cannot be resolved,
// is not present in the catalog, or has no cost entry (e.g. the github-copilot
// bucket, which prices at {0,0}).
//
// Cost delegates to Breakdown (Total field); behavior is unchanged from before
// Breakdown existed.
//
// Auto-conversion: when the resolved provider is "ollama", it is converted to
// "ollama-cloud" before the catalog lookup. Local ollama models not present in
// the ollama-cloud bucket price at 0.
func (p CatalogPricer) Cost(gatewayModel string, t activity.Tokens) float64 {
	b, ok := p.Breakdown(gatewayModel, t)
	if !ok {
		return 0
	}
	return b.Total
}

// costBreakdownOf computes the full CostBreakdown from per-million rates and
// token counts. Savings = Cache.Read/1e6 * max(0, cost.Input - cost.CacheRead).
func costBreakdownOf(cost Cost, t activity.Tokens) CostBreakdown {
	const perMillion = 1e6
	in := float64(t.Input) / perMillion * cost.Input
	out := float64(t.Output) / perMillion * cost.Output
	cr := float64(t.Cache.Read) / perMillion * cost.CacheRead
	cw := float64(t.Cache.Write) / perMillion * cost.CacheWrite
	savings := float64(t.Cache.Read) / perMillion * max(0, cost.Input-cost.CacheRead)
	return CostBreakdown{
		Input:      in,
		Output:     out,
		CacheRead:  cr,
		CacheWrite: cw,
		Total:      in + out + cr + cw,
		Savings:    savings,
	}
}
