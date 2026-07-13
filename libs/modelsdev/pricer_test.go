package modelsdev

import (
	"testing"

	"github.com/webcenter-fr/eino-ext/callbacks/activity"
)

func TestCostBreakdownOf(t *testing.T) {
	cost := Cost{Input: 5, Output: 25, CacheRead: 0.5, CacheWrite: 6.25}
	tokens := activity.Tokens{
		Input:  1_000_000,
		Output: 2_000_000,
		Cache:  activity.CacheTokens{Read: 500_000, Write: 100_000},
	}
	b := costBreakdownOf(cost, tokens)
	if got, want := b.Input, 5.0; got != want {
		t.Errorf("Input = %v, want %v", got, want)
	}
	if got, want := b.Output, 50.0; got != want {
		t.Errorf("Output = %v, want %v", got, want)
	}
	if got, want := b.CacheRead, 0.25; got != want {
		t.Errorf("CacheRead = %v, want %v", got, want)
	}
	if got, want := b.CacheWrite, 0.625; got != want {
		t.Errorf("CacheWrite = %v, want %v", got, want)
	}
	// 5 + 50 + 0.25 + 0.625 = 55.875
	if got, want := b.Total, 55.875; got != want {
		t.Errorf("Total = %v, want %v", got, want)
	}
	// Savings = 0.5 * (5 - 0.5) = 2.25
	if got, want := b.Savings, 2.25; got != want {
		t.Errorf("Savings = %v, want %v", got, want)
	}
}

func TestCostBreakdownOf_ReasoningNotDoubleCounted(t *testing.T) {
	cost := Cost{Input: 1, Output: 1}
	// Reasoning tokens are a subset of Output; costBreakdownOf must not read
	// Reasoning at all, so a large Reasoning value with Output=0 must price at 0.
	tokens := activity.Tokens{Output: 0, Reasoning: 1_000_000}
	if got := costBreakdownOf(cost, tokens).Output; got != 0 {
		t.Errorf("Output = %v, want 0 (reasoning must not be priced separately)", got)
	}
}

func TestCatalogPricer_Cost(t *testing.T) {
	c := testCatalog()
	pricer := CatalogPricer{
		Catalog: c,
		Resolve: func(gatewayModel string) (provider, id string, ok bool) {
			if gatewayModel == "gw-opus" {
				return "anthropic", "claude-opus-4-5", true
			}
			return "", "", false
		},
	}

	got := pricer.Cost("gw-opus", activity.Tokens{Input: 1_000_000, Output: 1_000_000})
	want := 5.0 + 25.0
	if got != want {
		t.Errorf("Cost = %v, want %v", got, want)
	}
}

func TestCatalogPricer_Cost_UnknownModel(t *testing.T) {
	c := testCatalog()
	pricer := CatalogPricer{
		Catalog: c,
		Resolve: func(gatewayModel string) (provider, id string, ok bool) { return "", "", false },
	}
	if got := pricer.Cost("unknown", activity.Tokens{Input: 1_000_000}); got != 0 {
		t.Errorf("Cost = %v, want 0 for unresolvable model", got)
	}
}

func TestCatalogPricer_Cost_CopilotBucketZero(t *testing.T) {
	c := testCatalog()
	pricer := CatalogPricer{
		Catalog: c,
		Resolve: func(gatewayModel string) (provider, id string, ok bool) {
			return "github-copilot", "claude-opus-4.5", true
		},
	}
	if got := pricer.Cost("gw-opus", activity.Tokens{Input: 1_000_000, Output: 1_000_000}); got != 0 {
		t.Errorf("Cost = %v, want 0 for copilot {0,0} cost bucket", got)
	}
}

func TestCatalogPricer_Cost_NilFields(t *testing.T) {
	var pricer CatalogPricer
	if got := pricer.Cost("anything", activity.Tokens{Input: 1}); got != 0 {
		t.Errorf("Cost = %v, want 0 for zero-value CatalogPricer", got)
	}
}

func TestCatalogPricer_Cost_OllamaToCloud(t *testing.T) {
	c := testCatalog()
	pricer := CatalogPricer{
		Catalog: c,
		Resolve: func(gatewayModel string) (provider, id string, ok bool) {
			return "ollama", "deepseek-v4-flash", true
		},
	}
	got := pricer.Cost("ollama-deepseek", activity.Tokens{Input: 1_000_000, Output: 1_000_000})
	want := 0.89 + 1.79
	if got != want {
		t.Errorf("Cost = %v, want %v (ollama→ollama-cloud conversion)", got, want)
	}
}

func TestCatalogPricer_Cost_OllamaLocalNoPrice(t *testing.T) {
	c := testCatalog()
	pricer := CatalogPricer{
		Catalog: c,
		Resolve: func(gatewayModel string) (provider, id string, ok bool) {
			return "ollama", "llama3.2:3b", true
		},
	}
	if got := pricer.Cost("ollama-llama", activity.Tokens{Input: 1_000_000, Output: 500_000}); got != 0 {
		t.Errorf("Cost = %v, want 0 (local ollama model not in ollama-cloud)", got)
	}
}

