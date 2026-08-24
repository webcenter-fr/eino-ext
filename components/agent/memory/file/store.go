// Package file provides a simple JSONL-backed MemoryStore implementation.
package file

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
	"unicode/utf8"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/indexer"
	"github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"

	memoryagent "github.com/webcenter-fr/eino-ext/components/agent/memory"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
)

var _ memoryagent.MemoryStore = (*Store)(nil)

// metaKeyScore is the metadata key carrying the lexical relevance score of a
// retrieved memory. It mirrors the "_score" key that
// components/retriever/opensearch sets on OpenSearch hits, so both memory
// backends expose the score under the same name.
const metaKeyScore = "_score"

// Config holds configuration for the JSONL-backed memory store.
type Config struct {
	Dir string `json:"dir" validate:"required" jsonschema:"description=Directory for the memories.jsonl file,default=/tmp/eino/memory-agent"`

	// MinScore, when non-nil, drops retrieved memories whose lexical relevance
	// score is below this threshold (score >= MinScore is kept). nil means "no
	// threshold". A value of 0 behaves like nil, because non-matching entries
	// are always dropped. Mirrors agent/memory/opensearch.Config.MinScore.
	MinScore *float64 `json:"min_score,omitempty" validate:"omitempty,gte=0" jsonschema:"description=Optional minimum relevance score for retrieved memories"`
}

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

// NewStore creates a new JSONL-backed Store from the given configuration.
func NewStore(cfg Config) (*Store, error) {
	if err := validate.Struct(&cfg); err != nil {
		return nil, errors.Wrap(err, "invalid file store config")
	}
	if cfg.Dir == "" {
		cfg.Dir = "/tmp/eino/memory-agent"
	}
	if err := os.MkdirAll(cfg.Dir, 0750); err != nil {
		return nil, errors.Wrap(err, "create memory dir")
	}
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

func (s *Store) filePath() string { return filepath.Join(s.dir, "memories.jsonl") }

func (s *Store) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := os.OpenFile(s.filePath(), os.O_RDONLY|os.O_CREATE, 0640)
	if err != nil {
		return errors.Wrap(err, "open memories file")
	}
	defer func() { _ = f.Close() }()

	dec := json.NewDecoder(f)
	line := 0
	for dec.More() {
		line++
		var entry memoryagent.Entry
		if err := dec.Decode(&entry); err != nil {
			logrus.WithError(err).WithField("line", line).Warn("skipping corrupted memory entry")
			continue
		}
		s.entries[entry.ID] = &entry
		s.order = append(s.order, entry.ID)
	}
	return nil
}

