package memory

import (
	"context"
	_ "embed"
	"strings"
	"sync"
	"time"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
)

// DedupSystemPrompt is the system prompt for memory deduplication via LLM.
//
//go:embed prompts/dedup_system.md
var dedupSystemPrompt string

type MaintainerConfig struct {
	Store                 MemoryStore       `json:"store" jsonschema:"-"`
	Interval              time.Duration     `json:"interval" jsonschema:"-"`
	MaxCompactionSimilarity float64         `json:"max_compaction_similarity" jsonschema:"description=Jaccard similarity threshold for merging,default=0.8"`
	MaxAge                time.Duration     `json:"max_age" jsonschema:"description=Max age before cleanup, 0 disables"`
	Model                 model.BaseChatModel `json:"-" jsonschema:"-"`
}

type MemoryMaintainer struct {
	store    MemoryStore
	interval time.Duration

	maxCompactionSimilarity float64
	maxAge                 time.Duration
	model                  model.BaseChatModel

	stopCh chan struct{}
	wg     sync.WaitGroup
	mu     sync.Mutex
}

func NewMemoryMaintainer(cfg MaintainerConfig) *MemoryMaintainer {
	if cfg.Interval <= 0 {
		cfg.Interval = time.Hour
	}
	if cfg.MaxCompactionSimilarity <= 0 {
		cfg.MaxCompactionSimilarity = 0.8
	}

	return &MemoryMaintainer{
		store:                   cfg.Store,
		interval:                cfg.Interval,
		maxCompactionSimilarity: cfg.MaxCompactionSimilarity,
		maxAge:                  cfg.MaxAge,
		model:                   cfg.Model,
		stopCh:                  make(chan struct{}),
	}
}

func (m *MemoryMaintainer) Start(ctx context.Context) {
	m.wg.Add(1)
	go m.loop(ctx)
}

func (m *MemoryMaintainer) Stop() {
	close(m.stopCh)
	m.wg.Wait()
}

func (m *MemoryMaintainer) TriggerFullPass(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.compact(ctx); err != nil {
		return errors.Wrap(err, "compaction phase")
	}
	if err := m.cleanup(ctx); err != nil {
		return errors.Wrap(err, "cleanup phase")
	}
	return nil
}

func (m *MemoryMaintainer) loop(ctx context.Context) {
	defer m.wg.Done()

	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()

	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			_ = m.TriggerFullPass(ctx)
		}
	}
}

func (m *MemoryMaintainer) compact(ctx context.Context) error {
	docs, err := m.store.List(ctx, 0, 0)
	if err != nil {
		return errors.Wrap(err, "list entries for compaction")
	}
	if len(docs) < 2 {
		return nil
	}

	groups := groupBySimilarity(docs, m.maxCompactionSimilarity)

	for _, group := range groups {
		if len(group) < 2 {
			continue
		}
		if err := m.mergeGroup(ctx, group); err != nil {
			return errors.Wrap(err, "merge group")
		}
	}

	return nil
}

func (m *MemoryMaintainer) cleanup(ctx context.Context) error {
	if m.maxAge <= 0 {
		return nil
	}

	docs, err := m.store.List(ctx, 0, 0)
	if err != nil {
		return errors.Wrap(err, "list entries for cleanup")
	}

	var staleIDs []string
	for _, doc := range docs {
		entry := EntryFromDocument(doc)
		if time.Since(entry.CreatedAt) > m.maxAge {
			staleIDs = append(staleIDs, doc.ID)
		}
	}

	for _, id := range staleIDs {
		_ = m.store.Delete(ctx, id)
	}
	return nil
}

// mergeGroup merges a group of similar documents into a single consolidated entry.
// It stores the merged document first, then deletes the originals to prevent
// data loss on Store failure.
func (m *MemoryMaintainer) mergeGroup(ctx context.Context, docs []*schema.Document) error {
	if len(docs) < 2 {
		return nil
	}

	var contents []string
	for _, d := range docs {
		contents = append(contents, d.Content)
	}
	mergedContent := strings.Join(contents, "; ")

	if m.model != nil {
		result, err := m.model.Generate(ctx, []*schema.Message{
			schema.SystemMessage(dedupSystemPrompt),
			schema.UserMessage(strings.Join(contents, "\n---\n")),
		})
		if err == nil && result != nil {
			mergedContent = strings.TrimSpace(result.Content)
		} else if err != nil {
			logrus.WithError(err).Debug("LLM dedup failed, falling back to text concatenation")
		}
	}

	primary := docs[0]
	merged := &schema.Document{
		ID:       uuid.New().String(),
		Content:  mergedContent,
		MetaData: make(map[string]any),
	}
	for k, v := range primary.MetaData {
		merged.MetaData[k] = v
	}
	merged.MetaData["merged_from"] = collectIDs(docs)
	merged.MetaData["updated_at"] = time.Now().Format(time.RFC3339)

	if _, err := m.store.Store(ctx, []*schema.Document{merged}); err != nil {
		return errors.Wrap(err, "store merged entry")
	}

	for _, d := range docs {
		_ = m.store.Delete(ctx, d.ID)
	}

	return nil
}

func collectIDs(docs []*schema.Document) []string {
	ids := make([]string, len(docs))
	for i, d := range docs {
		ids[i] = d.ID
	}
	return ids
}

func groupBySimilarity(docs []*schema.Document, threshold float64) [][]*schema.Document {
	byCategory := make(map[string][]*schema.Document)
	for _, d := range docs {
		cat := ""
		if d.MetaData != nil {
			if v, ok := d.MetaData["category"].(string); ok {
				cat = v
			}
		}
		byCategory[cat] = append(byCategory[cat], d)
	}

	var groups [][]*schema.Document
	for _, catDocs := range byCategory {
		groups = append(groups, clusterByTextOverlap(catDocs, threshold)...)
	}
	return groups
}

func clusterByTextOverlap(docs []*schema.Document, threshold float64) [][]*schema.Document {
	if len(docs) <= 1 {
		return [][]*schema.Document{docs}
	}

	used := make(map[int]bool)
	var clusters [][]*schema.Document

	for i := range docs {
		if used[i] {
			continue
		}
		cluster := []*schema.Document{docs[i]}
		used[i] = true

		for j := i + 1; j < len(docs); j++ {
			if used[j] {
				continue
			}
			sim := textSimilarity(docs[i].Content, docs[j].Content)
			if sim >= threshold {
				cluster = append(cluster, docs[j])
				used[j] = true
			}
		}
		clusters = append(clusters, cluster)
	}

	return clusters
}

func textSimilarity(a, b string) float64 {
	wordsA := tokenize(a)
	wordsB := tokenize(b)
	if len(wordsA) == 0 || len(wordsB) == 0 {
		return 0
	}

	intersection := 0
	setA := make(map[string]struct{}, len(wordsA))
	for _, w := range wordsA {
		setA[w] = struct{}{}
	}
	for _, w := range wordsB {
		if _, ok := setA[w]; ok {
			intersection++
		}
	}

	union := len(setA)
	setB := make(map[string]struct{}, len(wordsB))
	for _, w := range wordsB {
		setB[w] = struct{}{}
	}
	for w := range setB {
		if _, ok := setA[w]; !ok {
			union++
		}
	}

	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

func tokenize(s string) []string {
	s = strings.ToLower(s)
	var words []string
	current := strings.Builder{}
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			current.WriteRune(r)
		} else {
			if current.Len() > 0 {
				words = append(words, current.String())
				current.Reset()
			}
		}
	}
	if current.Len() > 0 {
		words = append(words, current.String())
	}
	return words
}
