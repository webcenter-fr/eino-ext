package file

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/fileutil"
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

func TestStartGCRemovesStaleDirs(t *testing.T) {
	dir := t.TempDir()

	oldSession := filepath.Join(dir, "old-session")
	if err := os.MkdirAll(oldSession, 0o755); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(oldSession, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	recentSession := filepath.Join(dir, "recent-session")
	if err := os.MkdirAll(recentSession, 0o755); err != nil {
		t.Fatal(err)
	}

	// A single sweep via the shared helper is deterministic (no ticker wait).
	fileutil.SweepStaleDirs(dir, time.Hour)

	if _, err := os.Stat(oldSession); !os.IsNotExist(err) {
		t.Errorf("expected old session to be removed, but it still exists")
	}
	if _, err := os.Stat(recentSession); err != nil {
		t.Errorf("expected recent session to still exist, got error: %v", err)
	}
}

func TestStartGCIgnoresFiles(t *testing.T) {
	dir := t.TempDir()

	f, err := os.Create(filepath.Join(dir, "not-a-session.txt"))
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	oldTime := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(f.Name(), oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	fileutil.SweepStaleDirs(dir, time.Hour)

	if _, err := os.Stat(f.Name()); err != nil {
		t.Errorf("expected file to still exist, got error: %v", err)
	}
}

func TestStartGCEmptyWorkdir(t *testing.T) {
	dir := t.TempDir()
	// Should not panic.
	fileutil.SweepStaleDirs(dir, time.Hour)
}

func TestStartGCNonexistentWorkdir(t *testing.T) {
	// Should not panic.
	fileutil.SweepStaleDirs("/nonexistent/path/for/testing", time.Hour)
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

func TestTouchSessionKeepsNestedWriteAlive(t *testing.T) {
	workdir := t.TempDir()
	cfg := &Config{Workdir: workdir, SessionTTL: time.Hour}

	sessionDir := mustCreateSessionDir(t, workdir)
	// Pre-existing nested path: overwriting it would not update the session
	// directory mtime on its own, which is exactly the bug item 2 fixes.
	if err := os.MkdirAll(filepath.Join(sessionDir, "a", "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, "a", "b", "c.txt"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	oldTime := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(sessionDir, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	if err := TouchSession(context.Background(), cfg); err != nil {
		t.Fatalf("TouchSession: %v", err)
	}

	// Control: a sibling session directory with an old mtime is removed.
	staleSibling := filepath.Join(workdir, "stale-sibling")
	if err := os.MkdirAll(staleSibling, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(staleSibling, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	fileutil.SweepStaleDirs(workdir, time.Hour)

	if _, err := os.Stat(sessionDir); err != nil {
		t.Fatalf("touched session directory must survive GC, got %v", err)
	}
	if _, err := os.Stat(staleSibling); !os.IsNotExist(err) {
		t.Fatalf("stale sibling must be removed, got %v", err)
	}
}

func TestTouchSessionNoopWhenMissing(t *testing.T) {
	workdir := t.TempDir()
	cfg := &Config{Workdir: workdir}

	if err := TouchSession(context.Background(), cfg); err != nil {
		t.Fatalf("expected no error for missing session dir, got %v", err)
	}
	if _, err := os.Stat(testSessionDir(workdir)); !os.IsNotExist(err) {
		t.Fatalf("TouchSession must not create the session directory, got %v", err)
	}
}

func TestSessionDirRequireSessionFailsClosed(t *testing.T) {
	cfg := &Config{Workdir: t.TempDir(), RequireSession: true}

	if _, err := SessionDir(context.Background(), cfg); err == nil {
		t.Fatal("expected an error when RequireSession is set and no session is present")
	}
	if _, err := sessionDir(cfg, context.Background()); err == nil {
		t.Fatal("expected sessionDir to fail closed")
	}

	// With RequireSession off, the empty session falls back to "session".
	cfg.RequireSession = false
	dir, err := SessionDir(context.Background(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dir != testSessionDir(cfg.Workdir) {
		t.Fatalf("SessionDir = %q, want %q", dir, testSessionDir(cfg.Workdir))
	}
}
