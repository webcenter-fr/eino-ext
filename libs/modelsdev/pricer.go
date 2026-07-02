package modelsdev

import "github.com/webcenter-fr/eino-ext/callbacks/activity"

// NameResolver maps a per-step gateway model name (as carried on
// activity.StepStarted.Model) to a (pricingProvider, catalog id) pair. It is
// supplied by the caller because the mapping is deployment-specific: the same
// gateway model name may need to resolve to a different pricing provider than
// the one used for limits (e.g. limits from "github-copilot", pricing from
// "anthropic").
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

// Cost computes the USD cost of t for gatewayModel, resolving it via Resolve
// and pricing via the Catalog. It returns 0 when the model cannot be resolved,
// is not present in the catalog, or has no cost entry (e.g. the github-copilot
// bucket, which prices at {0,0}).
func (p CatalogPricer) Cost(gatewayModel string, t activity.Tokens) float64 {
	if p.Catalog == nil || p.Resolve == nil {
		return 0
	}
	provider, id, ok := p.Resolve(gatewayModel)
	if !ok {
		return 0
	}
	m, ok := p.Catalog.Model(provider, id)
	if !ok || m.Cost == nil {
		return 0
	}
	return costOf(*m.Cost, t)
}

// costOf sums per-million-token pricing over input, output, and cache
// read/write. Reasoning tokens are a subset of Output (CompletionTokens) per
// eino/models.dev semantics and must NOT be priced separately.
func costOf(cost Cost, t activity.Tokens) float64 {
	const perMillion = 1e6
	return float64(t.Input)/perMillion*cost.Input +
		float64(t.Output)/perMillion*cost.Output +
		float64(t.Cache.Read)/perMillion*cost.CacheRead +
		float64(t.Cache.Write)/perMillion*cost.CacheWrite
}
