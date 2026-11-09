// Package redis implements an ApprovalStore backed by Redis. Records use
// native key TTLs; decisions wake long-pollers through pub/sub.
package redis

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"emperror.dev/errors"
	"github.com/redis/go-redis/v9"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
	"github.com/webcenter-fr/eino-ext/server/approval/store"
)

// Config configures the Redis store.
type Config struct {
	// Addr is the Redis address (host:port).
	Addr string `validate:"required" jsonschema:"(required) Redis address (host:port)"`
	// Password is the Redis password.
	Password string `validate:"omitempty" json:"-" jsonschema:"description=Redis password"`
	// DB is the Redis database number.
	DB int `validate:"omitempty" jsonschema:"description=Redis database number"`
	// Prefix is the key prefix. Default "approval:".
	Prefix string `validate:"omitempty" jsonschema:"description=Key prefix (default 'approval:')"`
	// TTL is the default record TTL. Default 15m.
	TTL time.Duration `validate:"omitempty" jsonschema:"description=Default record TTL (default 15m)"`
	// Retention is how long decided/expired records are kept. Default 1h.
	Retention time.Duration `validate:"omitempty" jsonschema:"description=Retention after expiry (default 1h)"`
}

// Store is a Redis-backed ApprovalStore.
type Store struct {
	client    *redis.Client
	prefix    string
	ttl       time.Duration
	retention time.Duration
}

type storedRecord struct {
	Record        store.Record `json:"record"`
	ExpiresAtUnix int64        `json:"expiresAtUnix"`
	// ArgsB64 holds the raw tool arguments base64-encoded. The decide Lua
	// script round-trips the whole wrapper through cjson, which parses JSON
	// numbers as Lua doubles and would silently corrupt large integers in the
	// args (and reorder keys). Keeping args as an opaque string preserves them
	// byte-for-byte across the Lua round-trip.
	ArgsB64 string `json:"argsB64,omitempty"`
}

// encodeRecord builds the stored wrapper, moving the raw args out of the
// cjson-decoded record and into ArgsB64.
func encodeRecord(rec *store.Record) storedRecord {
	cloned := store.Clone(rec)
	args := cloned.Args
	cloned.Args = nil
	return storedRecord{
		Record:        *cloned,
		ExpiresAtUnix: rec.ExpiresAt.Unix(),
		ArgsB64:       base64.StdEncoding.EncodeToString(args),
	}
}

// decodeRecord restores the raw args from ArgsB64.
func decodeRecord(wrapper *storedRecord) (*store.Record, error) {
	rec := store.Clone(&wrapper.Record)
	if wrapper.ArgsB64 == "" {
		return rec, nil
	}
	args, err := base64.StdEncoding.DecodeString(wrapper.ArgsB64)
	if err != nil {
		return nil, errors.Wrap(err, "failed to decode approval args")
	}
	rec.Args = args
	return rec, nil
}

var decideScript = redis.NewScript(`
local raw = redis.call('GET', KEYS[1])
if not raw then return {err='notfound'} end
local wrapper = cjson.decode(raw)
local rec = wrapper.record
if rec.state ~= 'pending' then
  if rec.state == 'expired' then return {err='expired'} end
  return {err='already'}
end
if wrapper.expiresAtUnix <= tonumber(ARGV[5]) then
  rec.state = 'expired'
  wrapper.record = rec
  redis.call('SET', KEYS[1], cjson.encode(wrapper), 'KEEPTTL')
  redis.call('ZREM', KEYS[2], rec.id)
  return {err='expired'}
end
rec.state = (ARGV[1] == 'approve') and 'approved' or 'rejected'
rec.decision = ARGV[1]
rec.approverSub = ARGV[2]
rec.approverUser = ARGV[3]
rec.policy = ARGV[4]
rec.decidedAt = ARGV[6]
wrapper.record = rec
redis.call('SET', KEYS[1], cjson.encode(wrapper), 'KEEPTTL')
redis.call('ZREM', KEYS[2], rec.id)
return cjson.encode(wrapper)
`)

// New builds a Redis store and verifies connectivity.
func New(ctx context.Context, cfg *Config) (*Store, error) {
	if cfg == nil {
		cfg = &Config{}
	}
	if cfg.Prefix == "" {
		cfg.Prefix = "approval:"
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
	client := redis.NewClient(&redis.Options{Addr: cfg.Addr, Password: cfg.Password, DB: cfg.DB})
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, errors.Wrap(err, "failed to connect to Redis")
	}
	return &Store{client: client, prefix: cfg.Prefix, ttl: cfg.TTL, retention: cfg.Retention}, nil
}

// Close closes the Redis client.
func (s *Store) Close() error { return s.client.Close() }

// Create implements store.ApprovalStore.
func (s *Store) Create(ctx context.Context, rec *store.Record) error {
	if rec == nil {
		return errors.New("approval record is nil")
	}
	if err := store.ApplyDefaults(rec, time.Now(), s.ttl); err != nil {
		return err
	}
	wrapper := encodeRecord(rec)
	data, err := json.Marshal(wrapper)
	if err != nil {
		return errors.Wrap(err, "failed to encode approval record")
	}
	ttl := time.Until(rec.ExpiresAt) + s.retention
	if ttl <= 0 {
		ttl = s.retention
	}
	pipe := s.client.TxPipeline()
	pipe.Set(ctx, s.recordKey(rec.ID), data, ttl)
	pipe.ZAdd(ctx, s.pendingKey(), redis.Z{Score: float64(rec.CreatedAt.UnixNano()), Member: rec.ID})
	if _, err := pipe.Exec(ctx); err != nil {
		return errors.Wrap(err, "failed to store approval record")
	}
	return nil
}