// Store saves documents to the JSONL file and in-memory index.
func (s *Store) Store(_ context.Context, docs []*schema.Document, _ ...indexer.Option) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ids := make([]string, 0, len(docs))
	f, err := os.OpenFile(s.filePath(), os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0640)
	if err != nil {
		return nil, errors.Wrap(err, "open memories file for append")
	}
	defer func() { _ = f.Close() }()

	enc := json.NewEncoder(f)
	now := time.Now()

	for _, doc := range docs {
		entry := memoryagent.EntryFromDocument(doc)
		if entry.ID == "" {
			entry.ID = uuid.New().String()
		}
		if entry.CreatedAt.IsZero() {
			entry.CreatedAt = now
		}
		if entry.UpdatedAt.IsZero() {
			entry.UpdatedAt = now
		}
		doc.ID = entry.ID

		if err := enc.Encode(entry); err != nil {
			return ids, errors.Wrap(err, "encode memory entry")
		}
		s.entries[entry.ID] = entry
		s.order = append(s.order, entry.ID)
		ids = append(ids, entry.ID)
	}
	return ids, nil
}

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

	// Deduplicate the query terms once, up front. scoreEntry runs once per
	// entry; rebuilding a term set sized len(queryTerms) inside it would cost
	// O(entries x queryTerms) time and allocations for a single call — a CPU
	// and memory amplification vector when an oversized query (Retrieve is a
	// public API and the agent's MaxQueryChars defaults to 0/unbounded) hits a
	// large store (CWE-400). Building the set here keeps the per-entry cost
	// proportional to the entry's own content, matching the complexity of the
	// previous substring scan.
	querySet := make(map[string]struct{}, len(queryTerms))
	for _, term := range queryTerms {
		querySet[term] = struct{}{}
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
		score := scoreEntry(querySet, entry.Content)
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

// isStopword reports whether w is a known stopword.
func isStopword(w string) bool {
	_, ok := stopwords[w]
	return ok
}

// tokenize lowercases s, splits it on every rune that is neither a letter nor a
// digit, then drops single-rune tokens and stopwords. Digits are kept so
// identifiers such as "ran37hpd2" survive, and unicode.IsLetter keeps accented
// letters ("préférences") intact. Returns nil when nothing meaningful remains.
func tokenize(s string) []string {
	s = strings.ToLower(s)
	words := strings.FieldsFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	var result []string
	for _, w := range words {
		// utf8.RuneCountInString counts runes without allocating, unlike
		// len([]rune(w)).
		if utf8.RuneCountInString(w) >= 2 && !isStopword(w) {
			result = append(result, w)
		}
	}
	return result
}

// scoreEntry returns the lexical relevance of content for the given query terms:
//
//	score = matched + matched/distinctContentTerms
//
// where matched is the number of distinct query terms present in the tokenized
// content, i.e. the size of the intersection between queryTerms and the
// content's distinct tokens. The second term is a coverage bonus in (0, 1] that
// favors short, focused memories over long ones matching the same number of
// terms. Because the bonus is always > 0 and <= 1, an entry matching n+1 terms
// always outranks one matching n terms (max score for n is exactly n+1, min
// score for n+1 is strictly greater than n+1). Returns 0 when nothing matches,
// so callers can treat 0 as "not relevant".
//
// The caller must pass the query terms as a pre-deduplicated set built once per
// query (see Retrieve): the per-entry cost of this function is then proportional
// to the content's own token count only, never to the query length.
func scoreEntry(queryTerms map[string]struct{}, content string) float64 {
	contentTokens := tokenize(content)
	if len(queryTerms) == 0 || len(contentTokens) == 0 {
		return 0
	}

	contentSet := make(map[string]struct{}, len(contentTokens))
	for _, token := range contentTokens {
		contentSet[token] = struct{}{}
	}

	// matched = |contentSet ∩ queryTerms|: identical to iterating the distinct
	// query terms and counting those present in contentSet, but bounded by the
	// content's distinct-term count instead of the query's.
	matched := 0
	for token := range contentSet {
		if _, ok := queryTerms[token]; ok {
			matched++
		}
	}
	if matched == 0 {
		return 0
	}

	return float64(matched) + float64(matched)/float64(len(contentSet))
}

// Delete removes a document from the store by ID.
func (s *Store) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.entries[id]; !ok {
		return nil
	}
	delete(s.entries, id)
	s.order = removeFromOrder(s.order, id)
	return s.rewriteLocked()
}

// DeleteByFilter removes documents matching the given metadata filter.
func (s *Store) DeleteByFilter(_ context.Context, filter map[string]any) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	deleted := 0
	for id, entry := range s.entries {
		if matchesFilter(entry, filter) {
			delete(s.entries, id)
			s.order = removeFromOrder(s.order, id)
			deleted++
		}
	}
	if deleted > 0 {
		if err := s.rewriteLocked(); err != nil {
			return deleted, err
		}
	}
	return deleted, nil
}

// List returns all documents with pagination support.
func (s *Store) List(_ context.Context, offset, limit int) ([]*schema.Document, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = len(s.order)
	}

	var docs []*schema.Document
	for i := offset; i < len(s.order) && i < offset+limit; i++ {
		id := s.order[i]
		entry, ok := s.entries[id]
		if !ok {
			continue
		}
		docs = append(docs, entry.ToDocument())
	}
	return docs, nil
}

// Count returns the total number of stored documents.
func (s *Store) Count(_ context.Context) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.entries), nil
}

func (s *Store) rewriteLocked() error {
	tmp := s.filePath() + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return errors.Wrap(err, "create temp file")
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmp)
		}
	}()

	enc := json.NewEncoder(f)
	newOrder := make([]string, 0, len(s.order))
	for _, id := range s.order {
		entry, ok := s.entries[id]
		if !ok {
			continue
		}
		if err := enc.Encode(entry); err != nil {
			_ = f.Close()
			return errors.Wrap(err, "encode entry on rewrite")
		}
		newOrder = append(newOrder, id)
	}
	s.order = newOrder

	if err := f.Close(); err != nil {
		return errors.Wrap(err, "close temp file")
	}
	if err := os.Rename(tmp, s.filePath()); err != nil {
		return errors.Wrap(err, "rename temp file")
	}
	cleanup = false
	return nil
}

func matchesFilter(entry *memoryagent.Entry, filter map[string]any) bool {
	for k, v := range filter {
		fv, ok := v.(string)
		if !ok {
			return false
		}
		switch k {
		case "category":
			if entry.Category != fv {
				return false
			}
		case "source":
			if entry.Source != fv {
				return false
			}
		case "session_id":
			if entry.SessionID != fv {
				return false
			}
		default:
			if mv, exists := entry.Metadata[k]; !exists || mv != fv {
				return false
			}
		}
	}
	return true
}

func removeFromOrder(order []string, id string) []string {
	result := make([]string, 0, len(order))
	for _, v := range order {
		if v != id {
			result = append(result, v)
		}
	}
	return result
}
