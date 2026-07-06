# Follow-up plan — Reimplement the OpenSearch retriever with hybrid vector+BM25 search (RRF)

> Follow-up to the already-executed twin plans `1783324040988-eino-ext-rag-components.md` (lib, Plan A)
> and `1783324040988-project-rag-components-adoption.md` (project, Plan B). Those two plans are DONE for
> every lot **except** B6 (retriever) and B7 (loader/parser), which this plan supersedes for the retriever
> part. B7 (`components/document/loader/opensearch` + `components/document/parser/opensearch`) is fine as
> published and is NOT touched by this plan (BM25-only text extraction from OpenSearch source documents is
> exactly what the inventory *loader* needs — it does not do semantic search, it just reads raw source
> records to re-embed them).

## 0. Why this plan exists

### What Plan A/B assumed vs. what was actually published

Plan A/B assumed the lib's new `components/retriever/opensearch` package would be a straight extraction of
the project's `pkg/retriever/retriever.go`, i.e. a thin wrapper around
`github.com/cloudwego/eino-ext/components/retriever/opensearch3` keeping:
- `Embedding embedding.Embedder` (to vectorize the query),
- `SearchMode` = `search_mode.Approximate(...)` (kNN + hybrid BM25/vector `bool.should` query),
- `SearchPipeline` (an OpenSearch **search pipeline** name, e.g. `"rrf"`, applied via the request body /
  `search_pipeline` param to combine/normalize the BM25 and vector sub-scores after the fact).

**What was actually published** (`github.com/webcenter-fr/eino-ext@v0.0.0-20260706151438-429f1e65bb86`,
`components/retriever/opensearch/retriever.go`) is a **plain BM25 `query_string` retriever**:
- `Config` has no `Embedding`, no `VectorField`, no `SearchMode`/kNN support at all.
- `Retrieve()` always builds `{"query": {"query_string": {"query": query, ...}}}` — pure lexical search.
- `SearchPipeline` is still there as a field, but with only a lexical query in the body, an RRF/normalization
  pipeline has nothing to combine — it's a no-op at best.

This is a **functional regression**, not a refactor: `pkg/tools/doc/documentation_retriever.go` and
`pkg/tools/inventory/documentation_retriever.go` both rely on semantic (embedding/kNN) search combined with
BM25 via OpenSearch's hybrid search + RRF (Reciprocal Rank Fusion) post-processing — this is the whole
point of the vector index built by `pkg/indexer/opensearch.go` (`FieldContentVector = "vector"`,
`EmbDimension = 1536`, HNSW/FAISS/inner-product kNN mapping). Wiring the tools to the published retriever
as-is would silently turn every RAG lookup into keyword-only search, discarding the embeddings entirely.

### Current broken state in this repo

`pkg/retriever/opensearch.go` was already edited (uncommitted) to call
`osretriever.NewRetriever(ctx, &osretriever.RetrieverConfig{RetrieverConfig: opensearch3.RetrieverConfig{...}, SearchPipeline: "rrf"})`,
but the published lib package has no `RetrieverConfig` type (only `Config`) and no such embedded field —
**this does not compile**. `pkg/retriever/retriever.go` (the old working implementation) was already
deleted from the working tree. This plan restores a working hybrid retriever, implemented properly this
time in the lib (so the extraction goal is still met), instead of reverting to project-local code.

### Decision: extend the lib package, don't fork it back into the project

Enhance `github.com/webcenter-fr/eino-ext` → `components/retriever/opensearch` in place to support an
optional embedding-backed hybrid mode, keeping the existing pure-BM25 behavior as the default (so any other
consumer of that package outside this project keeps working unchanged). This preserves the "shared lib"
goal instead of re-forking retriever code back into the project.

---

## Part A — lib repo (`github.com/webcenter-fr/eino-ext`)

Clone/open the lib repo in the other IDE (`git clone https://github.com/webcenter-fr/eino-ext`), branch
`feat/retriever-hybrid-rrf`.

### A1. Extend `components/retriever/opensearch/retriever.go`

Add to `Config` (all new fields optional/backward compatible — zero value = current pure-BM25 behavior):

