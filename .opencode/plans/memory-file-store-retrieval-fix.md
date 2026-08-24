# Fix `agent/memory/file` retrieval: whole-query substring match returns nothing

**Repo:** `/projects/eino-ext` (module `github.com/webcenter-fr/eino-ext`, Go 1.26.3)
**Status:** implementation-ready. This document is self-contained — a coder can execute it without reading any other file.

---

## 1. Objective and background

### Bug

`components/agent/memory/file/store.go` `Retrieve()` currently matches with:

```go
queryLower := strings.ToLower(strings.TrimSpace(query))
...
if query == "" || strings.Contains(strings.ToLower(entry.Content), queryLower) {
```

(current file: the match line is **store.go:147**, the whole method is **store.go:125-155**).

This requires the **entire query string** to be a literal substring of a **single** memory.

### Root cause

* The query built by the memory agent is the last 1-2 **whole user messages** joined by `\n`
  (`components/agent/memory/agent.go`, `buildQuery`, lines 196-213; `const maxUserMessages = 2` at line 200), truncated only by `maxQueryChars`.
* Memories are stored as **single short sentences** (extraction prompt rule).

So `len(query) > len(entry.Content)` almost always holds, which makes the containment test mathematically unsatisfiable. Retrieval returns 0 documents, `enrichInput` (agent.go:156-179) injects nothing, and the agent behaves as if it has no memory.

### Second defect: no ranking (contract violation)

Matches are appended in **insertion order** and truncated at `topK` (`store.go:150-152`); `Agent.enrichInput` truncates again at `docs[:a.maxMemoriesPerRetrieve]` (agent.go:169-171). The agent therefore receives the **oldest** matches. `Store` is declared as a `retriever.Retriever` (via `memoryagent.MemoryStore`, `components/agent/memory/store.go:14-25`), whose contract is *relevance-ranked* retrieval — so ordering by insertion time is a contract violation.

### Fix summary

1. Replace substring containment with **lexical term-overlap scoring** (tokenize query + content, count distinct matched query terms, add a `<1` coverage bonus).
2. **Rank before truncating**: score desc → `UpdatedAt` desc → `CreatedAt` desc → insertion index asc, via `sort.SliceStable`.
3. **Drop noise**: stopwords and 1-rune tokens are removed from the term sets; a query that tokenizes to nothing returns **no** documents (never everything).
4. Add optional `Config.MinScore` (mirrors the OpenSearch memory store knob).
5. Preserve blank-query "list everything" behavior and current `TopK` semantics.

---

## 2. Scope

### Files to modify (exactly three, no new files)

| File | Change |
|---|---|
| `/projects/eino-ext/components/agent/memory/file/store.go` | imports, `Config.MinScore`, `Store.minScore`, `NewStore`, rewritten `Retrieve`, new unexported helpers `tokenize` / `scoreEntry` / `stopwords` / `metaKeyScore` |
| `/projects/eino-ext/components/agent/memory/file/store_test.go` | 2 new helpers + 8 required tests + 5 recommended tests |
| `/projects/eino-ext/components/agent/memory/file/README.md` | retrieval-semantics + configuration sections |

### Files explicitly NOT to touch

* `components/agent/memory/agent.go` (do **not** change `buildQuery`, `enrichInput`, prompts, `maxMemoriesPerRetrieve`).
* `components/agent/memory/maintainer.go` — it already has its own unexported `tokenize` (line 297) in the **parent** package with **different semantics**; do not unify or refactor it (see §8, Deviation D1).
* `components/agent/memory/store.go`, `types.go` (no `Entry.Score` field; scores are not persisted).
* `components/agent/memory/opensearch/**` and `components/retriever/opensearch/**` (already fixed for BM25).
* `libs/toolkit/**` — no new shared helper (see §8, Deviation D2).
* `components/memory/file/**` — different package (conversation history), unrelated.

---

## 3. `components/agent/memory/file/store.go` — ordered tasks

The current file is 296 lines. Apply the edits below in order; each shows the **exact current text** to replace.

### T1 — imports: add `sort` and `unicode`

Replace:

```go
import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
```

with:

```go
import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
```

(`strings` stays: still used by `tokenize`, `TrimSpace`, `strings.Builder`. `emperror.dev/errors` stays: still used by `load`, `Store`, `rewriteLocked`, `NewStore`.)

### T2 — `metaKeyScore` constant

Replace:

```go
var _ memoryagent.MemoryStore = (*Store)(nil)
```

with:

```go
var _ memoryagent.MemoryStore = (*Store)(nil)

// metaKeyScore is the metadata key carrying the lexical relevance score of a
// retrieved memory. It mirrors the "_score" key that
// components/retriever/opensearch sets on OpenSearch hits, so both memory
// backends expose the score under the same name.
const metaKeyScore = "_score"
```

### T3 — `Config.MinScore`

Replace:

```go
// Config holds configuration for the JSONL-backed memory store.
type Config struct {
	Dir string `json:"dir" validate:"required" jsonschema:"description=Directory for the memories.jsonl file,default=/tmp/eino/memory-agent"`
}
```

