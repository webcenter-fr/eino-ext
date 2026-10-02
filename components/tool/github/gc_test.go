package github

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClonePathForSessionRequireSessionFailsClosed(t *testing.T) {
	b := &baseTool{cloneDir: "/root", requireSession: true}

	_, err := b.clonePathForSession(context.Background(), "o", "r")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "session is required")

	err = b.touchCloneSession(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "session is required")
}

func TestClonePathForSessionDefaultFallback(t *testing.T) {
	b := &baseTool{cloneDir: "/root"}

	path, err := b.clonePathForSession(context.Background(), "o", "r")
	require.NoError(t, err)
	assert.Equal(t, "/root/default/o/r", path)
}

func TestCloneSessionDir(t *testing.T) {
	configs := Configs{"a": {Token: "t", CloneDir: "/root"}}

	dir, err := CloneSessionDir(context.Background(), configs)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join("/root", defaultSession), dir)
	assert.Equal(t, cloneSessionPath("/root", ""), dir)

	t.Run("require session fails closed", func(t *testing.T) {
		cfg := Configs{"a": {Token: "t", CloneDir: "/root", RequireSession: true}}
		_, err := CloneSessionDir(context.Background(), cfg)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "session is required")
	})

	t.Run("multi-instance CloneDir mismatch errors", func(t *testing.T) {
		_, err := CloneSessionDir(context.Background(), Configs{
			"a": {Token: "t", CloneDir: "/root"},
			"b": {Token: "t", CloneDir: "/other"},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "same CloneDir")
	})

	t.Run("empty configs errors", func(t *testing.T) {
		_, err := CloneSessionDir(context.Background(), Configs{})
		require.Error(t, err)
	})
}

func TestTouchCloneSession(t *testing.T) {
	cloneDir := t.TempDir()
	namespace := cloneSessionPath(cloneDir, "")
	require.NoError(t, os.MkdirAll(namespace, 0o755))
	old := time.Now().Add(-2 * time.Hour)
	require.NoError(t, os.Chtimes(namespace, old, old))

	err := TouchCloneSession(context.Background(), Configs{"a": {Token: "t", CloneDir: cloneDir}})
	require.NoError(t, err)

	fi, err := os.Stat(namespace)
	require.NoError(t, err)
	assert.True(t, fi.ModTime().After(old.Add(time.Minute)), "mtime was not refreshed")

	t.Run("no-op when missing", func(t *testing.T) {
		err := TouchCloneSession(context.Background(), Configs{"a": {Token: "t", CloneDir: t.TempDir()}})
		require.NoError(t, err)
	})
}

func TestStartCloneGCNoop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	configs := Configs{"a": {Token: "t", CloneDir: t.TempDir()}}
	// ttl 0 / non-positive interval / empty configs must all be no-ops.
	StartCloneGC(ctx, configs, 0, time.Millisecond)
	StartCloneGC(ctx, configs, time.Hour, 0)
	StartCloneGC(ctx, configs, time.Hour, -time.Second)
	StartCloneGC(ctx, Configs{}, time.Hour, time.Millisecond)
}

func TestStartCloneGCSweepsStaleDirs(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cloneDir := t.TempDir()
	stale := cloneSessionPath(cloneDir, "stale-session")
	require.NoError(t, os.MkdirAll(stale, 0o755))
	old := time.Now().Add(-2 * time.Hour)
	require.NoError(t, os.Chtimes(stale, old, old))

	// A fresh namespace that the sweep must keep.
	fresh := cloneSessionPath(cloneDir, "fresh-session")
	require.NoError(t, os.MkdirAll(fresh, 0o755))

	StartCloneGC(ctx, Configs{"a": {Token: "t", CloneDir: cloneDir}}, time.Hour, 5*time.Millisecond)

	assert.Eventually(t, func() bool {
		_, statErr := os.Stat(stale)
		return os.IsNotExist(statErr)
	}, 2*time.Second, 5*time.Millisecond, "stale clone namespace must be swept")

	_, err := os.Stat(fresh)
	require.NoError(t, err, "fresh clone namespace must survive the sweep")

	// The goroutine stops on ctx cancellation (no way to observe directly; the
	// select/return structure is exercised by reaching here without deadlock).
	cancel()
}
