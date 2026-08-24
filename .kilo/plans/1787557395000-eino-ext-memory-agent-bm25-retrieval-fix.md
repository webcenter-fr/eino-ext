# Fix memory-agent retrieval: BM25 `operator: and` never matches (webcenter-fr/eino-ext)

## Target repository

`github.com/webcenter-fr/eino-ext`
Verified against the current pin `v0.0.0-20260821194924-4aa3fce50b5f` (the commit
that fixed the prompt-enhance bug). **This bug is still present in that commit.**

All file paths below are relative to the **eino-ext repo root**.

## Symptom

The long-term memory agent (`memoryAgent.enabled: true`) "never uses its memory":
the assistant does not answer quickly from previously-learned facts/preferences,
as if nothing had ever been stored.

## Root cause

Memory retrieval goes through the **pure-BM25 branch** of the shared OpenSearch
retriever, which hard-codes `operator: "and"`:

`components/retriever/opensearch/retriever.go:198-209`
```go
func (r *Retriever) buildSearchBody(query string, topK int, vectors [][]float64) map[string]any {
	var queryPart map[string]any

	if r.config.Embedding == nil {
		queryPart = map[string]any{
			"match": map[string]any{
				r.config.ContentField: map[string]any{
					"query":    query,
					"operator": "and",          // <-- every query term must be present
				},
			},
		}
	} else { ... }
```

That branch is taken because the memory store is constructed **without an
embedder** (consumer side, `internal/server/server.go:821-827` in
rancher-doc-chat-api-k8s — no `Embedding` field is set), so
`components/agent/memory/opensearch/store.go:109-124` builds the retriever with
`Embedding: nil`.

Now combine that with the two ends of the query:

- **Query** = the last **1–2 user messages joined**
  (`components/agent/memory/agent.go:190-203`, `const maxUserMessages = 2`) —
  i.e. a long, multi-sentence natural-language prompt.
