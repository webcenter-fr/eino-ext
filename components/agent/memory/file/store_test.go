package file

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	memoryagent "github.com/webcenter-fr/eino-ext/components/agent/memory"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewStore(Config{Dir: t.TempDir()})
	require.NoError(t, err)
	return s
}

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

func TestStore_CRUD(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	ids, err := s.Store(ctx, []*schema.Document{
		{Content: "entry 1", MetaData: map[string]any{"category": "fact"}},
		{Content: "entry 2", MetaData: map[string]any{"category": "preference"}},
	})
	require.NoError(t, err)
	require.Len(t, ids, 2)

	count, err := s.Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, count)

	err = s.Delete(ctx, ids[0])
	require.NoError(t, err)

	count, err = s.Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	docs, err := s.List(ctx, 0, 10)
	require.NoError(t, err)
	assert.Len(t, docs, 1)
	assert.Equal(t, "entry 2", docs[0].Content)
}

func TestStore_Retrieve(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	_, err := s.Store(ctx, []*schema.Document{
		{Content: "user likes Go programming language"},
		{Content: "project uses PostgreSQL database"},
	})
	require.NoError(t, err)

	docs, err := s.Retrieve(ctx, "Go")
	require.NoError(t, err)
	require.Len(t, docs, 1)
	assert.Contains(t, docs[0].Content, "Go")

	docs, err = s.Retrieve(ctx, "PostgreSQL")
	require.NoError(t, err)
	require.Len(t, docs, 1)
	assert.Contains(t, docs[0].Content, "PostgreSQL")

	docs, err = s.Retrieve(ctx, "Ruby")
	require.NoError(t, err)
	assert.Empty(t, docs)

	docs, err = s.Retrieve(ctx, "")
	require.NoError(t, err)
	assert.Len(t, docs, 2)
}

func TestStore_DeleteNonExistent(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	err := s.Delete(ctx, "nonexistent")
	require.NoError(t, err)
}

func TestStore_DeleteByFilter(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	_, err := s.Store(ctx, []*schema.Document{
		{Content: "fact 1", MetaData: map[string]any{"category": "fact"}},
		{Content: "fact 2", MetaData: map[string]any{"category": "fact"}},
		{Content: "pref 1", MetaData: map[string]any{"category": "preference"}},
	})
	require.NoError(t, err)

	deleted, err := s.DeleteByFilter(ctx, map[string]any{"category": "fact"})
	require.NoError(t, err)
	assert.Equal(t, 2, deleted)

	docs, err := s.List(ctx, 0, 10)
	require.NoError(t, err)
	require.Len(t, docs, 1)
	assert.Equal(t, "pref 1", docs[0].Content)
}

func TestStore_Persistence(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	s1, err := NewStore(Config{Dir: dir})
	require.NoError(t, err)

	_, err = s1.Store(ctx, []*schema.Document{
		{Content: "persistent entry", MetaData: map[string]any{"category": "fact"}},
	})
	require.NoError(t, err)

	s2, err := NewStore(Config{Dir: dir})
	require.NoError(t, err)

	docs, err := s2.List(ctx, 0, 10)
	require.NoError(t, err)
	assert.Len(t, docs, 1)
	assert.Equal(t, "persistent entry", docs[0].Content)
}

func TestStore_ListPagination(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	for i := 0; i < 10; i++ {
		_, err := s.Store(ctx, []*schema.Document{{Content: "entry"}})
		require.NoError(t, err)
	}

	docs, err := s.List(ctx, 0, 3)
	require.NoError(t, err)
	assert.Len(t, docs, 3)

	docs2, err := s.List(ctx, 5, 5)
	require.NoError(t, err)
	assert.Len(t, docs2, 5)
}

func TestStore_Concurrency(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	done := make(chan struct{})
	for range 5 {
		go func() {
			for range 20 {
				_, _ = s.Store(ctx, []*schema.Document{{Content: "concurrent"}})
				_, _ = s.List(ctx, 0, 100)
				_, _ = s.Retrieve(ctx, "concurrent")
			}
			done <- struct{}{}
		}()
	}

	for range 5 {
		<-done
	}

	count, err := s.Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 100, count)
}

