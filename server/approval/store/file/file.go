// Package file implements an ApprovalStore backed by one JSON file per record.
// Writes use a temporary file plus atomic rename; the file mtime tracks the
// record expiry for GC.
package file

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"emperror.dev/errors"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
	"github.com/webcenter-fr/eino-ext/server/approval/store"
)

var idPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// Config configures the file store.
type Config struct {
	// Dir is the directory holding the record files.
	Dir string `validate:"required" jsonschema:"(required) Directory holding approval record files"`
	// TTL is the default record TTL. Default 15m.
	TTL time.Duration `validate:"omitempty" jsonschema:"description=Default record TTL (default 15m)"`
	// Retention is how long decided/expired records are kept. Default 1h.
	Retention time.Duration `validate:"omitempty" jsonschema:"description=Retention after expiry (default 1h)"`
}

// Store is a file-backed ApprovalStore.
type Store struct {
	dir       string
	ttl       time.Duration
	retention time.Duration
	mu        sync.Mutex
}

// New builds a file store, creating the directory when needed.
func New(_ context.Context, cfg *Config) (*Store, error) {
	if cfg == nil {
		cfg = &Config{}
	}
	if cfg.TTL <= 0 {
		cfg.TTL = store.DefaultTTL
	}
	if cfg.Retention <= 0 {
		cfg.Retention = store.DefaultRetention
	}
	if err := validate.Struct(cfg); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(cfg.Dir, 0o700); err != nil {
		return nil, errors.Wrapf(err, "failed to create approval store directory %q", cfg.Dir)
	}
	return &Store{dir: cfg.Dir, ttl: cfg.TTL, retention: cfg.Retention}, nil
}

// Create implements store.ApprovalStore.
func (s *Store) Create(_ context.Context, rec *store.Record) error {
	if rec == nil {
		return errors.New("approval record is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := store.ApplyDefaults(rec, time.Now(), s.ttl); err != nil {
		return err
	}
	if _, err := os.Stat(s.path(rec.ID)); err == nil {
		return errors.Errorf("approval record %q already exists", rec.ID)
	}
	return s.write(rec)
}

// Get implements store.ApprovalStore.
func (s *Store) Get(_ context.Context, id string) (*store.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, err := s.read(id)
	if err != nil {
		return nil, err
	}
	if store.ExpireIfNeeded(rec, time.Now()) {
		if err := s.write(rec); err != nil {
			return nil, err
		}
	}
	return rec, nil
}

// ListPending implements store.ApprovalStore.
func (s *Store) ListPending(_ context.Context, filter store.ListFilter) ([]*store.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, errors.Wrap(err, "failed to list approval records")
	}
	now := time.Now()
	out := make([]*store.Record, 0)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		rec, err := s.read(id)
		if err != nil {
			continue
		}
		if store.ExpireIfNeeded(rec, now) {
			_ = s.write(rec)
		}
		if rec.State != store.StatePending {
			continue
		}
		if filter.ServerName != "" && rec.ServerName != filter.ServerName {
			continue
		}
		if filter.Instance != "" && rec.Instance != filter.Instance {
			continue
		}
		if filter.ToolName != "" && rec.ToolName != filter.ToolName {
			continue
		}
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

// Decide implements store.ApprovalStore.
func (s *Store) Decide(_ context.Context, id string, d store.Decision) (*store.Record, error) {
	if d.Decision != store.DecisionApprove && d.Decision != store.DecisionReject {
		return nil, store.ErrInvalidDecision
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, err := s.read(id)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	if store.ExpireIfNeeded(rec, now) {
		_ = s.write(rec)
		return nil, store.ErrExpired
	}
	if rec.State == store.StateExpired {
		return nil, store.ErrExpired
	}
	if rec.State != store.StatePending {
		return nil, store.ErrAlreadyDecided
	}
	rec.State = store.StateApproved
	if d.Decision == store.DecisionReject {
		rec.State = store.StateRejected
	}
	rec.Decision = d.Decision
	rec.ApproverSub = d.ApproverSub
	rec.ApproverUser = d.ApproverUser
	rec.Policy = d.Policy
	decidedAt := now
	rec.DecidedAt = &decidedAt
	if err := s.write(rec); err != nil {
		return nil, err
	}
	return rec, nil
}

// WaitDecision implements store.ApprovalStore.
func (s *Store) WaitDecision(ctx context.Context, id string, wait time.Duration) (*store.Record, error) {
	return store.PollDecision(ctx, s, id, wait, 250*time.Millisecond)
}

// GC implements store.ApprovalStore.
func (s *Store) GC(_ context.Context, now time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return 0, errors.Wrap(err, "failed to list approval records")
	}
	deleted := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		rec, err := s.read(id)
		if err != nil {
			continue
		}
		if store.ExpireIfNeeded(rec, now) {
			_ = s.write(rec)
		}
		if now.After(rec.ExpiresAt.Add(s.retention)) {
			if err := os.Remove(s.path(id)); err == nil {
				deleted++
			}
		}
	}
	return deleted, nil
}

func (s *Store) path(id string) string {
	return filepath.Join(s.dir, id+".json")
}

func (s *Store) read(id string) (*store.Record, error) {
	if !idPattern.MatchString(id) {
		return nil, store.ErrNotFound
	}
	data, err := os.ReadFile(s.path(id))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, store.ErrNotFound
		}
		return nil, errors.Wrapf(err, "failed to read approval record %q", id)
	}
	var rec store.Record
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, errors.Wrapf(err, "failed to decode approval record %q", id)
	}
	return &rec, nil
}

func (s *Store) write(rec *store.Record) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return errors.Wrap(err, "failed to encode approval record")
	}
	tmp, err := os.CreateTemp(s.dir, ".approval-*.tmp")
	if err != nil {
		return errors.Wrap(err, "failed to create temporary approval record")
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return errors.Wrap(err, "failed to write temporary approval record")
	}
	if err := tmp.Close(); err != nil {
		return errors.Wrap(err, "failed to close temporary approval record")
	}
	if err := os.Chtimes(tmpName, rec.ExpiresAt, rec.ExpiresAt); err != nil {
		return errors.Wrap(err, "failed to set approval record mtime")
	}
	if err := os.Rename(tmpName, s.path(rec.ID)); err != nil {
		return errors.Wrap(err, "failed to atomically replace approval record")
	}
	return nil
}

var _ store.ApprovalStore = (*Store)(nil)
