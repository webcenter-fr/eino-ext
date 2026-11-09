package redis

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/webcenter-fr/eino-ext/server/approval/store"
	"github.com/webcenter-fr/eino-ext/server/approval/store/storetest"
)

func TestRedisStoreConformance(t *testing.T) {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		t.Skip("REDIS_ADDR is not set; skipping Redis integration test")
	}
	storetest.Run(t, func(t *testing.T) store.ApprovalStore {
		s, err := New(context.Background(), &Config{Addr: addr, Prefix: "approval-test:"})
		require.NoError(t, err)
		t.Cleanup(func() { _ = s.Close() })
		return s
	})
}

func TestStoredRecordArgsPreserved(t *testing.T) {
	// The decide Lua script round-trips the wrapper through cjson, which parses
	// JSON numbers as Lua doubles. A large integer in the args would be
	// corrupted (and keys reordered) if the args were stored as raw JSON inside
	// the cjson-decoded record. Storing args base64-encoded must preserve them
	// byte-for-byte.
	args := json.RawMessage(`{"big":1700000000000000000,"nested":{"a":1},"s":"x"}`)
	rec := &store.Record{
		ID: "id1", ServerName: "srv", ToolName: "tool",
		Args: args, ArgsHash: "hash", Requester: store.Requester{Sub: "alice"},
		ExpiresAt: time.Now().Add(time.Hour),
	}

	wrapper := encodeRecord(rec)
	data, err := json.Marshal(wrapper)
	require.NoError(t, err)

	// Simulate the Lua cjson round-trip: decode into a generic value (numbers
	// become float64, exactly like Lua doubles) and re-encode.
	var generic any
	require.NoError(t, json.Unmarshal(data, &generic))
	reencoded, err := json.Marshal(generic)
	require.NoError(t, err)

	var decoded storedRecord
	require.NoError(t, json.Unmarshal(reencoded, &decoded))
	got, err := decodeRecord(&decoded)
	require.NoError(t, err)
	assert.Equal(t, string(args), string(got.Args))
}