func TestCatalogPricer_Breakdown(t *testing.T) {
	c := testCatalog()
	pricer := CatalogPricer{
		Catalog: c,
		Resolve: func(gatewayModel string) (provider, id string, ok bool) {
			if gatewayModel == "gw-opus" {
				return "anthropic", "claude-opus-4-5", true
			}
			return "", "", false
		},
	}
	tokens := activity.Tokens{
		Input:  2_000_000,
		Output: 1_000_000,
		Cache:  activity.CacheTokens{Read: 500_000, Write: 200_000},
	}
	b, ok := pricer.Breakdown("gw-opus", tokens)
	if !ok {
		t.Fatal("Breakdown ok = false, want true")
	}
	if got, want := b.Input, 10.0; got != want {
		t.Errorf("Input = %v, want %v", got, want)
	}
	if got, want := b.Output, 25.0; got != want {
		t.Errorf("Output = %v, want %v", got, want)
	}
	if got, want := b.CacheRead, 0.25; got != want {
		t.Errorf("CacheRead = %v, want %v", got, want)
	}
	if got, want := b.CacheWrite, 1.25; got != want {
		t.Errorf("CacheWrite = %v, want %v", got, want)
	}
	if got, want := b.Total, 10.0+25.0+0.25+1.25; got != want {
		t.Errorf("Total = %v, want %v", got, want)
	}
	// Savings = Cache.Read/1e6 * max(0, cost.Input - cost.CacheRead)
	//         = 0.5 * (5 - 0.5) = 2.25
	if got, want := b.Savings, 2.25; got != want {
		t.Errorf("Savings = %v, want %v", got, want)
	}
}

func TestCatalogPricer_Breakdown_SavingsZeroWhenNoCacheRead(t *testing.T) {
	c := testCatalog()
	pricer := CatalogPricer{
		Catalog: c,
		Resolve: func(gatewayModel string) (provider, id string, ok bool) {
			return "anthropic", "claude-opus-4-5", true
		},
	}
	b, ok := pricer.Breakdown("gw-opus", activity.Tokens{Input: 1_000_000})
	if !ok {
		t.Fatal("Breakdown ok = false")
	}
	if b.Savings != 0 {
		t.Errorf("Savings = %v, want 0 (no cache reads)", b.Savings)
	}
}

func TestCatalogPricer_Breakdown_SavingsClamped(t *testing.T) {
	// When CacheRead cost >= Input cost, savings should be 0 (clamped).
	c := &Catalog{
		providers: map[string]Provider{
			"test": {
				ID: "test",
				Models: map[string]Model{
					"cheap-cache": {
						ID:   "cheap-cache",
						Cost: &Cost{Input: 0.5, CacheRead: 2.0},
					},
				},
			},
		},
	}
	pricer := CatalogPricer{
		Catalog: c,
		Resolve: func(gw string) (string, string, bool) { return "test", "cheap-cache", true },
	}
	b, ok := pricer.Breakdown("gw", activity.Tokens{Input: 1_000_000, Cache: activity.CacheTokens{Read: 500_000}})
	if !ok {
		t.Fatal("Breakdown ok = false")
	}
	if b.Savings != 0 {
		t.Errorf("Savings = %v, want 0 (cache_read >= input, clamped)", b.Savings)
	}
}

func TestCatalogPricer_Breakdown_UnknownModel(t *testing.T) {
	c := testCatalog()
	pricer := CatalogPricer{
		Catalog: c,
		Resolve: func(gw string) (string, string, bool) { return "", "", false },
	}
	b, ok := pricer.Breakdown("unknown", activity.Tokens{Input: 1_000_000})
	if ok {
		t.Error("Breakdown ok = true for unknown model, want false")
	}
	if b.Total != 0 {
		t.Errorf("Total = %v, want 0", b.Total)
	}
	if b.Savings != 0 {
		t.Errorf("Savings = %v, want 0", b.Savings)
	}
}

func TestCatalogPricer_BackwardCompatible(t *testing.T) {
	c := testCatalog()
	tests := []struct {
		name   string
		resolve func(string) (string, string, bool)
		tokens activity.Tokens
	}{
		{"known model", func(gw string) (string, string, bool) { return "anthropic", "claude-opus-4-5", true }, activity.Tokens{Input: 1_000_000, Output: 1_000_000, Cache: activity.CacheTokens{Read: 500_000, Write: 100_000}}},
		{"copilot zero", func(gw string) (string, string, bool) { return "github-copilot", "claude-opus-4.5", true }, activity.Tokens{Input: 1_000_000}},
		{"unknown resolver", func(gw string) (string, string, bool) { return "", "", false }, activity.Tokens{Input: 1_000_000}},
		{"no cache", func(gw string) (string, string, bool) { return "anthropic", "claude-opus-4-5", true }, activity.Tokens{Input: 1_000_000, Output: 500_000}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pricer := CatalogPricer{Catalog: c, Resolve: func(gw string) (string, string, bool) { return tt.resolve(gw) }}
			cost := pricer.Cost("gw", tt.tokens)
			b, _ := pricer.Breakdown("gw", tt.tokens)
			if cost != b.Total {
				t.Errorf("Cost = %v, Breakdown.Total = %v — must be equal (BC)", cost, b.Total)
			}
		})
	}
}