```go
// Embedding, when set, enables kNN vector search. If Hybrid is also true,
// the vector search is combined with a BM25 match on ContentField via a
// bool "should" query (the OpenSearch search pipeline, if configured, then
// combines/normalizes the two sub-scores).
Embedding embedding.Embedder

// VectorField is the knn_vector field to search. Required when Embedding is set.
VectorField string

// ContentField is the text field used for the BM25 side of a hybrid query
// (also the field defaultResultParser reads as Content). Defaults to "content".
ContentField string

// Hybrid combines the kNN query with a BM25 match on ContentField (requires
// Embedding and VectorField). If false and Embedding is set, pure kNN only.
Hybrid bool

// K is the number of nearest neighbors requested from the kNN query.
// Defaults to TopK (see Retrieve) when zero.
K int
```

Import `"github.com/cloudwego/eino/components/embedding"` (already an indirect dep of the module via other
packages; add as direct if `go mod tidy` flags it).

In `Retrieve()`, branch on `config.Embedding`:
- `Embedding == nil` → **unchanged current behavior** (`query_string` body), so existing/other consumers of
  this package are unaffected.
- `Embedding != nil`:
  1. `vectors, err := config.Embedding.EmbedStrings(ctx, []string{query})`; error → wrap and return.
  2. Build `knnQuery := {"knn": {config.VectorField: {"vector": vectors[0], "k": k}}}` where
     `k := config.K; if k == 0 { k = topK }`.
  3. If `config.Hybrid`: `finalQuery := {"bool": {"should": [knnQuery, {"match": {config.ContentField: query}}]}}`.
     Else: `finalQuery := knnQuery`.
  4. `body := {"query": finalQuery, "size": topK}`, plus `search_pipeline` in body as today when
     `config.SearchPipeline != ""`.
- Keep `defaultResultParser` as-is (reads `"content"`/`ContentField`... — see A1a below) and the existing
  `_id`/`_index`/`_score`/`_version` flattening in `searchHitToMap` (top-level keys merged with the source
  fields, **not** nested under `_source`) — this is the shape the project's custom `ResultParser` must be
  adapted to (see Part B).

**A1a.** `defaultResultParser` currently hardcodes reading `hit["content"]`. Make it read
`config.ContentField` (default `"content"`) instead of the literal string, by turning it into a method/
closure bound to the retriever's configured `ContentField` (or keep the package-level default for the
zero-Embedding case, and only apply `ContentField` when the caller didn't supply a custom `ResultParser` —
simplest: keep `defaultResultParser` reading `"content"` for backward compat, since `ContentField` is a new,
opt-in field only meaningful when `Embedding` is set, and this project always supplies a custom
`ResultParser` anyway).

### A2. New file `components/retriever/opensearch/pipeline.go` — idempotent RRF pipeline provisioning

```go
package opensearch

import (
	"context"
	"encoding/json"

	"emperror.dev/errors"
	opensearchv4 "github.com/disaster37/opensearch/v4"
)

// DefaultRRFPipelineBody is the phase-results-processor pipeline definition
// used by EnsureRRFPipeline: reciprocal rank fusion combining a BM25 and a
// kNN sub-query's rankings. Requires an OpenSearch version/plugin exposing
// the "score-ranker-processor" phase results processor (unified hybrid
// query framework). Older clusters (normalization-processor only, or no
// hybrid/neural-search plugin) will reject this with a 400 — EnsureRRFPipeline
// treats that as non-fatal (see below).
const DefaultRRFPipelineBody = `{
  "description": "Post processor for hybrid RRF search",
  "phase_results_processors": [
    {
      "score-ranker-processor": {
        "combination": {
          "technique": "rrf"
        }
      }
    }
  ]
}`

// EnsureRRFPipeline creates the named search pipeline with DefaultRRFPipelineBody
// ONLY if it does not already exist (an existing pipeline, e.g. hand-tuned by
// ops, is never overwritten). Returns (created bool, err error).
//
// A failure to create it (e.g. cluster/plugin does not support
// phase_results_processors / score-ranker-processor) is returned as an error
// so the caller can decide whether to treat it as fatal or just log+continue
// with hybrid search still working (unnormalized bool-should scores) minus
// pipeline-based fusion.
func EnsureRRFPipeline(ctx context.Context, client opensearchv4.Client, name string) (created bool, err error) {
	exists, err := searchPipelineExists(ctx, client, name)
	if err != nil {
		return false, errors.Wrap(err, "failed to check existing search pipeline")
	}
	if exists {
		return false, nil
	}

	var body map[string]any
	if err = json.Unmarshal([]byte(DefaultRRFPipelineBody), &body); err != nil {
		return false, errors.Wrap(err, "failed to parse default RRF pipeline body")
	}
	if err = putSearchPipeline(ctx, client, name, body); err != nil {
		return false, errors.Wrap(err, "failed to create RRF search pipeline")
	}
	return true, nil
}
```

