package modelsdev

import (
	"testing"

	"github.com/webcenter-fr/eino-ext/callbacks/activity"
)

func TestCostOf(t *testing.T) {
	cost := Cost{Input: 5, Output: 25, CacheRead: 0.5, CacheWrite: 6.25}
	tokens := activity.Tokens{
		Input:  1_000_000,
		Output: 2_000_000,
		Cache:  activity.CacheTokens{Read: 500_000, Write: 100_000},
	}
	// 1e6/1e6*5 + 2e6/1e6*25 + 0.5e6/1e6*0.5 + 0.1e6/1e6*6.25
	// = 5 + 50 + 0.25 + 0.625 = 55.875
	got := costOf(cost, tokens)
	want := 55.875
	if got != want {
		t.Errorf("costOf = %v, want %v", got, want)
	}
}

func TestCostOf_ReasoningNotDoubleCounted(t *testing.T) {
	cost := Cost{Input: 1, Output: 1}
	// Reasoning tokens are a subset of Output; costOf must not read Reasoning
	// at all, so a large Reasoning value with Output=0 must price at 0 for the
	// output component.
	tokens := activity.Tokens{Output: 0, Reasoning: 1_000_000}
	if got := costOf(cost, tokens); got != 0 {
		t.Errorf("costOf = %v, want 0 (reasoning must not be priced separately)", got)
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
