// Package workspace owns the on-disk layout shared by the GitHub clone tools
// and the Dagger-backed shell tool. It resolves the per-session clone namespace
// (<root>/<sessionHash>/<owner>/<repo>), validates paths under a workspace root,
// refreshes session mtimes for garbage collection, and sweeps stale session
// directories.
//
// The host filesystem is the single source of truth: the shell tool mounts a
// workspace directory into a container and only writes container changes back
// on an explicit opt-in. This package contains no Dagger or GitHub logic so
// both components can depend on it without depending on each other.
package workspace

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"emperror.dev/errors"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/fileutil"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
)

// DefaultSessionKey is the adk session-value key used when a Layout does not
// specify one. Hosts that share a workspace with the GitHub tools should set
// SessionKey to github.CloneSessionKey.
const DefaultSessionKey = "session_id"

// DefaultSessionSegment is the namespace used when no session id is present and
// RequireSession is false.
const DefaultSessionSegment = "default"

// Layout describes where session workspaces live and how the session id is
// resolved from the invocation context.
type Layout struct {
	// Root is the absolute directory under which session namespaces are created.
	Root string `validate:"required" jsonschema:"description=Absolute root directory for session workspaces"`

	// SessionKey is the adk session-value key carrying the session id.
	SessionKey string `validate:"required" jsonschema:"description=ADK session-value key carrying the session id"`

	// RequireSession fails closed when no session id is present instead of
	// falling back to the "default" namespace.
	RequireSession bool `validate:"omitempty" jsonschema:"description=Fail closed when no session id is present"`
}

// NewLayout applies defaults to cfg, validates it, and verifies that Root is a
// safe host directory. It returns a copy so the caller's configuration is not
// mutated.
func NewLayout(cfg *Layout) (*Layout, error) {
	if cfg == nil {
		cfg = &Layout{}
	}
	layout := *cfg
	if layout.SessionKey == "" {
		layout.SessionKey = DefaultSessionKey
	}
	if err := validate.Struct(&layout); err != nil {
		return nil, err
	}
	if err := fileutil.ValidateRootDir(layout.Root); err != nil {
		return nil, errors.Wrap(err, "workspace root")
	}
	return &layout, nil
}

// SessionDirFor returns <root>/<sessionSegment> for an explicit session id.
// An empty session id maps to the literal "default" segment; a non-empty id is
// hashed with fileutil.SessionDirName so distinct ids never collide.
func (l *Layout) SessionDirFor(session string) string {
	segment := DefaultSessionSegment
	if session != "" {
		segment = fileutil.SessionDirName(session)
	}
	return filepath.Join(l.Root, segment)
}

// sessionFromContext returns the session id carried in ctx, failing closed when
// RequireSession is set and no session is present.
func (l *Layout) sessionFromContext(ctx context.Context) (string, error) {
	session := fileutil.SessionFromContext(ctx, l.SessionKey)
	if session == "" && l.RequireSession {
		return "", errors.Errorf("a session is required but none was found in the invocation context; set the adk session value %q", l.SessionKey)
	}
	return session, nil
}

// SessionDir returns the per-session namespace for the session carried in ctx.
// It fails closed when RequireSession is set and no session is present.
func (l *Layout) SessionDir(ctx context.Context) (string, error) {
	session, err := l.sessionFromContext(ctx)
	if err != nil {
		return "", err
	}
	return l.SessionDirFor(session), nil
}

// RepoDirFor returns <root>/<sessionSegment>/<owner>/<repo> for an explicit
// session id. Owner and repo are sanitized so they cannot escape the session
// namespace.
func (l *Layout) RepoDirFor(session, owner, repo string) string {
	return filepath.Join(
		l.SessionDirFor(session),
		fileutil.SanitizePathSegment(owner, "repo"),
		fileutil.SanitizePathSegment(repo, "repo"),
	)
}

// RepoDir returns the session-scoped clone path for owner/repo.
func (l *Layout) RepoDir(ctx context.Context, owner, repo string) (string, error) {
	session, err := l.sessionFromContext(ctx)
	if err != nil {
		return "", err
	}
	return l.RepoDirFor(session, owner, repo), nil
}

// Touch refreshes the mtime of the current session namespace so StartGC keeps
// it. It is a no-op when the namespace does not exist.
func (l *Layout) Touch(ctx context.Context) error {
	dir, err := l.SessionDir(ctx)
	if err != nil {
		return err
	}
	return fileutil.TouchDir(dir)
}

// StartGC starts a background goroutine that every interval sweeps Root for
// session namespaces whose mtime is older than ttl and removes them. A
// non-positive ttl or interval is a no-op; the goroutine stops on ctx
// cancellation.
func (l *Layout) StartGC(ctx context.Context, ttl, interval time.Duration) {
	if ttl <= 0 || interval <= 0 {
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
				fileutil.SweepStaleDirs(l.Root, ttl)
			}
		}
	}()
}

// Exists reports whether path exists. It is used by the shell tool to evict a
// cached container when its workspace was swept mid-session.
func Exists(path string) (bool, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, errors.Wrapf(err, "failed to stat workspace path %q", path)
	}
	return true, nil
}

// ResolvePath validates relPath under root and rejects symlinks at every path
// component. When createDirs is true, missing intermediate directories are
// created for writes.
func ResolvePath(root, relPath string, createDirs bool) (string, error) {
	fullPath, err := fileutil.ValidateRelativePath(root, relPath)
	if err != nil {
		return "", err
	}
	return fileutil.ResolveSymlinkSafe(root, fullPath, createDirs)
}