Implement `searchPipelineExists` / `putSearchPipeline` using whatever low-level request the
`disaster37/opensearch/v4` client exposes for arbitrary endpoints (check `client.???` — the `v4` client used
elsewhere in this lib (`reconcile.go`) only exposes `Search()`/`Document()`; there is no typed
`_search/pipeline` helper). Two options, pick whichever the client supports; if neither is available,
implement via a raw HTTP request using `client`'s underlying transport/base URL+auth (check
`opensearchv4.Client` interface for a generic `Perform`/`Do` method, similar to the official
`opensearch-go` `Transport.Perform`). **Verify this against the actual `disaster37/opensearch/v4` v4.0.0-7
API before writing this file** — do not guess a nonexistent method.

Add a test in `components/retriever/opensearch/pipeline_test.go` that at least exercises
`json.Unmarshal([]byte(DefaultRRFPipelineBody), ...)` and, if a live/mocked OpenSearch is available in the
lib's test setup (check how `reconcile.go` / `mappings.go` are tested), an integration-style test for
create-if-missing / no-op-if-exists.

### A3. README + validation

Update `components/retriever/opensearch/README.md` with the new hybrid config fields and
`EnsureRRFPipeline` usage snippet. Then:
```
go build ./...
go test ./components/retriever/... ./components/indexer/...
go vet ./...
```
Commit, push branch, merge, note the new commit hash for Part B's `go get`.

---

## Part B — project repo (`rancher-doc-chat-api-k8s`)

### B1. Bump the lib

```
go get github.com/webcenter-fr/eino-ext@<new-commit-from-Part-A>
```

### B2. Rewrite `pkg/retriever/opensearch.go`

Replace the current (non-compiling) body with a call into the enhanced `Config`:

```go
package retriever

import (
	"context"
	"crypto/tls"
	"net/http"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/embedding"
	"github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/schema"
	"github.com/hm-it/rancher-doc-chat-api-k8s/pkg/indexer"
	log "github.com/sirupsen/logrus"
	osretriever "github.com/webcenter-fr/eino-ext/components/retriever/opensearch"
)

// RRFConfig controls the optional OpenSearch RRF search-pipeline combination
// of the BM25 and kNN vector sub-queries. Left as an explicit, separately
// constructed struct (rather than always-on) so a cluster/plugin that does
// not support "score-ranker-processor" (older OpenSearch, neural-search
// plugin not installed) can still run hybrid search without pipeline-based
// score fusion, by passing PipelineName: "".
type RRFConfig struct {
	// PipelineName is the OpenSearch search pipeline applied to combine BM25
	// + kNN sub-scores. Empty disables pipeline-based fusion (the bool-should
	// hybrid query still runs and returns results, just without normalized
	// fused scoring).
	PipelineName string
	// EnsurePipeline creates PipelineName (if non-empty) on the cluster when
	// missing, using osretriever.DefaultRRFPipelineBody. If PUT fails (e.g.
	// unsupported OpenSearch version), this is logged as a warning, NOT
	// returned as an error: retriever construction still succeeds and falls
	// back to un-fused hybrid scoring.
	EnsurePipeline bool
}

// NewOpensearchRetriever creates a new OpenSearch hybrid (BM25 + kNN vector)
// retriever, optionally combined via an OpenSearch RRF search pipeline.
func NewOpensearchRetriever(ctx context.Context, eb embedding.Embedder, url, username, password, index string, rrf RRFConfig) (rtr retriever.Retriever, err error) {
	// ... build the disaster37 opensearch/v4 client used by osretriever
	// (see osretriever.NewRetriever's expected client construction / check
	// what Config accepts as of Part A — URLs/Username/Password/TLSSkipVerify,
	// it builds its own client internally, it does NOT take a pre-built
	// client). Confirm exact Config shape from the merged Part A code before
	// writing this.

	if rrf.PipelineName != "" && rrf.EnsurePipeline {
		// EnsureRRFPipeline needs a client; either export one from
		// osretriever.NewRetriever's return value, or construct a short-lived
		// one here with the same URL/username/password purely to call
		// EnsureRRFPipeline. Confirm which against Part A's actual API.
		if _, ensureErr := osretriever.EnsureRRFPipeline(ctx, /* client */, rrf.PipelineName); ensureErr != nil {
			log.Warnf("Failed to ensure RRF search pipeline %q (hybrid search will run without pipeline-based score fusion): %v", rrf.PipelineName, ensureErr)
		}
	}

	return osretriever.NewRetriever(ctx, &osretriever.Config{
		URLs:           []string{url},
		Username:       username,
		Password:       password,
		TLSSkipVerify:  true,
		Embedding:      eb,
		VectorField:    indexer.FieldContentVector,
		ContentField:   indexer.FieldContent,
		Hybrid:         true,
		K:              10,
		SearchPipeline: rrf.PipelineName,
		ResultParser: func(ctx context.Context, hit map[string]any) (*schema.Document, error) {
			resp := &schema.Document{MetaData: map[string]any{}}
			if id, ok := hit["_id"].(string); ok {
				resp.ID = id
			}
			for field, val := range hit {
				switch field {
				case "_id", "_index", "_version":
					continue
				case "_score":
					if score, ok := val.(float64); ok {
						resp.WithScore(score)
					}
				case indexer.FieldContent:
					if content, ok := val.(string); ok {
						resp.Content = content
					}
				default:
					resp.MetaData[field] = val
				}
			}
			return resp, nil
		},
	})
}
```