with:

```go
// Config holds configuration for the JSONL-backed memory store.
type Config struct {
	Dir string `json:"dir" validate:"required" jsonschema:"description=Directory for the memories.jsonl file,default=/tmp/eino/memory-agent"`

	// MinScore, when non-nil, drops retrieved memories whose lexical relevance
	// score is below this threshold (score >= MinScore is kept). nil means "no
	// threshold". A value of 0 behaves like nil, because non-matching entries
	// are always dropped. Mirrors agent/memory/opensearch.Config.MinScore.
	MinScore *float64 `json:"min_score,omitempty" validate:"omitempty,gte=0" jsonschema:"description=Optional minimum relevance score for retrieved memories"`
}
```

**Validation rule:** `omitempty,gte=0` on a `*float64` is enforced by the existing `validate.Struct(&cfg)` call already present in `NewStore` — `go-playground/validator` dereferences non-nil pointers, and `omitempty` skips a nil pointer. No manual `if` is added: `CONTRIBUTING.md` (lines 148-150) forbids ad hoc manual checks when a tag expresses the constraint. See §8 Deviation D3 for why this differs from the OpenSearch store.

**Error wrapping:** `NewStore` already wraps validation failures via `errors.Wrap(err, "invalid file store config")` (`emperror.dev/errors`), and `validate.Struct` itself wraps with `invalid parameters for *file.Config`. A negative `MinScore` therefore produces an error whose message contains `min_score` and `must be >= 0`. Do not add new error paths.

### T4 — `Store.minScore` field

Replace:

```go
// Store is a JSONL-file-backed implementation of MemoryStore.
type Store struct {
	mu      sync.RWMutex
	dir     string
	entries map[string]*memoryagent.Entry
	order   []string
}
```

with:

```go
// Store is a JSONL-file-backed implementation of MemoryStore.
type Store struct {
	mu      sync.RWMutex
	dir     string
	entries map[string]*memoryagent.Entry
	order   []string

	// minScore is an owned copy of Config.MinScore (nil when unset), so a
	// caller mutating its own float64 after NewStore cannot race with Retrieve.
	minScore *float64
}
```

### T5 — `NewStore`: copy `MinScore` by value

Replace:

```go
	s := &Store{
		dir:     cfg.Dir,
		entries: make(map[string]*memoryagent.Entry),
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}
```

with:

```go
	s := &Store{
		dir:     cfg.Dir,
		entries: make(map[string]*memoryagent.Entry),
	}
	if cfg.MinScore != nil {
		// Copy the value: the store must not alias caller-owned memory.
		minScore := *cfg.MinScore
		s.minScore = &minScore
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}
```

Do **not** reorder the existing `validate.Struct(&cfg)` / `cfg.Dir` default block (see §8 Observation D5).

### T6 — rewrite `Retrieve`

Replace the whole current block (store.go:125-155), i.e. from `// Retrieve searches in-memory entries matching the query.` through the closing `}` of the method, with:

```go
// Retrieve returns the memories most relevant to the query, ranked by a lexical
// term-overlap score (see scoreEntry). Matching is lexical, not semantic: a
// paraphrase that shares no term with a memory will not be found.
//
// Behavior:
//   - Empty store: nil, nil.
//   - Blank query (empty or whitespace only): every entry in insertion order,
//     bounded by TopK (preserves the historical "list everything" behavior).
//   - Query that tokenizes to nothing (only stopwords, punctuation, or
//     single-rune tokens): nil, nil — never "everything".
//   - Otherwise: entries with a non-zero score (and >= MinScore when
//     configured), sorted by score desc, then UpdatedAt desc, CreatedAt desc,
//     insertion index asc, truncated to TopK. Each returned document carries
//     its score under the "_score" metadata key.
//
// TopK semantics are unchanged: a nil, zero, or negative TopK means "no limit".
func (s *Store) Retrieve(_ context.Context, query string, opts ...retriever.Option) ([]*schema.Document, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.entries) == 0 {
		return nil, nil
	}

	retOpts := retriever.GetCommonOptions(&retriever.Options{}, opts...)
	topK := len(s.entries)
	if retOpts.TopK != nil && *retOpts.TopK > 0 && *retOpts.TopK < topK {
		topK = *retOpts.TopK
	}

	// Blank query: list-like behavior, insertion order, bounded by topK.
	if strings.TrimSpace(query) == "" {
		docs := make([]*schema.Document, 0, topK)
		for _, id := range s.order {
			entry, ok := s.entries[id]
			if !ok {
				continue
			}
			docs = append(docs, entry.ToDocument())
			if len(docs) >= topK {
				break
			}
		}
		return docs, nil
	}

	queryTerms := tokenize(query)
	if len(queryTerms) == 0 {
		// Nothing discriminative to match on: returning every memory would be
		// worse than returning none.
		return nil, nil
	}

	type scoredEntry struct {
		entry *memoryagent.Entry
		score float64
		index int
	}

	matches := make([]scoredEntry, 0, len(s.order))
	for i, id := range s.order {
		entry, ok := s.entries[id]
		if !ok {
			continue
		}
		score := scoreEntry(queryTerms, entry.Content)
		if score <= 0 {
			continue
		}
		if s.minScore != nil && score < *s.minScore {
			continue
		}
		matches = append(matches, scoredEntry{entry: entry, score: score, index: i})
	}
	if len(matches) == 0 {
		return nil, nil
	}

	// Fully deterministic ranking: relevance, then recency, then insertion order.
	sort.SliceStable(matches, func(i, j int) bool {
		a, b := matches[i], matches[j]
		if a.score != b.score {
			return a.score > b.score
		}
		if !a.entry.UpdatedAt.Equal(b.entry.UpdatedAt) {
			return a.entry.UpdatedAt.After(b.entry.UpdatedAt)
		}
		if !a.entry.CreatedAt.Equal(b.entry.CreatedAt) {
			return a.entry.CreatedAt.After(b.entry.CreatedAt)
		}
		return a.index < b.index
	})

	if len(matches) > topK {
		matches = matches[:topK]
	}

	docs := make([]*schema.Document, 0, len(matches))
	for _, m := range matches {
		doc := m.entry.ToDocument()
		// ToDocument always initializes MetaData, so this is safe.
		doc.MetaData[metaKeyScore] = m.score
		docs = append(docs, doc)
	}
	return docs, nil
}
```

