package file

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"emperror.dev/errors"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/fileutil"
)

// sessionPath returns the session-scoped directory path: <Workdir>/<segment>.
// An empty session falls back to the "session" segment; a non-empty session is
// hashed via fileutil.SessionDirName so distinct session ids never collide
// after sanitising.
func sessionPath(workdir, session string) string {
	segment := "session"
	if session != "" {
		segment = fileutil.SessionDirName(session)
	}
	return filepath.Join(workdir, segment)
}

// sessionDir derives the session directory from ctx. It fails closed when
// cfg.RequireSession is true and no session id is present, before any directory
// creation.
func sessionDir(cfg *Config, ctx context.Context) (string, error) {
	session := fileutil.SessionFromContext(ctx, FileSessionKey)
	if session == "" && cfg.RequireSession {
		return "", errors.Errorf("a session is required but none was found in the invocation context; set the adk session value %q", FileSessionKey)
	}
	return sessionPath(cfg.Workdir, session), nil
}

// resolvePath resolves relPath within the session directory derived from the
// invocation context. With createDirs false it creates nothing (used by reads
// and every dry-run). With createDirs true it creates the session directory and
// missing intermediate dirs (used by the execute path of write/copy/move). It
// does NOT touch mtime (see TouchSession).
func resolvePath(cfg *Config, ctx context.Context, relPath string, createDirs bool) (string, error) {
	root, err := sessionDir(cfg, ctx)
	if err != nil {
		return "", err
	}

	if createDirs {
		if err := os.MkdirAll(root, 0o755); err != nil {
			return "", errors.Wrapf(err, "failed to create session directory %q", root)
		}
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

// validateTransferPaths performs the read-only validation shared by file_copy
// and file_move: it resolves source+destination within the session, rejects
// same clean path, dest-inside-source, symlinks, a missing source, and file/dir
// type mismatches. It creates nothing; the caller creates destination parents
// on the authorized execute path.
func validateTransferPaths(cfg *Config, ctx context.Context, source, destination string) (srcSafePath, dstSafePath string, isDir bool, err error) {
	root, err := sessionDir(cfg, ctx)
	if err != nil {
		return "", "", false, err
	}

	srcSafePath, err = resolvePath(cfg, ctx, source, false)
	if err != nil {
		return "", "", false, err
	}
	dstSafePath, err = fileutil.ValidateRelativePath(root, destination)
	if err != nil {
		return "", "", false, err
	}

	// Read-only destination inspection: rejects symlinks anywhere on the path
	// without creating anything (missing components are allowed).
	if _, statErr := statReadOnly(root, destination, 0); statErr != nil {
		return "", "", false, statErr
	}

	// Reject aliased or nested endpoints. A destination that resolves to the
	// same path as the source would corrupt the source in place, and a
	// destination inside the source directory would make CopyDir write into the
	// very tree it is walking, recursing without bound.
	cleanSrc := filepath.Clean(srcSafePath)
	cleanDst := filepath.Clean(dstSafePath)
	if cleanDst == cleanSrc {
		return "", "", false, errors.Errorf("source and destination resolve to the same path %q", source)
	}
	if strings.HasPrefix(cleanDst, cleanSrc+string(filepath.Separator)) {
		return "", "", false, errors.Errorf("destination %q is inside the source directory %q", destination, source)
	}

	srcFi, err := os.Lstat(srcSafePath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", "", false, errors.Wrapf(err, "source path %q not found", source)
		}
		return "", "", false, errors.Wrapf(err, "failed to stat source path %q", source)
	}
	isDir = srcFi.IsDir()

	// Reject type mismatches if destination exists.
	if dstFi, statErr := os.Lstat(dstSafePath); statErr == nil {
		if dstFi.Mode()&os.ModeSymlink != 0 {
			return "", "", false, errors.Errorf("destination %q is a symlink; symlinks are not allowed", destination)
		}
		if dstFi.IsDir() && !isDir {
			return "", "", false, errors.Errorf("destination %q is a directory but source is a file", destination)
		}
		if !dstFi.IsDir() && isDir {
			return "", "", false, errors.Errorf("destination %q is a file but source is a directory", destination)
		}
	}
	return srcSafePath, dstSafePath, isDir, nil
}
