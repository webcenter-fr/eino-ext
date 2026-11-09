// Package storetest provides a reusable conformance suite for ApprovalStore
// implementations.
package storetest

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/webcenter-fr/eino-ext/server/approval/store"
)

// Factory builds a fresh store for one subtest.
type Factory func(t *testing.T) store.ApprovalStore

// Run executes the conformance suite against the store returned by factory.
func Run(t *testing.T, factory Factory) {
	t.Helper()

	t.Run("create and get", func(t *testing.T) {
		s := factory(t)
		rec := newRecord("srv", "tool", "prod")
		require.NoError(t, s.Create(context.Background(), rec))
		assert.NotEmpty(t, rec.ID)
		assert.False(t, rec.CreatedAt.IsZero())
		assert.False(t, rec.ExpiresAt.IsZero())

		got, err := s.Get(context.Background(), rec.ID)
		require.NoError(t, err)
		assert.Equal(t, rec.ID, got.ID)
		assert.Equal(t, store.StatePending, got.State)
		assert.JSONEq(t, string(rec.Args), string(got.Args))
	})

	t.Run("get missing", func(t *testing.T) {
		s := factory(t)
		_, err := s.Get(context.Background(), "missing")
		assert.ErrorIs(t, err, store.ErrNotFound)
	})

	t.Run("list pending filters and orders", func(t *testing.T) {
		s := factory(t)
		ctx := context.Background()
		first := newRecord("srv-a", "tool-a", "prod")
		first.CreatedAt = time.Now().Add(-2 * time.Minute)
		second := newRecord("srv-b", "tool-b", "dev")
		second.CreatedAt = time.Now().Add(-time.Minute)
		require.NoError(t, s.Create(ctx, first))
		require.NoError(t, s.Create(ctx, second))

		all, err := s.ListPending(ctx, store.ListFilter{})
		require.NoError(t, err)
		require.Len(t, all, 2)
		assert.Equal(t, first.ID, all[0].ID)

		filtered, err := s.ListPending(ctx, store.ListFilter{ServerName: "srv-b"})
		require.NoError(t, err)
		require.Len(t, filtered, 1)
		assert.Equal(t, second.ID, filtered[0].ID)

		limited, err := s.ListPending(ctx, store.ListFilter{Limit: 1})
		require.NoError(t, err)
		assert.Len(t, limited, 1)
	})

	t.Run("decide approve and reject", func(t *testing.T) {
		s := factory(t)
		ctx := context.Background()
		rec := newRecord("srv", "tool", "prod")
		require.NoError(t, s.Create(ctx, rec))

		decided, err := s.Decide(ctx, rec.ID, store.Decision{
			Decision: store.DecisionApprove, ApproverSub: "bob", ApproverUser: "bob", Policy: "self-approve",
		})
		require.NoError(t, err)
		assert.Equal(t, store.StateApproved, decided.State)
		assert.Equal(t, "bob", decided.ApproverSub)
		assert.NotNil(t, decided.DecidedAt)

		_, err = s.Decide(ctx, rec.ID, store.Decision{Decision: store.DecisionReject, ApproverSub: "bob"})
		assert.ErrorIs(t, err, store.ErrAlreadyDecided)

		rejected := newRecord("srv", "tool", "prod")
		require.NoError(t, s.Create(ctx, rejected))
		got, err := s.Decide(ctx, rejected.ID, store.Decision{Decision: store.DecisionReject, ApproverSub: "bob"})
		require.NoError(t, err)
		assert.Equal(t, store.StateRejected, got.State)
	})

	t.Run("invalid decision", func(t *testing.T) {
		s := factory(t)
		ctx := context.Background()
		rec := newRecord("srv", "tool", "prod")
		require.NoError(t, s.Create(ctx, rec))
		_, err := s.Decide(ctx, rec.ID, store.Decision{Decision: "maybe"})
		assert.ErrorIs(t, err, store.ErrInvalidDecision)
	})

	t.Run("ttl expiry", func(t *testing.T) {
		s := factory(t)
		ctx := context.Background()
		rec := newRecord("srv", "tool", "prod")
		rec.ExpiresAt = time.Now().Add(-time.Second)
		require.NoError(t, s.Create(ctx, rec))

		got, err := s.Get(ctx, rec.ID)
		require.NoError(t, err)
		assert.Equal(t, store.StateExpired, got.State)

		_, err = s.Decide(ctx, rec.ID, store.Decision{Decision: store.DecisionApprove, ApproverSub: "bob"})
		assert.ErrorIs(t, err, store.ErrExpired)
	})

	t.Run("wait decision wakes on decide", func(t *testing.T) {
		s := factory(t)
		ctx := context.Background()
		rec := newRecord("srv", "tool", "prod")
		require.NoError(t, s.Create(ctx, rec))

		done := make(chan struct{})
		go func() {
			defer close(done)
			time.Sleep(50 * time.Millisecond)
			_, _ = s.Decide(ctx, rec.ID, store.Decision{Decision: store.DecisionApprove, ApproverSub: "bob"})
		}()

		got, err := s.WaitDecision(ctx, rec.ID, 2*time.Second)
		require.NoError(t, err)
		assert.Equal(t, store.StateApproved, got.State)
		<-done
	})

	t.Run("wait decision times out while pending", func(t *testing.T) {
		s := factory(t)
		ctx := context.Background()
		rec := newRecord("srv", "tool", "prod")
		require.NoError(t, s.Create(ctx, rec))

		got, err := s.WaitDecision(ctx, rec.ID, 100*time.Millisecond)
		require.NoError(t, err)
		assert.Equal(t, store.StatePending, got.State)
	})

	t.Run("gc removes records past retention", func(t *testing.T) {
		s := factory(t)
		ctx := context.Background()
		rec := newRecord("srv", "tool", "prod")
		rec.ExpiresAt = time.Now().Add(-2 * time.Hour)
		require.NoError(t, s.Create(ctx, rec))

		deleted, err := s.GC(ctx, time.Now())
		require.NoError(t, err)
		assert.GreaterOrEqual(t, deleted, 1)
		_, err = s.Get(ctx, rec.ID)
		assert.ErrorIs(t, err, store.ErrNotFound)
	})
}

func newRecord(serverName, toolName, instance string) *store.Record {
	return &store.Record{
		ServerName: serverName,
		ToolName:   toolName,
		Instance:   instance,
		Args:       json.RawMessage(`{"name":"demo"}`),
		ArgsHash:   "hash",
		Requester:  store.Requester{Sub: "alice", User: "alice"},
	}
}
