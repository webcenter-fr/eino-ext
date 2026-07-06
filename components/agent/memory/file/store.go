// Package file provides a simple JSONL-backed MemoryStore implementation.
package file

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

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

type Config struct {
	Dir string `json:"dir" jsonschema:"description=Directory for the memories.jsonl file,default=/tmp/eino/memory-agent"`
}

type Store struct {
	mu      sync.RWMutex
	dir     string
	entries map[string]*memoryagent.MemoryEntry
	order   []string
}

func NewStore(cfg Config) (*Store, error) {
	if err := validate.Struct(&cfg); err != nil {
		return nil, errors.Wrap(err, "invalid file store config")
	}
	if cfg.Dir == "" {
		cfg.Dir = "/tmp/eino/memory-agent"
	}
	if err := os.MkdirAll(cfg.Dir, 0755); err != nil {
		return nil, errors.Wrap(err, "create memory dir")
	}
	s := &Store{
		dir:     cfg.Dir,
		entries: make(map[string]*memoryagent.MemoryEntry),
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

	f, err := os.OpenFile(s.filePath(), os.O_RDONLY|os.O_CREATE, 0644)
	if err != nil {
		return errors.Wrap(err, "open memories file")
	}
	defer f.Close()

	dec := json.NewDecoder(f)
	line := 0
	for dec.More() {
		line++
		var entry memoryagent.MemoryEntry
		if err := dec.Decode(&entry); err != nil {
			logrus.WithError(err).WithField("line", line).Warn("skipping corrupted memory entry")
			continue
		}
		s.entries[entry.ID] = &entry
		s.order = append(s.order, entry.ID)
	}
	return nil
}

func (s *Store) Store(_ context.Context, docs []*schema.Document, _ ...indexer.Option) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ids := make([]string, 0, len(docs))
	f, err := os.OpenFile(s.filePath(), os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		return nil, errors.Wrap(err, "open memories file for append")
	}
	defer f.Close()

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
		if topK > 0 && len(docs) >= topK {
			break
		}
	}
	return docs, nil
}

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
			f.Close()
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

func matchesFilter(entry *memoryagent.MemoryEntry, filter map[string]any) bool {
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
