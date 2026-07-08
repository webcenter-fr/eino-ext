package modelsdev

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func testCatalog() *Catalog {
	return &Catalog{
		providers: map[string]Provider{
			"anthropic": {
				ID: "anthropic",
				Models: map[string]Model{
					"claude-opus-4-5": {
						ID:    "claude-opus-4-5",
						Limit: Limit{Context: 200000, Output: 64000},
						Cost:  &Cost{Input: 5, Output: 25, CacheRead: 0.5, CacheWrite: 6.25},
					},
				},
			},
			"github-copilot": {
				ID: "github-copilot",
				Models: map[string]Model{
					"claude-opus-4.5": {
						ID:    "claude-opus-4.5",
						Limit: Limit{Context: 200000, Input: 168000, Output: 32000},
						Cost:  &Cost{Input: 0, Output: 0},
					},
				},
			},
			"ollama-cloud": {
				ID: "ollama-cloud",
				Models: map[string]Model{
					"deepseek-v4-flash": {
						ID:    "deepseek-v4-flash",
						Limit: Limit{Context: 1048576, Output: 1048576},
						Cost:  &Cost{Input: 0.89, Output: 1.79},
					},
				},
			},
		},
	}
}

func TestCatalog_Model(t *testing.T) {
	c := testCatalog()

	m, ok := c.Model("anthropic", "claude-opus-4-5")
	if !ok {
		t.Fatal("expected model to be found")
	}
	if m.Limit.Context != 200000 {
		t.Errorf("Context = %d, want 200000", m.Limit.Context)
	}

	if _, ok := c.Model("anthropic", "does-not-exist"); ok {
		t.Error("expected unknown model id to report ok=false")
	}
	if _, ok := c.Model("does-not-exist", "claude-opus-4-5"); ok {
		t.Error("expected unknown provider to report ok=false")
	}
}

func TestCatalog_Limits(t *testing.T) {
	c := testCatalog()

	// anthropic bucket has no Input override: contextWindow falls back to
	// Context.
	ctxWindow, output, ok := c.Limits("anthropic", "claude-opus-4-5")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if ctxWindow != 200000 || output != 64000 {
		t.Errorf("Limits = (%d, %d), want (200000, 64000)", ctxWindow, output)
	}

	// github-copilot bucket has a tighter Input budget: contextWindow prefers
	// Input over Context.
	ctxWindow, output, ok = c.Limits("github-copilot", "claude-opus-4.5")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if ctxWindow != 168000 || output != 32000 {
		t.Errorf("Limits = (%d, %d), want (168000, 32000)", ctxWindow, output)
	}

	if _, _, ok := c.Limits("anthropic", "does-not-exist"); ok {
		t.Error("expected unknown model to report ok=false")
	}
}

func TestCatalog_Limits_NilCatalog(t *testing.T) {
	var c *Catalog
	if _, ok := c.Model("anthropic", "claude-opus-4-5"); ok {
		t.Error("expected nil catalog to report ok=false")
	}
}

func TestLoad_EmbeddedFallback(t *testing.T) {
	// An unreachable URL forces the network fetch to fail; Load must fall back
	// to the embedded snapshot without error and without blocking for long.
	c := Load(context.Background(), LoadOptions{
		URL:     "http://127.0.0.1:1", // nothing listens here
		Timeout: 500 * time.Millisecond,
	})
	if c == nil {
		t.Fatal("Load returned nil")
	}
	if c.Fresh {
		t.Error("expected Fresh=false when network fetch fails")
	}
	if len(c.providers) == 0 {
		t.Error("expected embedded snapshot to populate providers")
	}
}

func TestLoad_NetworkFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"acme":{"id":"acme","models":{"m1":{"id":"m1","limit":{"context":1000,"output":100},"cost":{"input":1,"output":2}}}}}`))
	}))
	defer srv.Close()

	c := Load(context.Background(), LoadOptions{URL: srv.URL, Timeout: 2 * time.Second})
	if !c.Fresh {
		t.Error("expected Fresh=true when network fetch succeeds")
	}
	m, ok := c.Model("acme", "m1")
	if !ok {
		t.Fatal("expected fetched model to be found")
	}
	if m.Limit.Context != 1000 {
		t.Errorf("Context = %d, want 1000", m.Limit.Context)
	}
}

func TestEmbeddedSnapshot_Parses(t *testing.T) {
	providers, err := parse(embeddedSnapshot)
	if err != nil {
		t.Fatalf("parse(embeddedSnapshot): %v", err)
	}
	if len(providers) == 0 {
		t.Fatal("expected embedded snapshot to contain providers")
	}
}
