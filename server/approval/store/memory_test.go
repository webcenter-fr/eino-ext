package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/webcenter-fr/eino-ext/server/approval/store"
	"github.com/webcenter-fr/eino-ext/server/approval/store/storetest"
)

func TestMemoryStoreConformance(t *testing.T) {
	storetest.Run(t, func(t *testing.T) store.ApprovalStore {
		return store.NewMemory()
	})
}

func TestMemoryStoreExpirySignalsWaiter(t *testing.T) {
	now := time.Now()
	s := store.NewMemory(store.WithClock(func() time.Time { return now }))
	ctx := context.Background()
	rec := &store.Record{
		ServerName: "srv",
		ToolName:   "tool",
		Args:       []byte(`{}`),
		Requester:  store.Requester{Sub: "alice"},
		ExpiresAt:  now.Add(50 * time.Millisecond),
	}
	require.NoError(t, s.Create(ctx, rec))

	done := make(chan *store.Record, 1)
	go func() {
		got, _ := s.WaitDecision(ctx, rec.ID, time.Second)
		done <- got
	}()

	now = now.Add(time.Second)
	select {
	case got := <-done:
		require.NotNil(t, got)
		assert.Equal(t, store.StateExpired, got.State)
	case <-time.After(2 * time.Second):
		t.Fatal("waiter was not woken by expiry")
	}
}

func TestMemoryStoreCloneIsolation(t *testing.T) {
	s := store.NewMemory()
	ctx := context.Background()
	rec := &store.Record{
		ServerName: "srv",
		ToolName:   "tool",
		Args:       []byte(`{"a":1}`),
		Requester:  store.Requester{Sub: "alice", Groups: []string{"devs"}},
	}
	require.NoError(t, s.Create(ctx, rec))
	rec.Args[0] = 'X'
	rec.Requester.Groups[0] = "changed"

	got, err := s.Get(ctx, rec.ID)
	require.NoError(t, err)
	assert.JSONEq(t, `{"a":1}`, string(got.Args))
	assert.Equal(t, []string{"devs"}, got.Requester.Groups)
}