**Important — hit map shape changed vs. the old local implementation.** The published lib's
`searchHitToMap` flattens `_id`/`_index`/`_score`/`_version` directly into the same map as the unmarshaled
`_source` fields (no nested `hit["_source"]`). The `ResultParser` above must read fields at the top level,
not via `hit["_source"].(map[string]interface{})` like the old code did. Double check this against Part A's
final `searchHitToMap` before finalizing — if Part A changed that shape, update accordingly.

Also double-check exact `TopK` plumbing: `osretriever.Config` today has no `TopK` field (topK is resolved
from `retriever.Options` at `Retrieve()` call time via `retriever.WithTopK(...)`, defaulting to
`defaultTopK = 10` — same as before). No change needed there; every caller already passes
`retriever.WithTopK(limit)`.

### B3. Wire `RRFConfig` end-to-end

Add to config (`internal/server/agent/chat.go`):
```go
type DocumentationAgentParameter struct {
	Url                string
	Username           string
	Password           string
	IndexDocumentation string
	IndexInventory     string
	RRFPipelineName    string
	EnsureRRFPipeline  bool
}
```
Update both call sites in `chat.go` (`doc.RetrieverConfig` / `inventory.RetrieverConfig` construction, or
plumb straight through to `NewRetrieverTool` → `NewOpensearchRetriever`) to pass
`retriever.RRFConfig{PipelineName: docConfig.RRFPipelineName, EnsurePipeline: docConfig.EnsureRRFPipeline}`.

`pkg/tools/doc/documentation_retriever.go` and `pkg/tools/inventory/documentation_retriever.go`: add
`RRF retriever.RRFConfig` (or the two flat fields) to their `RetrieverConfig` structs and forward into
`localRetriever.NewOpensearchRetriever(ctx, config.Embedder, config.Url, config.Username, config.Password, config.Index, config.RRF)`.

### B4. Config plumbing (`internal/server/server.go` + config file)

Read two new viper keys under `llm.tools.documentation` (shared cluster/creds with inventory, per current
code):
```go
docConfig := &agent.DocumentationAgentParameter{
	Url:                docCfg.GetString("url"),
	Username:           docCfg.GetString("username"),
	Password:           docCfg.GetString("password"),
	IndexDocumentation: docCfg.GetString("index"),
	IndexInventory:     inventoryCfg.GetString("index"),
	RRFPipelineName:    docCfg.GetString("rrfPipeline"),      // e.g. "rrf"; empty disables pipeline fusion
	EnsureRRFPipeline:  docCfg.GetBool("ensureRrfPipeline"),  // default true if unset — see viper.SetDefault below
}
```
Add sensible defaults (e.g. in the config bootstrap, `viper.SetDefault("llm.tools.documentation.rrfPipeline", "rrf")`
and `viper.SetDefault("llm.tools.documentation.ensureRrfPipeline", true)`) so existing deployments/config
files keep working with hybrid+RRF enabled by default, matching today's hardcoded `SearchPipeline: "rrf"`.
Document both keys in whatever config reference (README/sample config) already documents
`llm.tools.documentation.*`.