### T7 — add the helpers

Insert the following **immediately after** the new `Retrieve` and **before** `// Delete removes a document from the store by ID.`:

```go
// stopwords are query/content tokens that carry no discriminative signal.
// Without this filter a natural-language query would match every memory through
// words like "the" or "de". The list is a deliberately small English/French
// heuristic, not a linguistic model; extend it only with words that can never
// be meaningful content in a memory.
var stopwords = map[string]struct{}{
	// English
	"an": {}, "and": {}, "are": {}, "as": {}, "at": {}, "be": {}, "been": {},
	"but": {}, "by": {}, "can": {}, "did": {}, "do": {}, "does": {}, "for": {},
	"from": {}, "had": {}, "has": {}, "have": {}, "if": {}, "in": {}, "into": {},
	"is": {}, "it": {}, "its": {}, "me": {}, "my": {}, "not": {}, "of": {},
	"on": {}, "or": {}, "our": {}, "please": {}, "should": {}, "than": {},
	"that": {}, "the": {}, "their": {}, "them": {}, "then": {}, "there": {},
	"these": {}, "they": {}, "this": {}, "to": {}, "was": {}, "were": {},
	"what": {}, "when": {}, "where": {}, "which": {}, "will": {}, "with": {},
	"would": {}, "you": {}, "your": {},
	// French
	"au": {}, "aux": {}, "avec": {}, "ce": {}, "ces": {}, "cette": {},
	"dans": {}, "de": {}, "des": {}, "du": {}, "elle": {}, "en": {}, "est": {},
	"et": {}, "il": {}, "ils": {}, "je": {}, "la": {}, "le": {}, "les": {},
	"leur": {}, "ma": {}, "mais": {}, "mon": {}, "ne": {}, "nous": {},
	"ou": {}, "par": {}, "pas": {}, "plus": {}, "pour": {}, "que": {},
	"qui": {}, "sa": {}, "se": {}, "ses": {}, "son": {}, "sont": {}, "sur": {},
	"tu": {}, "un": {}, "une": {}, "votre": {}, "vous": {},
}

// tokenize lowercases s, splits it on every rune that is neither a letter nor a
// digit, then drops single-rune tokens and stopwords. Digits are kept so
// identifiers such as "ran37hpd2" survive, and unicode.IsLetter keeps accented
// letters ("préférences") intact. Returns nil when nothing meaningful remains.
func tokenize(s string) []string {
	var (
		tokens  []string
		current strings.Builder
	)
	flush := func() {
		if current.Len() == 0 {
			return
		}
		token := current.String()
		current.Reset()
		if len([]rune(token)) < 2 {
			return
		}
		if _, isStopword := stopwords[token]; isStopword {
			return
		}
		tokens = append(tokens, token)
	}
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			current.WriteRune(r)
			continue
		}
		flush()
	}
	flush()
	return tokens
}

// scoreEntry returns the lexical relevance of content for the given query terms:
//
//	score = matched + matched/distinctContentTerms
//
// where matched is the number of distinct query terms present in the tokenized
// content. The second term is a coverage bonus in (0, 1] that favors short,
// focused memories over long ones matching the same number of terms. Because the
// bonus is always > 0 and <= 1, an entry matching n+1 terms always outranks one
// matching n terms (max score for n is exactly n+1, min score for n+1 is
// strictly greater than n+1). Returns 0 when nothing matches, so callers can
// treat 0 as "not relevant".
func scoreEntry(queryTerms []string, content string) float64 {
	contentTokens := tokenize(content)
	if len(queryTerms) == 0 || len(contentTokens) == 0 {
		return 0
	}

	contentSet := make(map[string]struct{}, len(contentTokens))
	for _, token := range contentTokens {
		contentSet[token] = struct{}{}
	}

	seen := make(map[string]struct{}, len(queryTerms))
	matched := 0
	for _, term := range queryTerms {
		if _, duplicate := seen[term]; duplicate {
			continue
		}
		seen[term] = struct{}{}
		if _, ok := contentSet[term]; ok {
			matched++
		}
	}
	if matched == 0 {
		return 0
	}

	return float64(matched) + float64(matched)/float64(len(contentSet))
}
```

