package contentcomp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"
)

// MemoryStore is a simple, deterministic, content-addressed in-memory Store.
// It is safe for concurrent use and is the default Store for tests and small
// single-process deployments.
type MemoryStore struct {
	mu   sync.RWMutex
	data map[string]string
}

var _ Store = (*MemoryStore)(nil)

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{data: make(map[string]string)}
}

// ContentKey returns the deterministic content-addressed key for content.
func ContentKey(content string) string {
	sum := sha256.Sum256([]byte(content))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Put stores content under its content-addressed key (idempotent).
func (s *MemoryStore) Put(_ context.Context, content string) (Ref, error) {
	key := ContentKey(content)
	s.mu.Lock()
	if s.data == nil {
		s.data = make(map[string]string)
	}
	s.data[key] = content
	s.mu.Unlock()
	return Ref{Key: key, Size: len(content)}, nil
}

// Get retrieves the content stored under ref.Key.
func (s *MemoryStore) Get(_ context.Context, ref Ref) (string, error) {
	s.mu.RLock()
	v, ok := s.data[ref.Key]
	s.mu.RUnlock()
	if !ok {
		return "", &NotFoundError{Key: ref.Key}
	}
	return v, nil
}

// NotFoundError is returned when a Ref cannot be resolved by a Store.
type NotFoundError struct{ Key string }

func (e *NotFoundError) Error() string { return "contentcomp: ref not found: " + e.Key }
