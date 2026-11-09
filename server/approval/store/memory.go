package store

import (
	"context"
	"sort"
	"sync"
	"time"

	"emperror.dev/errors"
)

// MemoryStore is the default in-memory ApprovalStore. It is valid for a single
// approval-server replica; persistent backends exist for restart survival and
// HA.
type MemoryStore struct {
	mu        sync.Mutex
	records   map[string]*Record
	waiters   map[string]chan struct{}
	ttl       time.Duration
	retention time.Duration
	now       func() time.Time
}

// MemoryOption customizes a MemoryStore.
type MemoryOption func(*MemoryStore)

// WithTTL sets the default record TTL.
func WithTTL(ttl time.Duration) MemoryOption {
	return func(s *MemoryStore) {
		if ttl > 0 {
			s.ttl = ttl
		}
	}
}

// WithRetention sets how long decided/expired records are retained.
func WithRetention(retention time.Duration) MemoryOption {
	return func(s *MemoryStore) {
		if retention > 0 {
			s.retention = retention
		}
	}
}

// WithClock overrides the clock (tests).
func WithClock(now func() time.Time) MemoryOption {
	return func(s *MemoryStore) {
		if now != nil {
			s.now = now
		}
	}
}

// NewMemory builds an in-memory store.
func NewMemory(opts ...MemoryOption) *MemoryStore {
	s := &MemoryStore{
		records:   make(map[string]*Record),
		waiters:   make(map[string]chan struct{}),
		ttl:       DefaultTTL,
		retention: DefaultRetention,
		now:       time.Now,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Create implements ApprovalStore.
func (s *MemoryStore) Create(_ context.Context, rec *Record) error {
	if rec == nil {
		return errors.New("approval record is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if err := ApplyDefaults(rec, now, s.ttl); err != nil {
		return err
	}
	if _, exists := s.records[rec.ID]; exists {
		return errors.Errorf("approval record %q already exists", rec.ID)
	}
	s.records[rec.ID] = Clone(rec)
	s.waiters[rec.ID] = make(chan struct{})
	return nil
}

// Get implements ApprovalStore.
func (s *MemoryStore) Get(_ context.Context, id string) (*Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[id]
	if !ok {
		return nil, ErrNotFound
	}
	if ExpireIfNeeded(rec, s.now()) {
		s.signalLocked(id)
	}
	return Clone(rec), nil
}

// ListPending implements ApprovalStore.
func (s *MemoryStore) ListPending(_ context.Context, filter ListFilter) ([]*Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	out := make([]*Record, 0)
	for id, rec := range s.records {
		if ExpireIfNeeded(rec, now) {
			s.signalLocked(id)
		}
		if rec.State != StatePending {
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
		out = append(out, Clone(rec))
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

// Decide implements ApprovalStore.
func (s *MemoryStore) Decide(_ context.Context, id string, d Decision) (*Record, error) {
	if d.Decision != DecisionApprove && d.Decision != DecisionReject {
		return nil, ErrInvalidDecision
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[id]
	if !ok {
		return nil, ErrNotFound
	}
	now := s.now()
	if ExpireIfNeeded(rec, now) {
		s.signalLocked(id)
		return nil, ErrExpired
	}
	if rec.State == StateExpired {
		return nil, ErrExpired
	}
	if rec.State != StatePending {
		return nil, ErrAlreadyDecided
	}
	rec.State = StateApproved
	if d.Decision == DecisionReject {
		rec.State = StateRejected
	}
	rec.Decision = d.Decision
	rec.ApproverSub = d.ApproverSub
	rec.ApproverUser = d.ApproverUser
	rec.Policy = d.Policy
	decidedAt := now
	rec.DecidedAt = &decidedAt
	s.signalLocked(id)
	return Clone(rec), nil
}

// WaitDecision implements ApprovalStore.
func (s *MemoryStore) WaitDecision(ctx context.Context, id string, wait time.Duration) (*Record, error) {
	s.mu.Lock()
	rec, ok := s.records[id]
	if !ok {
		s.mu.Unlock()
		return nil, ErrNotFound
	}
	if ExpireIfNeeded(rec, s.now()) {
		s.signalLocked(id)
	}
	if rec.State != StatePending || wait <= 0 {
		out := Clone(rec)
		s.mu.Unlock()
		return out, nil
	}
	waiter := s.waiters[id]
	expiresAt := rec.ExpiresAt
	s.mu.Unlock()

	timer := time.NewTimer(wait)
	defer timer.Stop()
	var expiry <-chan time.Time
	if !expiresAt.IsZero() {
		untilExpiry := time.Until(expiresAt)
		if untilExpiry <= 0 {
			return s.Get(ctx, id)
		}
		expiryTimer := time.NewTimer(untilExpiry)
		defer expiryTimer.Stop()
		expiry = expiryTimer.C
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-waiter:
	case <-timer.C:
	case <-expiry:
	}
	return s.Get(ctx, id)
}

// GC implements ApprovalStore.
func (s *MemoryStore) GC(_ context.Context, now time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	deleted := 0
	for id, rec := range s.records {
		if ExpireIfNeeded(rec, now) {
			s.signalLocked(id)
		}
		if now.After(rec.ExpiresAt.Add(s.retention)) {
			delete(s.records, id)
			delete(s.waiters, id)
			deleted++
		}
	}
	return deleted, nil
}

func (s *MemoryStore) signalLocked(id string) {
	if ch, ok := s.waiters[id]; ok {
		select {
		case <-ch:
		default:
			close(ch)
		}
	}
}

var _ ApprovalStore = (*MemoryStore)(nil)