### T8 — enumerated edge cases (must all hold after T1-T7)

| Case | Expected behavior | Where handled |
|---|---|---|
| Empty store | `nil, nil` | `len(s.entries) == 0` early return (unchanged) |
| `query == ""` | all entries, insertion order, bounded by `topK` | blank-query fast path |
| Whitespace-only query (`"  \n\t "`) | same as `""` | `strings.TrimSpace(query) == ""` (already the effective behavior today — see D9) |
| Query tokenizing to nothing (`"the a of is"`, `"??? !!!"`, `"x y z"`) | `nil, nil` | `len(queryTerms) == 0` guard |
| Entry whose content tokenizes to nothing | never returned for a non-blank query (score 0); still returned by the blank-query path | `scoreEntry` returns 0 |
| Zero-score entries | always filtered, regardless of `MinScore` | `if score <= 0 { continue }` |
| `MinScore == nil` | no threshold | `s.minScore != nil` guard |
| `MinScore == 0` | identical to nil (zero scores already dropped) | same |
| `MinScore < 0` | `NewStore` returns a validation error | `validate:"omitempty,gte=0"` |
| Score ties | deterministic: `UpdatedAt` desc → `CreatedAt` desc → insertion index asc | `sort.SliceStable` comparator |
| Entries stored in one `Store()` call | share the same `CreatedAt`/`UpdatedAt` (single `now` per call, store.go:100), so ties fall through to insertion index | comparator last clause |
| `TopK` nil / `0` / negative | no limit (`topK = len(s.entries)`) | unchanged guard `*retOpts.TopK > 0` |
| `TopK` > number of matches | returns all matches | `if len(matches) > topK` |
| Concurrent `Retrieve` + `Store` | safe: `Retrieve` holds `s.mu.RLock()` for the whole scoring/sorting pass; scoring is read-only, no entry mutation | unchanged locking |
| Unicode / accents | preserved (`unicode.IsLetter`), lowercased | `tokenize` |
| Digits / identifiers (`ran37hpd2`, `eu-west-3`) | `ran37hpd2` kept; `eu`, `west` kept; `3` dropped (single rune) | `tokenize` |
| CJK / space-less scripts | degraded (whole run becomes one token) — documented limitation, not fixed here | README |

**Errors:** `Retrieve` still returns `error` only to satisfy `retriever.Retriever`; it never produces a non-nil error (no I/O in the read path). `NewStore` error paths are unchanged: `errors.Wrap(err, "invalid file store config")`, `errors.Wrap(err, "create memory dir")`, and `load()`'s `errors.Wrap(err, "open memories file")` — all `emperror.dev/errors`.

---

## 4. Tests — `components/agent/memory/file/store_test.go`

Existing style: package `file` (internal tests), `context.Background()`, `testify` `require`/`assert`, helper `newTestStore(t)` using `t.TempDir()`. Keep that style. Add `"time"` to the test imports (needed by T4.13).

### New helpers (add right after the existing `newTestStore`)

```go
func newTestStoreWithMinScore(t *testing.T, minScore float64) *Store {
	t.Helper()
	s, err := NewStore(Config{Dir: t.TempDir(), MinScore: &minScore})
	require.NoError(t, err)
	return s
}

// storeContents stores one document per content string, in order.
func storeContents(t *testing.T, s *Store, contents ...string) {
	t.Helper()
	docs := make([]*schema.Document, 0, len(contents))
	for _, c := range contents {
		docs = append(docs, &schema.Document{Content: c})
	}
	_, err := s.Store(context.Background(), docs)
	require.NoError(t, err)
}
```

### Required tests (the 8 from the external plan)

**T4.1 `TestRetrieve_RealisticQueryMatchesMemory`** — regression test for this bug.

```go
func TestRetrieve_RealisticQueryMatchesMemory(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	storeContents(t, s,
		"The user works with the logcentralizer-rec namespace on cluster ran37hpd2.",
		"The user prefers concise answers.",
		"The project uses PostgreSQL for storage.",
	)

	docs, err := s.Retrieve(ctx, "Provide the kubectl command syntax required to execute a test operation on a Kafka pod in the logcentralizer-rec namespace.")
	require.NoError(t, err)
	require.Len(t, docs, 1)
	assert.Contains(t, docs[0].Content, "logcentralizer-rec")
}
```

Acceptance: exactly 1 hit (matched terms `logcentralizer`, `rec`, `namespace` → score `3 + 3/7 ≈ 3.43`); the other two score 0.

**T4.2 `TestRetrieve_RanksByOverlap`** — more shared terms ranks first, and the score is exposed.

