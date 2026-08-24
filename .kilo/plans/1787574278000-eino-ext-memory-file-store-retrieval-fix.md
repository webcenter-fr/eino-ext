# Fix memory-agent FILE store retrieval: whole-query substring match returns nothing

> Companion to `1787557395000-eino-ext-memory-agent-bm25-retrieval-fix.md` (the
> OpenSearch backend). Same class of defect — retrieval assumes the query is
> contained in the document — but a different backend and a different fix.
> The OpenSearch fix does **not** help deployments using `memoryAgent.type: file`.

## Target repository

`github.com/webcenter-fr/eino-ext`
Verified against `v0.0.0-20260824100100-a7bc32605360` (current pin). **Present in that commit.**

All paths below are relative to the **eino-ext repo root**.

## Symptom

With `memoryAgent.type: file` (the default backend, used for local runs), the
long-term memory agent never answers from stored memories.

## Root cause

`components/agent/memory/file/store.go:126-155`, specifically **line 147**:

```go
queryLower := strings.ToLower(strings.TrimSpace(query))
var docs []*schema.Document
for _, id := range s.order {
	entry, ok := s.entries[id]
	if !ok {
		continue
	}
	if query == "" || strings.Contains(strings.ToLower(entry.Content), queryLower) {
		docs = append(docs, entry.ToDocument())
	}
	...
}
```

`strings.Contains(content, query)` requires the **entire query string** to be a
literal substring of a **single memory**. But:

- The query is the last 1–2 **whole user messages** joined by `\n`
  (`components/agent/memory/agent.go:190-203`, `maxUserMessages = 2`).
- Memories are deliberately **one short sentence each**
  (`components/agent/memory/prompts/extraction_system.md`, rule 7).

So the query is almost always *longer* than any single memory, which makes the
containment check **mathematically impossible** to satisfy whenever
`len(query) > len(entry.Content)`. Retrieval returns zero documents, so
`enrichInput` injects nothing (`agent.go:151-167`) and the agent behaves as if it
has no memory.

### Empirical proof

Against the real `file.Store` with three realistic one-sentence memories:

| Query | Hits |
|---|---|
| `"Provide the kubectl command syntax required to execute a test operation on a Kafka pod in the logcentralizer-rec namespace."` | **0** |
| `"re run the command"` | **0** |
| `"kafka"` (single keyword) | 1 |
| `"logcentralizer-rec namespace"` (exact substring of a memory) | 1 |
| `""` (empty) | 3 (all) |

Only a query that is literally contained in a memory matches. Real agent queries
never are.

### Secondary defect: no ranking

Even when something matches, results are returned in **insertion order** and
simply truncated at `topK` (store.go:150-152). `Agent.enrichInput` then truncates
again with `docs[:a.maxMemoriesPerRetrieve]` (`agent.go:160-162`), so the agent
receives the **oldest** matches rather than the most relevant ones. The store
implements `retriever.Retriever` (`components/agent/memory/store.go:14-16`), whose
contract is relevance-ranked retrieval, so this is a contract violation too.

## Why this cannot be fixed consumer-side

`Retrieve` is implemented inside eino-ext; the consumer only supplies
`file.Config{Dir: ...}`. There is no hook, comparator, or scorer to override.

## Goals

- Realistic natural-language queries retrieve the relevant memories.
- Results are ordered by relevance, so `maxMemoriesPerRetrieve` keeps the best.
- Behavior is consistent with the OpenSearch backend after its BM25 `or` fix.
- Avoid the opposite failure: a query must not match every memory via stopwords.

## Non-goals

- Embeddings/vector search for the file store (it is the simple local backend).
- Changing `Agent.buildQuery` or the extraction prompts.
- Any change to the OpenSearch backend (already fixed).

## Design decisions

1. **Replace containment with term-overlap scoring.** Tokenize both the query and
   `entry.Content`, score by how many distinct query terms the memory contains,
   and drop zero-score entries. This is the in-process analogue of the BM25 `or`
   semantics the OpenSearch backend now uses.
2. **Rank before truncating.** Sort by score descending, tie-broken by recency
   (`UpdatedAt`, then `CreatedAt`, then insertion order) so `topK` and
   `maxMemoriesPerRetrieve` keep the best rather than the oldest.
3. **Normalize slightly by content length.** Prefer a memory that matches 3 of
   its 8 terms over a long memory that matches 3 of 60. Keep it simple:
   `score = matchedTerms + (matchedTerms / contentTermCount)` or an explicit
   `float64` score; exact formula is an implementation detail, but it must be
   deterministic.
4. **Ignore stopwords and 1-character tokens** when building query terms.
   Without this, "the"/"a"/"is" makes every query match every memory — the exact
   noise problem `MinScore` guards against on the OpenSearch side.
   If *all* query terms are stopwords, return no documents (not everything).
5. **Preserve `query == ""` → return all** (bounded by `topK`). `Agent.enrichInput`
   already guards with `userQuery != ""` (agent.go:151), and `Maintainer`/other
   callers rely on the "list-like" behavior.
6. **Optional `MinScore`** on `file.Config` to mirror
   `agent/memory/opensearch.Config.MinScore`, default unset (no threshold).

## Implementation tasks