func TestStore_EmptyStore(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	docs, err := s.Retrieve(ctx, "query")
	require.NoError(t, err)
	assert.Empty(t, docs)

	count, err := s.Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}

func TestStore_IDGeneration(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	ids, err := s.Store(ctx, []*schema.Document{
		{Content: "no id"},
		{ID: "custom-id", Content: "has id"},
	})
	require.NoError(t, err)
	require.Len(t, ids, 2)
	assert.NotEmpty(t, ids[0])
	assert.NotEqual(t, ids[1], ids[0])
	assert.Equal(t, "custom-id", ids[1])
}

func TestMatchesFilter_Metadata(t *testing.T) {
	entry := &memoryagent.Entry{
		Content:  "test",
		Metadata: map[string]any{"confidence": "high"},
	}
	assert.True(t, matchesFilter(entry, map[string]any{"confidence": "high"}))
	assert.False(t, matchesFilter(entry, map[string]any{"confidence": "low"}))
}

func TestMatchesFilter_NonStringValue(t *testing.T) {
	entry := &memoryagent.Entry{Category: "fact"}
	assert.False(t, matchesFilter(entry, map[string]any{"category": 42}))
}

func TestRetrieve_WithTopK(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	_, err := s.Store(ctx, []*schema.Document{
		{Content: "Go programming"},
		{Content: "Go testing"},
		{Content: "Go deployment"},
	})
	require.NoError(t, err)

	docs, err := s.Retrieve(ctx, "Go", retriever.WithTopK(2))
	require.NoError(t, err)
	assert.Len(t, docs, 2)
}

func TestStore_ImplementsMemoryStore(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	var store memoryagent.MemoryStore = s
	_, err := store.Store(ctx, []*schema.Document{{Content: "test"}})
	require.NoError(t, err)
}

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

func TestRetrieve_NoOverlapReturnsNothing(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	storeContents(t, s, "The user works with kafka.")

	docs, err := s.Retrieve(ctx, "quantum chromodynamics lecture notes")
	require.NoError(t, err)
	assert.Empty(t, docs)
}

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

func TestRetrieve_IdentifiersSurviveTokenization(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	storeContents(t, s, "The user's cluster is ran37hpd2 in region eu-west-3.")

	docs, err := s.Retrieve(ctx, "what is the status of cluster ran37hpd2")
	require.NoError(t, err)
	require.Len(t, docs, 1)
	assert.Contains(t, docs[0].Content, "ran37hpd2")
}

func TestNewStore_MinScoreNegative(t *testing.T) {
	ms := -1.0
	s, err := NewStore(Config{Dir: t.TempDir(), MinScore: &ms})
	require.Error(t, err)
	assert.Nil(t, s)
	assert.Contains(t, err.Error(), "min_score")
}

func TestNewStore_MinScoreZeroAllowed(t *testing.T) {
	ms := 0.0
	s, err := NewStore(Config{Dir: t.TempDir(), MinScore: &ms})
	require.NoError(t, err)
	require.NotNil(t, s)
}

func TestTokenize(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{name: "empty", input: "", expected: nil},
		{name: "stopwords only", input: "the a of is it", expected: nil},
		{name: "single word", input: "Go", expected: []string{"go"}},
		{name: "hyphenated", input: "logcentralizer-rec namespace", expected: []string{"logcentralizer", "rec", "namespace"}},
		{name: "identifiers", input: "cluster ran37hpd2 eu-west-3", expected: []string{"cluster", "ran37hpd2", "eu", "west"}},
		{name: "accents", input: "Préférences de l'utilisateur", expected: []string{"préférences", "utilisateur"}},
		{name: "punctuation", input: "???!!!", expected: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tokenize(tt.input))
		})
	}
}