```go
func TestRetrieve_RanksByOverlap(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	// Weak match stored first: insertion order would put it first.
	storeContents(t, s,
		"The user works on the kafka cluster.",
		"The user works on the kafka cluster in the logcentralizer-rec namespace.",
	)

	docs, err := s.Retrieve(ctx, "kafka logcentralizer-rec namespace")
	require.NoError(t, err)
	require.Len(t, docs, 2)
	assert.Contains(t, docs[0].Content, "logcentralizer-rec")
	assert.NotContains(t, docs[1].Content, "logcentralizer-rec")

	first, ok := docs[0].MetaData["_score"].(float64)
	require.True(t, ok)
	second := docs[1].MetaData["_score"].(float64)
	assert.Greater(t, first, second)
}
```

Acceptance: scores `4 + 4/7 ≈ 4.57` vs `1 + 1/4 = 1.25`; best first.

**T4.3 `TestRetrieve_TopKKeepsBestNotOldest`** — guards the ranking defect.

```go
func TestRetrieve_TopKKeepsBestNotOldest(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	storeContents(t, s,
		"The user likes kafka.",
		"The user monitors the kafka broker latency in logcentralizer-rec.",
	)

	docs, err := s.Retrieve(ctx, "kafka logcentralizer-rec", retriever.WithTopK(1))
	require.NoError(t, err)
	require.Len(t, docs, 1)
	assert.Contains(t, docs[0].Content, "broker latency")
}
```

Acceptance: `3 + 3/7 ≈ 3.43` beats `1 + 1/3 ≈ 1.33`, so the newest/best (stored last) survives `topK=1`.

**T4.4 `TestRetrieve_NoOverlapReturnsNothing`**

```go
func TestRetrieve_NoOverlapReturnsNothing(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	storeContents(t, s, "The user works with kafka.")

	docs, err := s.Retrieve(ctx, "quantum chromodynamics lecture notes")
	require.NoError(t, err)
	assert.Empty(t, docs)
}
```

**T4.5 `TestRetrieve_StopwordOnlyQueryReturnsNothing`** — table-driven over degenerate queries.

```go
func TestRetrieve_StopwordOnlyQueryReturnsNothing(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	storeContents(t, s, "The user works with kafka.", "The project uses PostgreSQL.")

	for _, query := range []string{"the a of is it", "de la le des", "??? !!! ---", "x y z"} {
		t.Run(query, func(t *testing.T) {
			docs, err := s.Retrieve(ctx, query)
			require.NoError(t, err)
			assert.Empty(t, docs, "degenerate query must return nothing, not everything")
		})
	}
}
```

**T4.6 `TestRetrieve_EmptyQueryReturnsAll`** — preserves existing behavior (blank + whitespace + topK bound).

```go
func TestRetrieve_EmptyQueryReturnsAll(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	storeContents(t, s, "memory one", "memory two", "memory three")

	docs, err := s.Retrieve(ctx, "")
	require.NoError(t, err)
	assert.Len(t, docs, 3)

	docs, err = s.Retrieve(ctx, "   \n\t ")
	require.NoError(t, err)
	assert.Len(t, docs, 3)

	docs, err = s.Retrieve(ctx, "", retriever.WithTopK(2))
	require.NoError(t, err)
	assert.Len(t, docs, 2)
	assert.Equal(t, "memory one", docs[0].Content)
}
```

**T4.7 `TestRetrieve_MinScoreFiltersWeakMatches`**

```go
func TestRetrieve_MinScoreFiltersWeakMatches(t *testing.T) {
	ctx := context.Background()
	const (
		strong = "The user works with kafka on the logcentralizer-rec namespace."
		weak   = "The user prefers dark mode in the namespace editor."
		query  = "kafka logcentralizer-rec namespace status"
	)

	t.Run("without min score both match", func(t *testing.T) {
		s := newTestStore(t)
		storeContents(t, s, strong, weak)
		docs, err := s.Retrieve(ctx, query)
		require.NoError(t, err)
		require.Len(t, docs, 2)
		assert.Contains(t, docs[0].Content, "kafka")
	})

	t.Run("min score drops the weak match", func(t *testing.T) {
		s := newTestStoreWithMinScore(t, 2)
		storeContents(t, s, strong, weak)
		docs, err := s.Retrieve(ctx, query)
		require.NoError(t, err)
		require.Len(t, docs, 1)
		assert.Contains(t, docs[0].Content, "kafka")
	})
}
```

Acceptance: strong `4 + 4/6 ≈ 4.67`, weak `1 + 1/6 ≈ 1.17`; threshold `2` keeps only the strong one.

**T4.8 `TestRetrieve_IdentifiersSurviveTokenization`**

```go
func TestRetrieve_IdentifiersSurviveTokenization(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	storeContents(t, s, "The user's cluster is ran37hpd2 in region eu-west-3.")

	docs, err := s.Retrieve(ctx, "what is the status of cluster ran37hpd2")
	require.NoError(t, err)
	require.Len(t, docs, 1)
	assert.Contains(t, docs[0].Content, "ran37hpd2")
}
```

### Recommended additional tests

**T4.9 `TestNewStore_MinScoreNegative`** (mirrors `opensearch.TestConfig_MinScoreNegative`)

