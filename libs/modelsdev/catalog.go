// Package modelsdev provides a provider-generic models.dev catalog: per-model
// context/output limits and USD-per-million-token cost, mirroring kilocode's
// core/models-dev.
//
// The catalog is embedded at build time from a committed api.json snapshot
// (see api.json and Makefile/go:embed below) so lookups work offline and
// reproducibly. Load additionally attempts a bounded network refresh so
// long-running processes pick up new models without a redeploy, falling back
// to the embedded snapshot on any error or timeout.
//
// This package is intentionally policy-free: it never guesses a model id, and
// an unknown (provider, id) pair simply reports ok=false so callers can decide
// how to react (e.g. WARN and price at 0).
package modelsdev

import (
	"context"
	_ "embed"
	"net/http"
	"time"

	"emperror.dev/errors"
	"github.com/goccy/go-json"
)

// api.json is the committed models.dev snapshot, refreshed via
// `go generate ./libs/modelsdev` (see gen.go) or `make models-dev-refresh`.
//
//go:embed api.json
var embeddedSnapshot []byte

// DefaultURL is the default models.dev base URL, overridable via LoadOptions.URL
// (or the EINO_MODELS_URL environment variable, which callers may read
// themselves and pass through).
const DefaultURL = "https://models.dev"

// DefaultTimeout bounds the network refresh attempt in Load so app readiness
// probes are never blocked for long.
const DefaultTimeout = 5 * time.Second

// Limit is the context/output token window for a model, as reported by
// models.dev. Input, when present, is a tighter input-budget than Context
// (e.g. reserving room for output); see Catalog.Limits for the resolution
// rule.
type Limit struct {
	Context int `json:"context"`
	Input   int `json:"input,omitempty"`
	Output  int `json:"output"`
}

// Cost is the USD price per **million** tokens for a model, as reported by
// models.dev. Zero-valued fields mean "not applicable"; a nil *Cost on Model
// means pricing is entirely unknown for that model.
type Cost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cache_read,omitempty"`
	CacheWrite float64 `json:"cache_write,omitempty"`
}

// Model is a single models.dev model entry, trimmed to the fields this package
// resolves (limits and cost). Unknown/extra JSON fields are ignored.
type Model struct {
	ID    string `json:"id"`
	Name  string `json:"name,omitempty"`
	Limit Limit  `json:"limit"`
	Cost  *Cost  `json:"cost,omitempty"`
}

// Provider is a models.dev provider bucket (e.g. "github-copilot",
// "anthropic"), keyed by model id in Models.
type Provider struct {
	ID     string           `json:"id"`
	Name   string           `json:"name,omitempty"`
	Models map[string]Model `json:"models"`
}

// Catalog is an in-memory, immutable-after-Load snapshot of the models.dev
// provider/model table. The zero value is an empty catalog (all lookups
// report ok=false); use Load to populate one.
type Catalog struct {
	providers map[string]Provider
	// Fresh reports whether this Catalog was populated from a live network
	// fetch (true) or the embedded fallback snapshot (false).
	Fresh bool
	// LoadErr holds the last network-fetch error when the live refresh failed
	// and Load fell back to the embedded snapshot (Fresh == false). It is nil
	// when the fetch succeeded (Fresh == true) or when no network attempt was
	// made. Callers may log it to surface why the catalog is not fresh.
	LoadErr error
}

// LoadOptions configures Load. The zero value uses the package defaults.
type LoadOptions struct {
	// URL is the models.dev base URL; the catalog is fetched from
	// "<URL>/api.json". Defaults to DefaultURL.
	URL string
	// Timeout bounds the network attempt (including retries). Defaults to
	// DefaultTimeout.
	Timeout time.Duration
	// Retries is the number of additional attempts after the first failure.
	// Defaults to 1 (i.e. up to 2 attempts total).
	Retries int
	// HTTPClient overrides the client used for the network fetch. Defaults to
	// a client scoped to Timeout.
	HTTPClient *http.Client
}