func TestScoreEntry(t *testing.T) {
	assert.Equal(t, 0.0, scoreEntry(nil, "x"))
	assert.Equal(t, 0.0, scoreEntry(map[string]struct{}{"kafka": {}}, "postgresql"))
	assert.Greater(t,
		scoreEntry(map[string]struct{}{"kafka": {}, "namespace": {}}, "kafka namespace"),
		scoreEntry(map[string]struct{}{"kafka": {}, "namespace": {}}, "kafka"),
	)
	// Duplicate query terms must count once: a set built from ["kafka","kafka"]
	// is identical to one built from ["kafka"].
	dupTerms := make(map[string]struct{}, 2)
	for _, term := range []string{"kafka", "kafka"} {
		dupTerms[term] = struct{}{}
	}
	assert.Equal(t,
		scoreEntry(dupTerms, "kafka broker"),
		scoreEntry(map[string]struct{}{"kafka": {}}, "kafka broker"),
	)
}

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

// TestNewStore_MinScoreSpecialFloats pins the validation behavior of
// non-finite MinScore values. NaN must be rejected by the gte=0 tag (NaN >= 0
// is false), because a NaN threshold would make every "score < minScore"
// comparison false at runtime and silently disable the filter (CWE-682).
// +Inf passes validation but fails closed at runtime: every finite score is
// below it, so Retrieve returns nothing rather than everything.
func TestNewStore_MinScoreSpecialFloats(t *testing.T) {
	t.Run("NaN is rejected", func(t *testing.T) {
		ms := math.NaN()
		s, err := NewStore(Config{Dir: t.TempDir(), MinScore: &ms})
		require.Error(t, err)
		assert.Nil(t, s)
		assert.Contains(t, err.Error(), "min_score")
	})

	t.Run("-Inf is rejected", func(t *testing.T) {
		ms := math.Inf(-1)
		s, err := NewStore(Config{Dir: t.TempDir(), MinScore: &ms})
		require.Error(t, err)
		assert.Nil(t, s)
	})

	t.Run("+Inf is allowed but filters everything (fail closed)", func(t *testing.T) {
		ms := math.Inf(1)
		s, err := NewStore(Config{Dir: t.TempDir(), MinScore: &ms})
		require.NoError(t, err)

		storeContents(t, s, "The user works with kafka.")
		docs, err := s.Retrieve(context.Background(), "kafka")
		require.NoError(t, err)
		assert.Empty(t, docs, "an unreachable threshold must return nothing, not everything")
	})
}

// TestNewStore_MinScoreNotAliased proves NewStore copies the MinScore value:
// mutating the caller's float64 after construction must not affect the store
// (no data race or threshold change through shared memory, CWE-362).
func TestNewStore_MinScoreNotAliased(t *testing.T) {
	ctx := context.Background()
	ms := 0.0 // no threshold
	s, err := NewStore(Config{Dir: t.TempDir(), MinScore: &ms})
	require.NoError(t, err)
	storeContents(t, s, "The user works with kafka.")

	// Raise the caller-owned variable after construction: the store must keep
	// its own copy and still return the match.
	ms = 1000.0
	docs, err := s.Retrieve(ctx, "kafka")
	require.NoError(t, err)
	require.Len(t, docs, 1)
	assert.Contains(t, docs[0].Content, "kafka")
}

// TestRetrieve_LargeQueryDuplicateTerms exercises an attacker-shaped oversized
// query (Retrieve is a public API; the agent's MaxQueryChars defaults to 0,
// i.e. unbounded). Duplicate terms must neither change the result nor multiply
// the per-entry work: the query term set is deduplicated once per call, so
// cost stays proportional to the query length plus the store's total content,
// not entries x query tokens (CWE-400).
func TestRetrieve_LargeQueryDuplicateTerms(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	storeContents(t, s, "The user works with the kafka cluster daily.", "The project uses PostgreSQL.")

	query := strings.Repeat("kafka ", 100_000) + "postgresql"
	docs, err := s.Retrieve(ctx, query)
	require.NoError(t, err)
	require.Len(t, docs, 2)
	// Both entries match exactly one distinct term; the shorter content wins
	// on the coverage bonus (1 + 1/3 > 1 + 1/5).
	assert.Contains(t, docs[0].Content, "PostgreSQL")
	assert.Contains(t, docs[1].Content, "kafka")
}