```go
func TestNewStore_MinScoreNegative(t *testing.T) {
	ms := -1.0
	s, err := NewStore(Config{Dir: t.TempDir(), MinScore: &ms})
	require.Error(t, err)
	assert.Nil(t, s)
	assert.Contains(t, err.Error(), "min_score")
}
```

**T4.10 `TestNewStore_MinScoreZeroAllowed`** — `ms := 0.0` → `require.NoError`.

**T4.11 `TestTokenize`** — table-driven:

| input | expected |
|---|---|
| `""` | `nil` |
| `"the a of is it"` | `nil` |
| `"Go"` | `["go"]` |
| `"logcentralizer-rec namespace"` | `["logcentralizer", "rec", "namespace"]` |
| `"cluster ran37hpd2 eu-west-3"` | `["cluster", "ran37hpd2", "eu", "west"]` |
| `"Préférences de l'utilisateur"` | `["préférences", "utilisateur"]` |
| `"???!!!"` | `nil` |

**T4.12 `TestScoreEntry`** — `scoreEntry(nil, "x") == 0`; no overlap → `0`; `scoreEntry([]string{"kafka","namespace"}, "kafka namespace")` (= 3) > `scoreEntry([]string{"kafka","namespace"}, "kafka")` (= 2); duplicate query terms counted once: `scoreEntry([]string{"kafka","kafka"}, "kafka broker")` equals `scoreEntry([]string{"kafka"}, "kafka broker")`.

**T4.13 `TestRetrieve_TieBreakPrefersRecent`** — equal scores must order by `UpdatedAt` desc, deterministically. Use explicit RFC3339 timestamps in metadata (`Store` keeps non-zero `CreatedAt`/`UpdatedAt` parsed by `EntryFromDocument`):

```go
func TestRetrieve_TieBreakPrefersRecent(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	old := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
	recent := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)

	_, err := s.Store(ctx, []*schema.Document{
		{Content: "kafka cluster alpha", MetaData: map[string]any{"created_at": old, "updated_at": old}},
		{Content: "kafka cluster beta", MetaData: map[string]any{"created_at": recent, "updated_at": recent}},
	})
	require.NoError(t, err)

	for range 3 { // same order on every call
		docs, err := s.Retrieve(ctx, "kafka cluster")
		require.NoError(t, err)
		require.Len(t, docs, 2)
		assert.Equal(t, "kafka cluster beta", docs[0].Content)
		assert.Equal(t, "kafka cluster alpha", docs[1].Content)
	}
}
```

**T4.14 (optional) extend `TestStore_Concurrency`** with `_, _ = s.Retrieve(ctx, "concurrent")` inside the goroutine loop, and run the package with `-race`.

### Existing tests that must keep passing unchanged

* `TestStore_Retrieve`: `"Go"` → 1 (token `go` matches `user likes Go programming language`); `"PostgreSQL"` → 1; `"Ruby"` → 0; `""` → 2. All still hold (no stopword collision; `go` is 2 runes).
* `TestRetrieve_WithTopK`: `"Go"` over 3 entries stored in one call → all score `1 + 1/2 = 1.5`, identical timestamps, so the tie-break falls to insertion index → first 2 returned, `Len == 2`. Holds.
* `TestStore_EmptyStore`, `TestStore_CRUD`, `TestStore_DeleteByFilter`, `TestStore_Persistence`, `TestStore_ListPagination`, `TestStore_IDGeneration`, `TestMatchesFilter_*`, `TestStore_ImplementsMemoryStore`: untouched code paths.

---

## 5. `components/agent/memory/file/README.md` additions

**(a)** In `## How it works`, replace the first bullet:

```md
- **Retrieval** uses simple substring matching against entry content (sorted by
  insertion order). The `TopK` retriever option is respected.
```

with:

```md
- **Retrieval** scores entries by lexical term overlap with the query and
  returns them best-first (see [Retrieval semantics](#retrieval-semantics)).
  The `TopK` retriever option is respected.
```

**(b)** Add a new `## Retrieval semantics` section after `## How it works`:

```md
## Retrieval semantics

`Retrieve` performs a **lexical** (not semantic) ranked search:

1. **Tokenization** — query and entry content are lowercased and split on every
   rune that is neither a letter nor a digit. Tokens shorter than 2 runes and
   stopwords (a small English/French list) are dropped. Digits are kept, so
   identifiers such as `ran37hpd2` and `eu-west-3` remain searchable
   (`eu`, `west`; the trailing `3` is dropped as a single rune).
2. **Scoring** — `score = matched + matched/distinctContentTerms`, where
   `matched` is the number of distinct query terms present in the entry. The
   coverage bonus is in `(0, 1]`, so an entry matching more query terms always
   outranks one matching fewer, while a short focused memory beats a long one
   with the same number of matches.
3. **Filtering** — entries scoring `0` are never returned. When `MinScore` is
   set, entries scoring below it are dropped as well.
4. **Ordering** — score descending, then `updated_at` descending, then
   `created_at` descending, then insertion order. Fully deterministic, so
   `TopK` (and the agent's `MaxMemoriesPerRetrieve`) keeps the *best* matches,
   not the oldest. Each returned document exposes its score under the `_score`
   metadata key, like the OpenSearch backend.
5. **Blank query** — an empty or whitespace-only query returns every entry in
   insertion order, bounded by `TopK` (list-like behavior).
6. **Degenerate query** — a query made only of stopwords, punctuation, or
   single-rune tokens returns **no** documents rather than everything.

> Matching is lexical: a paraphrase sharing no term with a memory is not found.
> That is expected for the local backend — use the OpenSearch backend with an
> embedder for semantic recall.
```