// Get implements store.ApprovalStore.
func (s *Store) Get(ctx context.Context, id string) (*store.Record, error) {
	rec, err := s.get(ctx, id)
	if err != nil {
		return nil, err
	}
	if store.ExpireIfNeeded(rec, time.Now()) {
		if err := s.save(ctx, rec); err != nil {
			return nil, err
		}
		_ = s.client.ZRem(ctx, s.pendingKey(), id).Err()
	}
	return rec, nil
}

// ListPending implements store.ApprovalStore.
func (s *Store) ListPending(ctx context.Context, filter store.ListFilter) ([]*store.Record, error) {
	ids, err := s.client.ZRange(ctx, s.pendingKey(), 0, -1).Result()
	if err != nil {
		return nil, errors.Wrap(err, "failed to list pending approvals")
	}
	out := make([]*store.Record, 0, len(ids))
	for _, id := range ids {
		rec, err := s.Get(ctx, id)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				_ = s.client.ZRem(ctx, s.pendingKey(), id).Err()
				continue
			}
			return nil, err
		}
		if rec.State != store.StatePending {
			_ = s.client.ZRem(ctx, s.pendingKey(), id).Err()
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
func (s *Store) Decide(ctx context.Context, id string, d store.Decision) (*store.Record, error) {
	if d.Decision != store.DecisionApprove && d.Decision != store.DecisionReject {
		return nil, store.ErrInvalidDecision
	}
	now := time.Now()
	result, err := decideScript.Run(ctx, s.client,
		[]string{s.recordKey(id), s.pendingKey()},
		d.Decision, d.ApproverSub, d.ApproverUser, d.Policy, now.Unix(), now.Format(time.RFC3339Nano),
	).Text()
	if err != nil {
		message := err.Error()
		switch {
		case errors.Is(err, redis.Nil), strings.Contains(message, "notfound"):
			return nil, store.ErrNotFound
		case strings.Contains(message, "already"):
			return nil, store.ErrAlreadyDecided
		case strings.Contains(message, "expired"):
			return nil, store.ErrExpired
		default:
			return nil, errors.Wrap(err, "failed to decide approval")
		}
	}
	var wrapper storedRecord
	if err := json.Unmarshal([]byte(result), &wrapper); err != nil {
		return nil, errors.Wrap(err, "failed to decode decided approval")
	}
	rec, err := decodeRecord(&wrapper)
	if err != nil {
		return nil, err
	}
	_ = s.client.Publish(ctx, s.channel(id), string(rec.State)).Err()
	return rec, nil
}

// WaitDecision implements store.ApprovalStore.
func (s *Store) WaitDecision(ctx context.Context, id string, wait time.Duration) (*store.Record, error) {
	if wait <= 0 {
		return s.Get(ctx, id)
	}
	pubsub := s.client.Subscribe(ctx, s.channel(id))
	defer func() { _ = pubsub.Close() }()
	if _, err := pubsub.Receive(ctx); err != nil {
		return nil, errors.Wrap(err, "failed to subscribe to approval decisions")
	}
	rec, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if rec.State != store.StatePending {
		return rec, nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return s.Get(ctx, id)
	case <-pubsub.Channel():
		return s.Get(ctx, id)
	}
}

// GC implements store.ApprovalStore. Native key TTLs already remove records
// after expiry + retention; this also deletes past-retention records still
// tracked in the pending set so the conformance contract holds.
func (s *Store) GC(ctx context.Context, now time.Time) (int, error) {
	ids, err := s.client.ZRange(ctx, s.pendingKey(), 0, -1).Result()
	if err != nil {
		return 0, errors.Wrap(err, "failed to list pending approvals")
	}
	deleted := 0
	for _, id := range ids {
		rec, err := s.Get(ctx, id)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				_ = s.client.ZRem(ctx, s.pendingKey(), id).Err()
			}
			continue
		}
		if rec.State != store.StatePending {
			_ = s.client.ZRem(ctx, s.pendingKey(), id).Err()
		}
		if now.After(rec.ExpiresAt.Add(s.retention)) {
			if n, err := s.client.Del(ctx, s.recordKey(id)).Result(); err == nil && n > 0 {
				deleted++
			}
		}
	}
	return deleted, nil
}

func (s *Store) get(ctx context.Context, id string) (*store.Record, error) {
	data, err := s.client.Get(ctx, s.recordKey(id)).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, store.ErrNotFound
		}
		return nil, errors.Wrap(err, "failed to read approval record")
	}
	var wrapper storedRecord
	if err := json.Unmarshal(data, &wrapper); err != nil {
		return nil, errors.Wrap(err, "failed to decode approval record")
	}
	return decodeRecord(&wrapper)
}

func (s *Store) save(ctx context.Context, rec *store.Record) error {
	wrapper := encodeRecord(rec)
	data, err := json.Marshal(wrapper)
	if err != nil {
		return errors.Wrap(err, "failed to encode approval record")
	}
	ttl := time.Until(rec.ExpiresAt) + s.retention
	if ttl <= 0 {
		ttl = s.retention
	}
	if err := s.client.Set(ctx, s.recordKey(rec.ID), data, ttl).Err(); err != nil {
		return errors.Wrap(err, "failed to save approval record")
	}
	return nil
}

func (s *Store) recordKey(id string) string { return s.prefix + "record:" + id }
func (s *Store) pendingKey() string         { return s.prefix + "pending" }
func (s *Store) channel(id string) string   { return s.prefix + "decided:" + id }

var _ store.ApprovalStore = (*Store)(nil)