// Load builds a Catalog, preferring a live network fetch of
// "<URL>/api.json" and falling back to the embedded snapshot on any error or
// timeout. It never returns an error: worst case, Catalog.Fresh is false and
// lookups use the embedded data.
//
// Load is a blocking, network-bound call bounded by opts.Timeout (default
// DefaultTimeout). Callers should invoke it lazily (e.g. on first use, or in
// a background goroutine after the service has started) rather than from a
// synchronous service-startup path that gates a readiness probe, since a slow
// or unreachable models.dev endpoint would otherwise delay startup by up to
// the timeout.
func Load(ctx context.Context, opts LoadOptions) *Catalog {
	if opts.URL == "" {
		opts.URL = DefaultURL
	}
	if opts.Timeout <= 0 {
		opts.Timeout = DefaultTimeout
	}
	if opts.Retries < 0 {
		opts.Retries = 0
	}
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: opts.Timeout}
	}

	attempts := opts.Retries + 1
	// Divide the overall timeout budget across attempts so a retry after a
	// slow/timed-out attempt still gets a real (if shorter) time budget,
	// instead of inheriting an already-expired deadline.
	perAttempt := opts.Timeout / time.Duration(attempts)
	var lastErr error
	for i := 0; i < attempts; i++ {
		attemptCtx, cancel := context.WithTimeout(ctx, perAttempt)
		providers, err := fetch(attemptCtx, client, opts.URL)
		cancel()
		if err == nil {
			return &Catalog{providers: providers, Fresh: true}
		}
		lastErr = err
	}
	// network refresh is best-effort; the embedded snapshot is the fallback.
	// lastErr is surfaced via Catalog.LoadErr so callers can log the reason.

	providers, err := parse(embeddedSnapshot)
	if err != nil {
		// The embedded snapshot is committed and validated at build time; a
		// parse failure here indicates a packaging bug, not a runtime
		// condition callers can recover from. Return an empty catalog rather
		// than panicking so lookups degrade to ok=false.
		return &Catalog{LoadErr: lastErr}
	}
	return &Catalog{providers: providers, LoadErr: lastErr}
}

func fetch(ctx context.Context, client *http.Client, baseURL string) (map[string]Provider, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api.json", nil)
	if err != nil {
		return nil, errors.Wrap(err, "modelsdev: building request")
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "modelsdev: fetching catalog")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.Errorf("modelsdev: unexpected status %d", resp.StatusCode)
	}
	var providers map[string]Provider
	if err := json.NewDecoder(resp.Body).Decode(&providers); err != nil {
		return nil, errors.Wrap(err, "modelsdev: decoding catalog")
	}
	return providers, nil
}

func parse(b []byte) (map[string]Provider, error) {
	var providers map[string]Provider
	if err := json.Unmarshal(b, &providers); err != nil {
		return nil, errors.Wrap(err, "modelsdev: parsing embedded catalog")
	}
	return providers, nil
}

// Model looks up a single model by exact provider and model id. ok is false
// when the provider or model id is not present in the catalog.
func (c *Catalog) Model(provider, id string) (Model, bool) {
	if c == nil {
		return Model{}, false
	}
	p, ok := c.providers[provider]
	if !ok {
		return Model{}, false
	}
	m, ok := p.Models[id]
	return m, ok
}

// Limits resolves the effective context window and output cap for
// (provider, id). contextWindow is limit.input when present, else
// limit.context (input-budget semantics: input reflects a tighter usable
// window when the provider reserves room for output). ok is false when the
// model is not found in the catalog.
func (c *Catalog) Limits(provider, id string) (contextWindow, output int, ok bool) {
	m, ok := c.Model(provider, id)
	if !ok {
		return 0, 0, false
	}
	contextWindow = m.Limit.Input
	if contextWindow <= 0 {
		contextWindow = m.Limit.Context
	}
	return contextWindow, m.Limit.Output, true
}
