package file

import (
	"context"
	"os"
	"path/filepath"
	"time"
)

// StartGC starts a background goroutine that periodically scans the Workdir
// for session subdirectories and removes those whose modification time is
// older than cfg.SessionTTL. As long as a session is actively used, its
// directory modtime stays fresh (every tool invocation creates files or
// directories inside it), so the modtime check is what protects the active
// session. Set SessionTTL generously (e.g. 1 hour) to avoid races.
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
				cleanStaleSessions(cfg)
			}
		}
	}()
}

// cleanStaleSessions scans cfg.Workdir for session subdirectories and removes
// those whose modification time is older than cfg.SessionTTL. Regular files in
// Workdir are never removed. Read errors are silently ignored: the GC is
// best-effort and must never crash the goroutine.
func cleanStaleSessions(cfg *Config) {
	entries, err := os.ReadDir(cfg.Workdir)
	if err != nil {
		// Workdir may not exist yet; that's fine.
		return
	}

	cutoff := time.Now().Add(-cfg.SessionTTL)

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		fullPath := filepath.Join(cfg.Workdir, entry.Name())
		fi, err := os.Stat(fullPath)
		if err != nil {
			continue
		}

		if fi.ModTime().After(cutoff) {
			continue // still fresh
		}

		_ = os.RemoveAll(fullPath)
	}
}
