package modelsdev_test

import (
	"testing"

	"github.com/webcenter-fr/eino-ext/components/model/chatmodel"
	"github.com/webcenter-fr/eino-ext/libs/modelsdev"
)

// TestCapOutputTokensInterplay documents how a catalog-derived output limit
// composes with chatmodel.CapOutputTokens: the catalog supplies
// modelOutputLimit, the caller supplies its own ceiling (or 0 for the package
// default), and the smaller of the two wins.
func TestCapOutputTokensInterplay(t *testing.T) {
	m := modelsdev.Model{Limit: modelsdev.Limit{Context: 200000, Output: 8192}}

	// Catalog output limit (8192) is tighter than the default ceiling
	// (chatmodel.OutputTokenMax = 32000): the catalog limit wins.
	if got := chatmodel.CapOutputTokens(m.Limit.Output, 0); got != 8192 {
		t.Errorf("CapOutputTokens = %d, want 8192", got)
	}

	// Caller ceiling (4096) is tighter than the catalog limit: the ceiling
	// wins.
	if got := chatmodel.CapOutputTokens(m.Limit.Output, 4096); got != 4096 {
		t.Errorf("CapOutputTokens = %d, want 4096", got)
	}

	// Unknown model (catalog ok=false, Limit zero value): falls back to the
	// caller's ceiling (or the package default).
	unknown := modelsdev.Model{}
	if got := chatmodel.CapOutputTokens(unknown.Limit.Output, 0); got != chatmodel.OutputTokenMax {
		t.Errorf("CapOutputTokens = %d, want %d", got, chatmodel.OutputTokenMax)
	}
}
