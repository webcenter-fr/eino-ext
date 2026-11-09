// Package opensearch implements an ApprovalStore backed by OpenSearch through
// the shared libs/toolkit/osclient v4 client.
package opensearch

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
	"time"

	"emperror.dev/errors"
	opensearchv4 "github.com/disaster37/opensearch/v4"
	"github.com/disaster37/opensearch/v4/api"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/osclient"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
	"github.com/webcenter-fr/eino-ext/server/approval/store"
)

// Config configures the OpenSearch store.
type Config struct {
	osclient.Config
	// Index is the records index.
	Index string `validate:"required" jsonschema:"(required) OpenSearch index holding approval records"`
	// TTL is the default record TTL. Default 15m.
	TTL time.Duration `validate:"omitempty" jsonschema:"description=Default record TTL (default 15m)"`
	// Retention is how long decided/expired records are kept. Default 1h.
	Retention time.Duration `validate:"omitempty" jsonschema:"description=Retention after expiry (default 1h)"`
}

// Store is an OpenSearch-backed ApprovalStore.
type Store struct {
	client    opensearchv4.Client
	index     string
	ttl       time.Duration
	retention time.Duration
	// mu serializes Create and Decide within this process. OpenSearch has no
	// native compare-and-swap exposed by the v4 client, so without this lock two
	// concurrent Decide calls could both read StatePending and both write,
	// letting the second decision silently overwrite the first. This protects
	// the single-replica case; multi-replica deployments should add optimistic
	// concurrency (if_seq_no/if_primary_term) at the client layer.
	mu sync.Mutex
}

type storedRecord struct {
	store.Record
	ExpiresAtUnix int64 `json:"expiresAtUnix"`
	CreatedAtUnix int64 `json:"createdAtUnix"`
}