### 1. `components/agent/memory/file/store.go`

- Add to `Config` (optional, keeps the fix additive):
  ```go
  // MinScore, when non-nil, drops matches scoring below this threshold.
  // nil means "not set". Mirrors agent/memory/opensearch.Config.MinScore.
  MinScore *float64 `json:"min_score,omitempty" jsonschema:"description=Optional minimum relevance score"`
  ```
  Store it on `Store`; validate `>= 0` in `NewStore` like the OpenSearch store does.

- Add unexported helpers in the same file:
  - `tokenize(s string) []string` — lowercase, split on any non-letter/non-digit
    rune (`unicode.IsLetter` / `unicode.IsDigit`), drop tokens of length < 2 and
    stopwords. Keep digits so identifiers like `ran37hpd2` survive.
  - `var stopwords = map[string]struct{}{...}` — a small English/French set
    (`the, a, an, and, or, of, to, in, on, for, is, are, with, that, this, it,
    le, la, les, de, des, du, et, un, une, pour, sur, dans, que, qui`).
    Keep it small and documented; this is a heuristic, not linguistics.
  - `scoreEntry(queryTerms []string, content string) float64` — count distinct
    query terms present in the tokenized content, return 0 when none match,
    and apply the length normalization from decision 3.

- Rewrite `Retrieve` (lines 126-155) to:
  1. Return `nil, nil` when there are no entries (unchanged).
  2. Resolve `topK` from `retriever.GetCommonOptions` (unchanged).
  3. When `strings.TrimSpace(query) == ""`, return entries in insertion order
     bounded by `topK` (unchanged behavior).
  4. Otherwise build `queryTerms := tokenize(query)`; if empty, return `nil, nil`.
  5. Score every entry; skip zero scores and scores below `MinScore` when set.
  6. Sort by score desc, then `UpdatedAt` desc, then `CreatedAt` desc, then
     insertion index asc (fully deterministic; use `sort.SliceStable`).
  7. Truncate to `topK` and return `entry.ToDocument()` for each.

- Keep the `s.mu.RLock()` usage; scoring is read-only.

### 2. `components/agent/memory/file/README.md`

Document the retrieval semantics: term-overlap scoring, relevance ordering,
stopword filtering, `MinScore`, and the empty-query behavior. Explicitly note
that it is a lexical (not semantic) match, so paraphrases will not be found —
that is expected for the local backend.

### 3. Tests — `components/agent/memory/file/store_test.go`

- `TestRetrieve_RealisticQueryMatchesMemory` — **the regression test for this
  bug**: store `"The user works with the logcentralizer-rec namespace on cluster ran37hpd2."`,
  query with a full sentence such as
  `"Provide the kubectl command syntax to run a test on a Kafka pod in the logcentralizer-rec namespace."`,
  assert ≥ 1 hit and that the memory is returned.
- `TestRetrieve_RanksByOverlap` — the memory sharing more query terms is first.
- `TestRetrieve_TopKKeepsBestNotOldest` — with `topK=1` and the best match stored
  last, assert the best match is returned (guards the ranking defect).
- `TestRetrieve_NoOverlapReturnsNothing` — unrelated query → 0 hits.
- `TestRetrieve_StopwordOnlyQueryReturnsNothing` — `"the a of"` → 0 hits.
- `TestRetrieve_EmptyQueryReturnsAll` — preserves existing behavior.
- `TestRetrieve_MinScoreFiltersWeakMatches` — single common term filtered out
  when `MinScore` is set.
- `TestRetrieve_IdentifiersSurviveTokenization` — a query containing
  `ran37hpd2` matches the memory containing it.

## Validation

In eino-ext:
- `go test ./components/agent/memory/...`
- `go build ./...`, `go vet ./...`, `gofmt -l` on changed files.

In the consumer after bumping:
1. **Rule out an empty store first** — if extraction never produced anything,
   retrieval is not the problem:
   ```
   wc -l <memoryAgent.file.path>/memories.jsonl
   ```
   (default path in this repo's config: `./data/memory-agent/memories.jsonl`).
   Zero lines/missing file ⇒ investigate extraction, not retrieval.
2. Teach the agent a fact in one conversation, start a **new** conversation, and
   ask a question that uses different wording but shares key terms. It should now
   answer from memory.
3. Confirm `memories.jsonl` grows after turns that contain durable facts.

## Risks

- **Lexical-only matching**: paraphrases with no shared terms still miss. Acceptable
  for the local/file backend; semantic recall is what the OpenSearch + embedding
  path is for.
- **Stopword list is heuristic** and English/French-biased. Mitigated by keeping it
  small and by falling back to "no results" only when *every* term is a stopword.
- **Behavior change for existing callers** that relied on substring semantics.
  Low risk: the current behavior returns nothing for realistic queries, so almost
  nothing can depend on it.

## Rollout

1. Land in eino-ext; tag/commit.
2. Consumer: `go get github.com/webcenter-fr/eino-ext@latest`, rebuild, redeploy.
   No consumer config change required.

## Interim workaround (no eino-ext change)

Point the local config at the OpenSearch backend
(`memoryAgent.type: "opensearch"`), whose BM25 `operator: "or"` path already
works after the previous fix. Only useful where an OpenSearch cluster is reachable.
