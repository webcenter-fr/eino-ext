package file

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStartGCNoopWhenTTLZero(t *testing.T) {
	// StartGC with zero TTL should return immediately without starting a goroutine.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// This should not panic or block.
	StartGC(ctx, &Config{Workdir: t.TempDir(), SessionTTL: 0}, time.Millisecond)
}

func TestStartGCNoopWhenIntervalNotPositive(t *testing.T) {
	// A non-positive interval must be a no-op: time.NewTicker would panic on
	// it inside the goroutine and crash the process.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// This should not panic or block.
	StartGC(ctx, &Config{Workdir: t.TempDir(), SessionTTL: time.Hour}, 0)
	StartGC(ctx, &Config{Workdir: t.TempDir(), SessionTTL: time.Hour}, -time.Second)
}

func TestCleanStaleSessionsRemovesOldDirs(t *testing.T) {
	dir := t.TempDir()

	// Create a session directory with an old modtime.
	oldSession := filepath.Join(dir, "old-session")
	if err := os.MkdirAll(oldSession, 0o755); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(oldSession, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	// Create a session directory with a recent modtime.
	recentSession := filepath.Join(dir, "recent-session")
	if err := os.MkdirAll(recentSession, 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := &Config{Workdir: dir, SessionTTL: 1 * time.Hour}
	cleanStaleSessions(cfg)

	// Old session should be removed.
	if _, err := os.Stat(oldSession); !os.IsNotExist(err) {
		t.Errorf("expected old session to be removed, but it still exists")
	}

	// Recent session should still exist.
	if _, err := os.Stat(recentSession); err != nil {
		t.Errorf("expected recent session to still exist, got error: %v", err)
	}
}

func TestCleanStaleSessionsIgnoresFiles(t *testing.T) {
	dir := t.TempDir()

	// Create a regular file in Workdir (not a session directory).
	f, err := os.Create(filepath.Join(dir, "not-a-session.txt"))
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	// Set its modtime to be old.
	oldTime := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(f.Name(), oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	cfg := &Config{Workdir: dir, SessionTTL: 1 * time.Hour}
	cleanStaleSessions(cfg)

	// File should still exist (GC only removes directories).
	if _, err := os.Stat(f.Name()); err != nil {
		t.Errorf("expected file to still exist, got error: %v", err)
	}
}

func TestCleanStaleSessionsEmptyWorkdir(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{Workdir: dir, SessionTTL: 1 * time.Hour}
	// Should not panic.
	cleanStaleSessions(cfg)
}

func TestCleanStaleSessionsNonexistentWorkdir(t *testing.T) {
	cfg := &Config{Workdir: "/nonexistent/path/for/testing", SessionTTL: 1 * time.Hour}
	// Should not panic.
	cleanStaleSessions(cfg)
}

func TestStartGCStopsOnContextCancel(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())

	cfg := &Config{Workdir: dir, SessionTTL: 1 * time.Hour}
	StartGC(ctx, cfg, 10*time.Millisecond)

	// Let it run for a bit.
	time.Sleep(50 * time.Millisecond)

	// Cancel and verify the goroutine stops (no panic, no leak).
	cancel()

	// Give it time to shut down.
	time.Sleep(50 * time.Millisecond)

	// If we reach here without timeout, the goroutine stopped cleanly.
}