This directly answers "must RRF be an option?" — **yes**: `rrfPipeline: ""` in config fully disables
pipeline-based fusion (falls back to plain bool-should hybrid scoring, still returning both BM25 and vector
matches, just not rank-fused/normalized), independent of whether the lib's `EnsureRRFPipeline` support even
exists on the target cluster.

### B5. Fix/replace the pipeline setup scripts

`scripts/setup_rrf_pipeline.sh` currently creates a pipeline named **`rrf-search-pipeline`** using the old
`normalization-processor`/`arithmetic_mean` technique, while the retriever code (both before and after this
plan) references a pipeline named **`rrf`** — this naming mismatch predates this plan and should be fixed
regardless. Replace its body with the exact JSON the user specified:
```bash
curl -X PUT "${OPENSEARCH_URL}/_search/pipeline/rrf" \
  -u "${OPENSEARCH_USERNAME}:${OPENSEARCH_PASSWORD}" \
  -H 'Content-Type: application/json' \
  -d '{
  "description": "Post processor for hybrid RRF search",
  "phase_results_processors": [
    {
      "score-ranker-processor": {
        "combination": {
          "technique": "rrf"
        }
      }
    }
  ]
}'
```
(This must stay byte-for-byte in sync with `osretriever.DefaultRRFPipelineBody` from Part A — consider
having the script simply be documentation/manual-fallback now that `EnsureRRFPipeline` does this
automatically at startup when `ensureRrfPipeline: true`.) Update `scripts/test_rrf_query.sh`'s
`search_pipeline` query param from `rrf-search-pipeline` to `rrf` to match.

### B6. `go mod tidy`

After all of the above compiles, run `go mod tidy` at the project root.

---

## Validation

1. `go build ./...` and `go test ./...` green in both repos (lib first, then project after bumping).
2. Against a real/test OpenSearch cluster with the neural-search hybrid/`score-ranker-processor` plugin
   available:
   - Start the app with `ensureRrfPipeline: true`, `rrfPipeline: "rrf"`, pipeline absent beforehand → confirm
     it gets created (`GET _search/pipeline/rrf` returns it) and is **not** modified if it already exists
     (edit it manually, restart, confirm it's untouched).
   - Run a documentation query through the doc agent; confirm results include both lexically-relevant and
     only-semantically-relevant chunks (i.e. hybrid, not BM25-only) and that scores look RRF-fused (not raw
     cosine/BM25 magnitudes).
3. Against a cluster/version WITHOUT `score-ranker-processor` support: confirm `EnsureRRFPipeline`/PUT fails
   gracefully (warning logged, no startup crash) and hybrid bool-should search still returns relevant
   results (unfused scoring).
4. Set `rrfPipeline: ""` in config: confirm no `search_pipeline` is sent and hybrid search still runs
   (bool-should combination only).
5. Re-run the existing `pkg/tools/doc` / `pkg/tools/inventory` retrieval smoke path (whatever manual/CLI
   test was used for the original twin plans' B13 step) to confirm no regression versus the pre-migration
   `pkg/retriever/retriever.go` behavior.

## Open items for the implementing agent to confirm against actual code (do not guess)

- Exact method(s) `disaster37/opensearch/v4` `Client` (v4.0.0-7) exposes for pipeline CRUD / arbitrary raw
  requests — needed to implement `searchPipelineExists`/`putSearchPipeline` in A2.
- Whether `osretriever.NewRetriever` should expose its internally-built client (so `EnsureRRFPipeline` can
  reuse it) or whether Part B should build a second short-lived client just for the Ensure call — prefer
  exposing/reusing if the lib's `Retriever` struct can cheaply expose `Client() opensearchv4.Client`.
- Confirm final `searchHitToMap` field-flattening shape from Part A before finalizing B2's `ResultParser`.
