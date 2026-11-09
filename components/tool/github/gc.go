package github

import (
	"context"
	"time"

	"emperror.dev/errors"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/workspace"
)

// cloneSettingsFromConfigs returns the single shared CloneDir and RequireSession
// across all configured instances, erroring if the map is empty or the instances
// disagree.
func cloneSettingsFromConfigs(configs Configs) (cloneDir string, requireSession bool, err error) {
	if len(configs) == 0 {
		return "", false, errors.New("at least one GitHub instance configuration is required")
	}
	cloneDirSet := false
	requireSessionSet := false
	for _, cfg := range configs {
		if !cloneDirSet {
			cloneDir = cfg.CloneDir
			cloneDirSet = true
		} else if cfg.CloneDir != cloneDir {
			return "", false, errors.Errorf("all instances must share the same CloneDir (got %q and %q)", cfg.CloneDir, cloneDir)
		}
		if !requireSessionSet {
			requireSession = cfg.RequireSession
			requireSessionSet = true
		} else if cfg.RequireSession != requireSession {
			return "", false, errors.Errorf("all instances must share the same RequireSession (got %v and %v)", requireSession, cfg.RequireSession)
		}
	}
	return cloneDir, requireSession, nil
}

// cloneDirFromConfigs returns the single shared CloneDir, erroring if configs
// are empty or disagree.
func cloneDirFromConfigs(configs Configs) (string, error) {
	cloneDir, _, err := cloneSettingsFromConfigs(configs)
	return cloneDir, err
}

// cloneLayoutFromConfigs builds the workspace layout shared by all instances.
func cloneLayoutFromConfigs(configs Configs) (*workspace.Layout, error) {
	cloneDir, requireSession, err := cloneSettingsFromConfigs(configs)
	if err != nil {
		return nil, err
	}
	return &workspace.Layout{
		Root:           cloneDir,
		SessionKey:     CloneSessionKey,
		RequireSession: requireSession,
	}, nil
}

// CloneSessionDir returns the per-session clone namespace <CloneDir>/<session>
// for the session carried in ctx, failing closed when RequireSession is set and
// no session is present. It validates that all configs share the same CloneDir.
func CloneSessionDir(ctx context.Context, configs Configs) (string, error) {
	layout, err := cloneLayoutFromConfigs(configs)
	if err != nil {
		return "", err
	}
	return layout.SessionDir(ctx)
}

// TouchCloneSession updates the mtime of the per-session clone namespace so
// StartCloneGC keeps the active session's clones. No-op when the directory does
// not exist.
func TouchCloneSession(ctx context.Context, configs Configs) error {
	layout, err := cloneLayoutFromConfigs(configs)
	if err != nil {
		return err
	}
	return layout.Touch(ctx)
}

// StartCloneGC starts a background goroutine that every interval sweeps
// <CloneDir> for session subdirectories whose mtime is older than ttl and
// removes them. ttl==0, interval<=0, or an unusable configs set (empty or
// disagreeing instances) -> no-op; the goroutine stops on ctx cancellation.
func StartCloneGC(ctx context.Context, configs Configs, ttl, interval time.Duration) {
	cloneDir, err := cloneDirFromConfigs(configs)
	if err != nil || ttl == 0 || interval <= 0 {
		return
	}
	(&workspace.Layout{Root: cloneDir, SessionKey: CloneSessionKey}).StartGC(ctx, ttl, interval)
}