**(c)** Add a `## Configuration` reference table (before or after the existing Go snippet, keeping the snippet):

```md
| Field      | Type       | Required | Default                   | Description                                                     |
|------------|------------|----------|---------------------------|-----------------------------------------------------------------|
| `Dir`      | `string`   | Yes      | `/tmp/eino/memory-agent`  | Directory holding `memories.jsonl`                              |
| `MinScore` | `*float64` | No       | `nil` (no threshold)      | Drops retrieved memories scoring below this value; `0` == `nil`  |
```

Add below the table:

```md
> `MinScore` mirrors `agent/memory/opensearch.Config.MinScore`. Set it to a
> small positive value (e.g. `2`) when memories matching a single common term
> are being injected. Negative values are rejected by `NewStore`.
```

**(d)** Extend `## Limitations` with:

```md
Retrieval is lexical and in-process: paraphrases without shared terms are not
found, and scripts written without word separators (e.g. Chinese, Japanese)
tokenize poorly. The stopword list is a small English/French heuristic.
```

---

## 6. Validation checklist

Run from `/projects/eino-ext`:

```bash
gofmt -l components/agent/memory/file/store.go components/agent/memory/file/store_test.go   # must print nothing
go build ./...
go vet ./...
go test ./components/agent/memory/...
go test -race ./components/agent/memory/file/...
```

Acceptance criteria:

| Check | Criterion |
|---|---|
| `gofmt -l` | no output for the two changed `.go` files (run `gofmt -w` if the snippets above need reflowing) |
| `go build ./...` / `go vet ./...` | clean; in particular no unused import (`sort`, `unicode`, `strings`, `errors` are all used) |
| `TestRetrieve_RealisticQueryMatchesMemory` | 1 hit — **the bug is fixed** |
| `TestRetrieve_RanksByOverlap` | best-overlap doc first; `_score` decreasing |
| `TestRetrieve_TopKKeepsBestNotOldest` | `topK=1` returns the best, not the oldest |
| `TestRetrieve_NoOverlapReturnsNothing` | empty result |
| `TestRetrieve_StopwordOnlyQueryReturnsNothing` | empty for all 4 degenerate queries |
| `TestRetrieve_EmptyQueryReturnsAll` | 3 / 3 / 2 docs; insertion order preserved |
| `TestRetrieve_MinScoreFiltersWeakMatches` | 2 docs without threshold, 1 with `MinScore=2` |
| `TestRetrieve_IdentifiersSurviveTokenization` | identifier query matches |
| `TestNewStore_MinScoreNegative` | error mentioning `min_score`; nil store |
| `TestRetrieve_TieBreakPrefersRecent` | most recent first, identical across repeated calls |
| Pre-existing tests | `TestStore_Retrieve`, `TestRetrieve_WithTopK`, `TestStore_EmptyStore`, CRUD/persistence/pagination tests all pass unmodified |
| `-race` run | no data race reported |

Manual smoke check for the consumer after bumping the dependency (no config change required):

1. `wc -l <dir>/memories.jsonl` — if 0/missing, the problem is extraction, not retrieval.
2. Teach a fact in one conversation, open a **new** conversation, and ask with different wording that shares key terms → the fact should be used.

---

## 7. Non-goals

* No vector/semantic search or embeddings in the file backend.
* No changes to `Agent.buildQuery`, `enrichInput`, `maxQueryChars`, `MaxMemoriesPerRetrieve`, or any extraction prompt.
* No changes to the OpenSearch memory store or the OpenSearch retriever (already fixed for BM25 `or` + `MinScore`).
* No BM25/IDF/TF weighting, stemming, fuzzy matching, or synonym expansion — deliberately simple, deterministic, dependency-free scoring.
* No new shared helper in `libs/toolkit/`, and no refactor of `components/agent/memory/maintainer.go`'s own `tokenize`.
* No fix for the unreachable `Dir` default or the missing `check.go` in this package (see D5, D8) — separate follow-ups.
* No persistence of scores; `memories.jsonl` format is unchanged.

---

## 8. Deviations from the external plan (verified against the real code)

**D1 — `tokenize` already exists in the parent package.** `components/agent/memory/maintainer.go:297` defines an unexported `tokenize(s string) []string` used by `textSimilarity`. It is ASCII-only (`a-z0-9`), keeps single-character tokens, and does **not** filter stopwords — Jaccard similarity for dedup needs those. There is **no compile conflict** (different package: `memory` vs `file`), and the two must **not** be unified: changing the maintainer's tokenizer would alter dedup/merge thresholds. The external plan did not mention this function; do not "clean it up".

