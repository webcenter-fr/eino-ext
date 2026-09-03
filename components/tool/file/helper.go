package file

import (
	"context"
	"os"
	"path/filepath"

	"emperror.dev/errors"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/fileutil"
)

// sessionPath returns the session-scoped directory path: <Workdir>/<session>.
func sessionPath(workdir, session string) string {
	return filepath.Join(workdir, fileutil.SanitizePathSegment(session, "session"))
}

// resolvePath validates and resolves a relative path within the session
// directory derived from the invocation context. Returns the absolute safe
// path. Ensures the session directory exists.
func resolvePath(workdir string, ctx context.Context, relPath string, createDirs bool) (string, error) {
	session := fileutil.SessionFromContext(ctx, FileSessionKey)
	root := sessionPath(workdir, session)

	// Ensure session directory exists.
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", errors.Wrapf(err, "failed to create session directory %q", root)
	}

	// Validate the relative path lexically.
	fullPath, err := fileutil.ValidateRelativePath(root, relPath)
	if err != nil {
		return "", err
	}

	// Reject symlinks at every component.
	safePath, err := fileutil.ResolveSymlinkSafe(root, fullPath, createDirs)
	if err != nil {
		return "", err
	}

	return safePath, nil
}
