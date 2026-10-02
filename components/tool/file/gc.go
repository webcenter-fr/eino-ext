package file

import (
	"context"
	"time"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/fileutil"
)

// SessionDir returns the session directory <Workdir>/<session> for the session
// carried in ctx. With cfg.RequireSession set and no session present it returns
// an error (fail closed); otherwise it falls back to the "session" segment.
func SessionDir(ctx context.Context, cfg *Config) (string, error) {
	return sessionDir(cfg, ctx)
}

// TouchSession updates the session directory's mtime so the GC (StartGC) keeps
// it. It is a no-op when the session directory does not exist. Every tool
// Invoke on the execute path calls this, and read.go calls it too so read-only
// sessions stay alive.
func TouchSession(ctx context.Context, cfg *Config) error {
	dir, err := sessionDir(cfg, ctx)
	if err != nil {
		return err
	}
	return fileutil.TouchDir(dir)
}

// StartGC starts a background goroutine that, every interval, sweeps the
// Workdir for session subdirectories whose mtime is older than SessionTTL and
// removes them. Sessions are kept alive by TouchSession, which every tool
// invocation calls on its session directory; a session that only writes to a
// nested path, only overwrites an existing file, or only reads is still kept
// fresh because the touch targets the session directory itself rather than
// relying on indirect mtime updates.
//
// The goroutine runs every interval until ctx is cancelled. If cfg.SessionTTL
// is zero or interval is not positive, StartGC returns immediately (no-op).
//
// The caller is responsible for cancelling ctx to stop the goroutine (e.g.,
// via a parent context that is cancelled on server shutdown).
//
// Usage:
//
//	ctx, cancel := context.WithCancel(context.Background())
//	defer cancel()
//	go file.StartGC(ctx, cfg, 5*time.Minute)
func StartGC(ctx context.Context, cfg *Config, interval time.Duration) {
	// A non-positive interval would make time.NewTicker panic inside the
	// goroutine and crash the process; treat it as a no-op instead.
	if cfg == nil || cfg.SessionTTL == 0 || interval <= 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				fileutil.SweepStaleDirs(cfg.Workdir, cfg.SessionTTL)
			}
		}
	}()
}