**D2 — no reusable helper exists in `libs/toolkit/`.** `libs/toolkit/strutil` only provides `Truncate`, `StripMarkdownFences`, `ExtractJSONBlock`. There is no tokenizer, stopword list, or scorer anywhere in `libs/toolkit/`, so the local unexported helpers in `store.go` are justified under the AGENTS.md "no duplication" rule.

**D3 — validation is done with a tag, not the OpenSearch-style manual check.** The external plan says "validate `>= 0` in `NewStore` like the OpenSearch store does". The real precedent (`components/agent/memory/opensearch/store.go:59` + `:101-103`, and `components/retriever/opensearch/retriever.go:87` + `:129-131`) uses `validate:"omitempty"` **plus** a hand-written `errors.New("MinScore must be >= 0")`. That contradicts `CONTRIBUTING.md:148-150` ("Do NOT ... write ad hoc manual checks: declare the constraint in the struct tag"). This plan uses `validate:"omitempty,gte=0"` and relies on the existing `validate.Struct(&cfg)` call, which the validator enforces correctly on a non-nil `*float64` while `omitempty` skips nil. Consequence: the error text is the `validate` package's message (mentions `min_score` and `must be >= 0`), **not** `"MinScore must be >= 0"` — tests must assert on `min_score`, not on the OpenSearch wording.

**D4 — json tags differ between the two stores.** `opensearch.Config` has **no** `json` tags; `file.Config.Dir` **has** `json:"dir"`. So the external plan's `json:"min_score,omitempty"` is right for this package (it also makes `validate.Struct` report the field as `min_score`), but "mirrors opensearch exactly" is inaccurate.

**D5 — pre-existing bug: the documented `Dir` default is unreachable.** `NewStore` calls `validate.Struct(&cfg)` (store.go:41) *before* `if cfg.Dir == "" { cfg.Dir = "/tmp/eino/memory-agent" }` (store.go:44-46), while `Dir` is tagged `validate:"required"`. An empty `Dir` therefore always errors and the default branch is dead code, contradicting both the README and `CONTRIBUTING.md:151-153`. **Out of scope** for this fix (changing it would make `NewStore(Config{})` succeed and silently write to `/tmp`). Do not "fix while you're there"; keep the block exactly as it is. Recommended follow-up: separate change switching `Dir` to `validate:"omitempty"` and moving `validate.Struct` after the defaults.

**D6 — line references in the external plan.** Accurate: `store.go:126-155` / `:147` (the method is 125-155 including its doc comment); `components/agent/memory/store.go` interface (actual `MemoryStore` block is 14-25, `retriever.Retriever` at line 16). Inaccurate: `agent.go:190-203` for `buildQuery` (actual **196-213**, `maxUserMessages` at **200**); `agent.go:151-167` for `enrichInput` (actual **156-179**, `Retrieve` call at **161**); `agent.go:160-162` for the `maxMemoriesPerRetrieve` truncation (actual **169-171**). Use the code, not those numbers.

**D7 — "Maintainer relies on the list-like behavior" is wrong.** `Maintainer` uses `store.List` (`maintainer.go:109`, `:136`), and `Agent.EndSession` uses `store.List` (`agent.go:412`); `Agent.enrichInput` guards with `userQuery != ""` (`agent.go:160`). No in-repo caller depends on `Retrieve("")`. The blank-query path is preserved anyway for backward compatibility and because `TestStore_Retrieve` asserts it.

**D8 — additions not in the external plan.** (a) `_score` metadata on returned documents, mirroring `components/retriever/opensearch/retriever.go:309-310`; safe because no in-repo path re-stores documents obtained from `Retrieve` (re-storing would land `_score` in `Entry.Metadata` via `EntryFromDocument`). (b) `MinScore` copied by value onto `Store` to avoid aliasing caller memory. (c) `Entry` has no `Score` field, so scores are never persisted to `memories.jsonl`. (d) This package has **no `check.go`/`check_test.go`**, unlike the OpenSearch memory store — a pre-existing `CONTRIBUTING.md` gap, explicitly out of scope.

**D9 — the empty-query change is not a behavior change.** Today a whitespace-only query already returns *everything*, because `strings.Contains(content, "")` is true after `TrimSpace` empties `queryLower`. The new `strings.TrimSpace(query) == ""` fast path formalizes existing behavior rather than changing it.

**D10 — the scoring formula is pinned.** The external plan left the formula "an implementation detail". This plan fixes it as `matched + matched/distinctContentTerms` (distinct, post-stopword-filter content tokens) precisely because the tests assert concrete orderings; the bonus being in `(0, 1]` is what guarantees "more matched terms always wins".

**D11 — timestamp ties are common.** `Store()` assigns a single `now := time.Now()` to every document in one call (store.go:100), so entries stored together have identical `CreatedAt`/`UpdatedAt` and ties resolve to insertion index. Tests that need distinct recency must pass explicit `created_at`/`updated_at` RFC3339 metadata (as T4.13 does) rather than relying on separate `Store` calls.