- **Documents** = memories that are deliberately **one concise sentence each**
  (`components/agent/memory/prompts/extraction_system.md`, rule 7: "Keep content
  concise - one sentence per item").

`operator: "and"` requires **every single term of the long query** to appear in
that one short sentence. In practice this never happens, so
`store.Retrieve` returns zero hits, `enrichInput` never injects anything
(`agent.go:151-167`), and the supervisor sees no memory context — exactly the
reported symptom.

### Blast radius (why only memory is affected)

In the consumer, the documentation and inventory RAG retrievers **do** pass an
`Embedding`, so they take the kNN/hybrid branch, whose BM25 side uses the
default OR semantics (`retriever.go:228-230`). The pathological `operator: "and"`
path is therefore reached **only** by the memory-agent store.

### Not the same bug as the prompt-enhance defect

Confirmed unrelated: the memory agent captures its retrieval query **and** its
extraction `userContent` as a string snapshot *before* the inner supervisor runs
(`agent.go:132`, `:136`, `:142`, `:305`). The prompt-enhance middleware runs
inside the supervisor, after that snapshot, so it cannot corrupt memory
retrieval or extraction.

## Why this cannot be fixed consumer-side

- The BM25 query body is built inside `retriever.go`; nothing in
  `memoryagent.Config` or `memagentopensearch.Config` exposes the match operator.
- The retriever instance is created *internally* by
  `memagentopensearch.NewStore` (store.go:109), so the consumer cannot inject a
  pre-configured retriever.
- The obvious workaround — pass an `Embedding` to switch to the kNN branch — is
  blocked today because the memory store hard-codes the vector dimension to
  **384** (`components/agent/memory/opensearch/store.go:175-185`), which is
  incompatible with the consumer's `text-embedding-3-small` (1536 dims), and the
  existing index was created with no vector field at all.

## Goals

- Make memory retrieval actually return relevant memories for natural-language
  queries.
- Keep precision acceptable so irrelevant memories are not injected as
  "authoritative reference data" (that framing comes from
  `agent.go:205-213` / `NewMemoryContextMessage`).
- Keep the change configurable rather than another hard-coded constant.

## Non-goals

- Reworking how the memory agent builds its query (optional Part 3 below).
- Enabling vector/kNN memory retrieval (optional Part 2 below; needs a reindex).
- Any change to conversation-history memory or the prompt-enhance package.

## Design decisions

1. **Make the BM25 match configurable** on `retriever/opensearch.Config`:
   `Operator`, `MinimumShouldMatch`, `MinScore`.
2. **Default `Operator` to `"or"`.** Rationale: `operator: and` on multi-sentence
   natural-language queries is a latent bug for *every* pure-BM25 consumer, not
   just the memory agent; OR + BM25 relevance ranking + `topK` is the standard
   RAG behavior. Consumers that genuinely want strict AND can now set it
   explicitly — an escape hatch that did not exist before.
3. **Add `MinScore`** so the recall gained from OR does not turn into noise: with
   OR, a query always matches *something* (low-IDF words), and the memory context
   is presented to the model as authoritative. `min_score` drops weak hits so
   "no relevant memory" correctly yields *no* injection.
4. **Surface the knobs on the memory store** (`agent/memory/opensearch.Config`)
   and pass them through, so a deployment can tune precision without touching
   eino-ext.
5. Do **not** add `minimum_should_match` as a default. For this asymmetric
   long-query/short-document case a percentage threshold re-creates the original
   "matches nothing" failure. It is exposed for consumers who want it, default
   empty (unset).

## Implementation tasks (ordered)

### Part 1 — core fix (required)

#### 1.1 `components/retriever/opensearch/retriever.go`
- Add to `Config` (near the existing `ContentField` / `Hybrid` / `K` fields,
  ~line 63-73):
  ```go
  // Operator is the match operator used by the pure-BM25 query ("or" or "and").
  // Defaults to "or". "and" requires every query term to be present, which is
  // pathological for multi-sentence natural-language queries.
  Operator string `validate:"omitempty,oneof=or and" jsonschema:"description=BM25 match operator: or (default) or and"`

  // MinimumShouldMatch is passed through to the BM25 match query when non-empty
  // (e.g. "2<70%"). Unset by default.
  MinimumShouldMatch string `validate:"omitempty" jsonschema:"description=Optional minimum_should_match for the BM25 match query"`

  // MinScore, when > 0, is sent as the search body's min_score so weakly
  // matching hits are dropped instead of returned as low-relevance noise.
  MinScore float64 `validate:"omitempty,gte=0" jsonschema:"description=Optional min_score threshold for search results"`
  ```
- In `NewRetriever` (~line 97-140), after the `ContentField` default, add:
  ```go
  if config.Operator == "" {
      config.Operator = "or"
  }
  ```
  (Set it before `validate.Struct(config)` so the `oneof` tag passes.)
- In `buildSearchBody` (line 198), pure-BM25 branch (lines 201-209): replace the
  hard-coded `"operator": "and"` with `r.config.Operator`, and add
  `minimum_should_match` only when `r.config.MinimumShouldMatch != ""`.
- In `buildSearchBody`, after `body` is assembled (lines 239-246): add
  `if r.config.MinScore > 0 { body["min_score"] = r.config.MinScore }`.
- Leave the kNN/hybrid branch (lines 210-237) unchanged.

#### 1.2 `components/agent/memory/opensearch/store.go`
- Add pass-through fields to `Config` (~lines 28-42):
  `Operator string`, `MinimumShouldMatch string`, `MinScore float64`
  (same jsonschema/validate style as above).
- Forward them in the `retrieveropensearch.Config` literal at **store.go:109-121**:
  ```go
  Operator:           cfg.Operator,
  MinimumShouldMatch: cfg.MinimumShouldMatch,
  MinScore:           cfg.MinScore,
  ```
- Do **not** set a non-zero `MinScore` default in the store: leave `0` (disabled)
  so the fix is purely additive, and let deployments tune it. Document the
  recommended starting point in the README instead.

#### 1.3 READMEs
- `components/retriever/opensearch/README.md`: document `Operator` (default
  `"or"`, previously hard-coded `"and"`), `MinimumShouldMatch`, `MinScore`, and
  call out the default change in a "Behavior change" note.
- `components/agent/memory/opensearch/README.md`: document the new pass-through
  fields and note that memory retrieval is BM25-only unless an `Embedding` is
  supplied.

### Part 2 — optional / deferred: allow real vector memory retrieval

Only do this if semantic (not keyword) memory recall is wanted. It requires an
index recreation/reindex on the consumer side.

- `components/agent/memory/opensearch/store.go:175-185`: replace the hard-coded
  `"dimension": 384` with a `Config.VectorDimension int` (default 384 for
  backward compatibility) so a 1536-dim embedder (`text-embedding-3-small`) can
  be used.
- Optionally expose `Hybrid` / `K` defaults suited to memory (they already exist
  on the store Config, lines 38-39, and are already forwarded).
- Note in the README that changing the dimension requires deleting/reindexing the
  memory index, because the mapping is only created when the index is absent
  (`ensureIndex`, store.go:135-148).

### Part 3 — optional: bound the memory query

With OR, a very long query dilutes ranking. Optionally cap the query in
`components/agent/memory/agent.go:190-203` (`buildQuery`), e.g. add a
`MaxQueryChars` (default ~512) on `memoryagent.Config` and truncate the joined
result. Keep `maxUserMessages = 2`. Skip unless retrieval precision is still poor
after Part 1.

## Tests

`components/retriever/opensearch/retriever_test.go` (add; a
`buildSearchBody` unit test needs no live cluster since it returns a `map`):
- `TestBuildSearchBody_DefaultOperatorIsOr` — `Embedding: nil`, assert
  `query.match.<content>.operator == "or"`. **This is the regression test for
  this bug.**
- `TestBuildSearchBody_ExplicitAndOperator` — `Operator: "and"` is honored.
- `TestBuildSearchBody_MinimumShouldMatch` — present only when configured.
- `TestBuildSearchBody_MinScore` — `body["min_score"]` set only when `> 0`.
- `TestBuildSearchBody_HybridUnchanged` — with `Embedding != nil` the kNN/hybrid
  body is byte-for-byte what it was before (guards the blast radius).
- `TestNewRetriever_DefaultsOperator` — `Config{Operator: ""}` → `"or"`.

`components/agent/memory/opensearch/store_test.go`:
- Assert the new fields are forwarded into the retriever config (or, if the
  existing test style requires a live cluster, add a small constructor-level test
  mirroring the existing ones in that file).

## Validation

In eino-ext:
- `go test ./components/retriever/opensearch/... ./components/agent/memory/...`
- `go build ./...`, `go vet ./...`, `gofmt -l` on changed files.

In the consumer (rancher-doc-chat-api-k8s), after bumping the module:

1. **First rule out an empty store** (competing root cause — if nothing was ever
   extracted, retrieval is not the problem):
   ```
   GET etlo-assistant-agent-memory/_count
   ```
   (index name from `config.app.yaml:283`). If the count is 0, stop: the defect is
   in extraction, not retrieval, and this plan does not apply.
2. Confirm retrieval now matches, comparing old vs new semantics:
   ```
   GET etlo-assistant-agent-memory/_search
   { "query": { "match": { "content": { "query": "<a recent user prompt>", "operator": "and" } } } }
   GET etlo-assistant-agent-memory/_search
   { "query": { "match": { "content": { "query": "<same prompt>", "operator": "or" } } } }
   ```
   Expect ~0 hits for `and` and relevant hits for `or` — this is the direct
   confirmation of the root cause.
3. Functional check: ask something the assistant previously learned; confirm the
   answer arrives directly from memory. Optionally raise the memory agent log
   level to confirm memories are injected.
4. If injected memories look loosely related, set `MinScore` on the memory store
   config and re-test.

## Risks

- **Default `Operator` change (`and` → `or`)** affects any pure-BM25 consumer of
  `retriever/opensearch`. In this app that is only the memory store (doc and
  inventory retrievers use the embedding path). External consumers relying on
  AND must set `Operator: "and"` explicitly — call this out in the release notes.
- **Recall/precision trade-off**: OR always matches something. `MinScore` is the
  mitigation; it is off by default, so a deployment that sees irrelevant memory
  injections should enable it rather than reverting to AND.
- **Part 2 requires a reindex**; `ensureIndex` only creates the mapping when the
  index does not exist, so an existing 0-vector index will not gain a
  `knn_vector` field on its own.

## Rollout

1. Land Part 1 in eino-ext; tag/commit.
2. Consumer: `go get github.com/webcenter-fr/eino-ext@latest`, rebuild, redeploy.
   No consumer config change is required — the fixed default takes effect
   automatically.
3. Optionally set `memoryAgent.opensearch.minScore` once the knob is plumbed
   through the consumer config (a small consumer-side follow-up in
   `loadMemoryAgentConfig`, `internal/server/server.go:816-831`).
