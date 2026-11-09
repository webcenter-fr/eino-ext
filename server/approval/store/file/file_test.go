package file

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/webcenter-fr/eino-ext/server/approval/store"
	"github.com/webcenter-fr/eino-ext/server/approval/store/storetest"
)

func TestFileStoreConformance(t *testing.T) {
	storetest.Run(t, func(t *testing.T) store.ApprovalStore {
		s, err := New(context.Background(), &Config{Dir: t.TempDir()})
		require.NoError(t, err)
		return s
	})
}

func TestFileStoreAtomicWriteAndMtime(t *testing.T) {
	dir := t.TempDir()
	s, err := New(context.Background(), &Config{Dir: dir})
	require.NoError(t, err)
	rec := &store.Record{
		ServerName: "srv", ToolName: "tool", Args: []byte(`{}`),
		Requester: store.Requester{Sub: "alice"},
		ExpiresAt: time.Now().Add(time.Hour),
	}
	require.NoError(t, s.Create(context.Background(), rec))

	info, err := os.Stat(filepath.Join(dir, rec.ID+".json"))
	require.NoError(t, err)
	assert.WithinDuration(t, rec.ExpiresAt, info.ModTime(), time.Second)

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, entry := range entries {
		assert.NotContains(t, entry.Name(), ".tmp")
	}
}

func TestFileStoreRejectsPathTraversal(t *testing.T) {
	s, err := New(context.Background(), &Config{Dir: t.TempDir()})
	require.NoError(t, err)
	_, err = s.Get(context.Background(), "../escape")
	assert.ErrorIs(t, err, store.ErrNotFound)
}