// New builds an OpenSearch store and ensures the index exists.
func New(ctx context.Context, cfg *Config) (*Store, error) {
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
	client, err := osclient.New(ctx, cfg.Config, 30*time.Second)
	if err != nil {
		return nil, err
	}
	s := &Store{client: client, index: cfg.Index, ttl: cfg.TTL, retention: cfg.Retention}
	if err := s.ensureIndex(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) ensureIndex(ctx context.Context) error {
	exists, err := s.client.Indices().Exists(ctx, []string{s.index})
	if err != nil {
		return errors.Wrap(err, "failed to check approval index")
	}
	if exists {
		return nil
	}
	mapping := map[string]any{
		"mappings": map[string]any{
			"properties": map[string]any{
				"state":         map[string]any{"type": "keyword"},
				"serverName":    map[string]any{"type": "keyword"},
				"toolName":      map[string]any{"type": "keyword"},
				"instance":      map[string]any{"type": "keyword"},
				"expiresAtUnix": map[string]any{"type": "long"},
				"createdAtUnix": map[string]any{"type": "long"},
			},
		},
	}
	if _, err := s.client.Indices().Create(ctx, s.index, mapping); err != nil {
		return errors.Wrap(err, "failed to create approval index")
	}
	return nil
}

// Create implements store.ApprovalStore.
func (s *Store) Create(ctx context.Context, rec *store.Record) error {
	if rec == nil {
		return errors.New("approval record is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := store.ApplyDefaults(rec, time.Now(), s.ttl); err != nil {
		return err
	}
	return s.indexRecord(ctx, rec)
}

// Get implements store.ApprovalStore.
func (s *Store) Get(ctx context.Context, id string) (*store.Record, error) {
	result, err := s.client.Document().Get(ctx, &api.GetRequest{Index: s.index, Id: id})
	if err != nil {
		return nil, errors.Wrap(err, "failed to read approval record")
	}
	if !result.Found {
		return nil, store.ErrNotFound
	}
	var wrapper storedRecord
	if err := json.Unmarshal(result.Source, &wrapper); err != nil {
		return nil, errors.Wrap(err, "failed to decode approval record")
	}
	rec := store.Clone(&wrapper.Record)
	if store.ExpireIfNeeded(rec, time.Now()) {
		if err := s.indexRecord(ctx, rec); err != nil {
			return nil, err
		}
	}
	return rec, nil
}

// ListPending implements store.ApprovalStore.
func (s *Store) ListPending(ctx context.Context, filter store.ListFilter) ([]*store.Record, error) {
	now := time.Now()
	must := []any{
		map[string]any{"term": map[string]any{"state": string(store.StatePending)}},
		map[string]any{"range": map[string]any{"expiresAtUnix": map[string]any{"gt": now.Unix()}}},
	}
	if filter.ServerName != "" {
		must = append(must, map[string]any{"term": map[string]any{"serverName": filter.ServerName}})
	}
	if filter.Instance != "" {
		must = append(must, map[string]any{"term": map[string]any{"instance": filter.Instance}})
	}
	if filter.ToolName != "" {
		must = append(must, map[string]any{"term": map[string]any{"toolName": filter.ToolName}})
	}
	size := 1000
	if filter.Limit > 0 && filter.Limit < size {
		size = filter.Limit
	}
	body := map[string]any{
		"size":  size,
		"query": map[string]any{"bool": map[string]any{"must": must}},
		"sort":  []any{map[string]any{"createdAtUnix": map[string]any{"order": "asc"}}},
	}
	result, err := s.client.Search().Search(ctx, &api.SearchRequest{
		Indices: []string{s.index}, Body: body, Params: &api.SearchParams{Size: &size},
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to list pending approvals")
	}
	out := make([]*store.Record, 0)
	if result.Hits != nil {
		for _, hit := range result.Hits.Hits {
			var wrapper storedRecord
			if err := json.Unmarshal(hit.Source, &wrapper); err != nil {
				continue
			}
			out = append(out, store.Clone(&wrapper.Record))
		}
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
func (s *Store) Decide(ctx context.Context, id string, d store.Decision) (*store.Record, error) {
	if d.Decision != store.DecisionApprove && d.Decision != store.DecisionReject {
		return nil, store.ErrInvalidDecision
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	if rec.State == store.StateExpired || !now.Before(rec.ExpiresAt) {
		rec.State = store.StateExpired
		_ = s.indexRecord(ctx, rec)
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
	if err := s.indexRecord(ctx, rec); err != nil {
		return nil, err
	}
	return rec, nil
}

// WaitDecision implements store.ApprovalStore.
func (s *Store) WaitDecision(ctx context.Context, id string, wait time.Duration) (*store.Record, error) {
	return store.PollDecision(ctx, s, id, wait, 250*time.Millisecond)
}

// GC implements store.ApprovalStore.
func (s *Store) GC(ctx context.Context, now time.Time) (int, error) {
	body := map[string]any{
		"size": 1000,
		"query": map[string]any{"bool": map[string]any{"must": []any{
			map[string]any{"range": map[string]any{"expiresAtUnix": map[string]any{"lte": now.Add(-s.retention).Unix()}}},
		}}},
	}
	size := 1000
	result, err := s.client.Search().Search(ctx, &api.SearchRequest{
		Indices: []string{s.index}, Body: body, Params: &api.SearchParams{Size: &size},
	})
	if err != nil {
		return 0, errors.Wrap(err, "failed to list expired approvals")
	}
	deleted := 0
	if result.Hits != nil {
		for _, hit := range result.Hits.Hits {
			if _, err := s.client.Document().Delete(ctx, &api.DeleteRequest{
				Index: s.index, Id: hit.Id, Params: &api.DeleteParams{Refresh: api.RefreshTrue},
			}); err == nil {
				deleted++
			}
		}
	}
	return deleted, nil
}

func (s *Store) indexRecord(ctx context.Context, rec *store.Record) error {
	wrapper := storedRecord{
		Record:        *store.Clone(rec),
		ExpiresAtUnix: rec.ExpiresAt.Unix(),
		CreatedAtUnix: rec.CreatedAt.Unix(),
	}
	if _, err := s.client.Document().Index(ctx, &api.IndexRequest{
		Index: s.index, Id: rec.ID, Body: wrapper, Params: &api.IndexParams{Refresh: api.RefreshTrue},
	}); err != nil {
		return errors.Wrap(err, "failed to index approval record")
	}
	return nil
}

var _ store.ApprovalStore = (*Store)(nil)
