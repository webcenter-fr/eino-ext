// Package fileutil provides pure filesystem safety helpers for validating
// paths under a root directory, rejecting symlinks at every component,
// detecting binary content, excluding VCS metadata directories, copying files
// and directory trees, and validating host-side root directories. It contains
// no business logic and returns plain errors; callers wrap them with
// component-specific context using emperror.dev/errors.
package fileutil

import (
	"bytes"
	"context"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/adk"
)

// systemDirs contains paths that must not be used as a root directory for
// filesystem operations.
var systemDirs = map[string]bool{
	"/":     true,
	"/etc":  true,
	"/bin":  true,
	"/usr":  true,
	"/var":  true,
	"/proc": true,
	"/sys":  true,
	"/dev":  true,
	"/tmp":  true,
}

// SessionFromContext reads the session ID from the adk session values under
// key. It returns an empty string when the value is absent, empty, or not a
// string.
func SessionFromContext(ctx context.Context, key string) string {
	v, ok := adk.GetSessionValue(ctx, key)
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok || s == "" {
		return ""
	}
	return s
}

// SanitizePathSegment ensures a path segment does not contain path traversal
// characters. NUL bytes, path separators, and control characters are removed,
// ".." sequences are collapsed, and an empty or "." result is replaced with
// fallback.
func SanitizePathSegment(s string, fallback string) string {
	s = strings.ReplaceAll(s, "\x00", "")
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, "\\", "_")
	for strings.Contains(s, "..") {
		s = strings.ReplaceAll(s, "..", "")
	}
	s = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, s)
	if s == "" || s == "." {
		s = fallback
	}
	return s
}

// ValidateRelativePath resolves a relative path under root and returns the
// absolute, cleaned path. It rejects:
//   - paths that escape root after cleaning (contain ".." that resolves
//     outside the root)
//   - absolute paths and drive letters
//   - NUL bytes (which can truncate paths at the OS level)
//
// This is a purely lexical check. Callers MUST additionally call
// ResolveSymlinkSafe to verify that no path component is a symlink that
// resolves outside root.
//
// relPath may be empty (returns root itself) or a relative path with
// forward slashes.
func ValidateRelativePath(root, relPath string) (string, error) {
	cleanRoot := filepath.Clean(root)
	if relPath == "" {
		return cleanRoot, nil
	}
	// Reject NUL bytes — on Unix, NUL terminates the path in syscalls, so a
	// path like "valid\x00../../etc/passwd" would be interpreted as "valid"
	// by the OS but pass lexical checks.
	if strings.ContainsRune(relPath, '\x00') {
		return "", errors.Errorf("path contains a null byte")
	}
	// Normalize separators.
	relPath = filepath.FromSlash(relPath)
	// Reject absolute paths and drive letters.
	if filepath.IsAbs(relPath) {
		return "", errors.Errorf("path must be relative, got absolute path %q", relPath)
	}
	joined := filepath.Join(cleanRoot, relPath)
	cleaned := filepath.Clean(joined)
	// Must be within root.
	if cleaned != cleanRoot && !strings.HasPrefix(cleaned, cleanRoot+string(filepath.Separator)) {
		return "", errors.Errorf("path %q escapes root directory %q", relPath, root)
	}
	// Reject any remaining ".." segments (defense in depth).
	if strings.Contains(filepath.ToSlash(cleaned), "/../") || strings.HasSuffix(filepath.ToSlash(cleaned), "/..") {
		return "", errors.Errorf("path %q contains directory traversal", relPath)
	}
	return cleaned, nil
}

// IsWithinPath returns true if path is equal to root or a descendant of root.
// Both paths are cleaned before comparison.
func IsWithinPath(root, path string) bool {
	cleanRoot := filepath.Clean(root)
	cleanPath := filepath.Clean(path)
	if cleanPath == cleanRoot {
		return true
	}
	if cleanRoot == string(filepath.Separator) {
		return filepath.IsAbs(cleanPath)
	}
	return strings.HasPrefix(cleanPath, cleanRoot+string(filepath.Separator))
}

// ResolveSymlinkSafe walks each component of fullPath from root, rejecting
// any component that is a symlink. This prevents symlink-based directory
// traversal where a malicious tree contains a symlink (e.g., "link -> /etc")
// that would redirect file reads/writes outside the root.
//
// If createDirs is true, missing intermediate directories are created (for
// writes). If createDirs is false, missing intermediate components cause an
// error; a missing final component is allowed (the caller will get a
// not-exist error from the subsequent file operation).
//
// The returned path is verified to be within root.
func ResolveSymlinkSafe(root, fullPath string, createDirs bool) (string, error) {
	cleanRoot := filepath.Clean(root)
	cleanFull := filepath.Clean(fullPath)

	rel, err := filepath.Rel(cleanRoot, cleanFull)
	if err != nil {
		return "", errors.Wrapf(err, "failed to compute relative path for symlink check")
	}
	rel = filepath.ToSlash(rel)
	if rel == "." {
		return cleanRoot, nil
	}
	// Defense-in-depth: reject if the relative path escapes upward. ValidateRelativePath
	// should have already caught this, but double-check.
	if strings.HasPrefix(rel, "../") || rel == ".." {
		return "", errors.Errorf("path escapes root directory")
	}

	parts := strings.Split(rel, "/")
	current := cleanRoot
	for i, part := range parts {
		next := filepath.Join(current, part)
		fi, statErr := os.Lstat(next)
		if statErr != nil {
			if !os.IsNotExist(statErr) {
				return "", errors.Wrapf(statErr, "failed to stat path component %q", next)
			}
			// Component does not exist.
			if i < len(parts)-1 {
				// Intermediate directory missing.
				if createDirs {
					// Tolerate EEXIST: a concurrent invocation may have created
					// the same directory between the Lstat above and this Mkdir.
					if mkErr := os.Mkdir(next, 0o755); mkErr != nil && !os.IsExist(mkErr) {
						return "", errors.Wrapf(mkErr, "failed to create directory %q", next)
					}
				} else {
					return "", errors.Wrapf(statErr, "path component %q does not exist", next)
				}
			}
			// Final component missing — OK for both read (caller gets not-exist) and write (caller creates).
			current = next
			continue
		}
		// Reject symlinks at any level.
		if fi.Mode()&os.ModeSymlink != 0 {
			return "", errors.Errorf("symlink at path component %q; symlinks are not allowed", next)
		}
		// Intermediate components must be directories.
		if i < len(parts)-1 && !fi.IsDir() {
			return "", errors.Errorf("path component %q is not a directory", next)
		}
		current = next
	}
	return current, nil
}

// IsBinary returns true if the first 8KB of data contain a null byte, which is
// the standard heuristic for detecting binary files.
func IsBinary(data []byte) bool {
	n := len(data)
	if n > 8192 {
		n = 8192
	}
	return bytes.Contains(data[:n], []byte{0})
}

// RejectDotGitPath returns an error if the path refers to the .git directory at
// any level. The path is cleaned before checking so that prefixes like "./" or
// "subdir/../" cannot bypass the check.
func RejectDotGitPath(path string) error {
	if path == "" {
		return nil
	}
	cleaned := filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
	if cleaned == ".git" || strings.HasPrefix(cleaned, ".git/") {
		return errors.Errorf("access to .git directory is not allowed")
	}
	// Reject if any path component is .git (covers nested .git in submodules).
	for _, part := range strings.Split(cleaned, "/") {
		if part == ".git" {
			return errors.Errorf("access to .git directory is not allowed")
		}
	}
	return nil
}

// ValidateRootDir validates that a directory path is safe to use as a
// host-side root for filesystem operations. It rejects:
//   - empty paths
//   - relative paths
//   - paths containing ".."
//   - system directories (/, /etc, /bin, /usr, /var, /proc, /sys, /dev, /tmp)
//   - paths under /proc/, /sys/, /dev/
//
// Callers that need component-specific error context (e.g. "CloneDir" or
// "Workdir") should wrap the returned error with emperror.dev/errors.
func ValidateRootDir(dir string) error {
	if dir == "" {
		return errors.New("root directory is required")
	}
	if !filepath.IsAbs(dir) {
		return errors.Errorf("root directory must be an absolute path, got %q", dir)
	}
	if strings.Contains(dir, "..") {
		return errors.Errorf("root directory must not contain directory traversal, got %q", dir)
	}
	cleaned := filepath.Clean(dir)
	if systemDirs[cleaned] {
		return errors.Errorf("root directory must not be a system directory, got %q", cleaned)
	}
	if strings.HasPrefix(cleaned, "/proc/") || strings.HasPrefix(cleaned, "/sys/") || strings.HasPrefix(cleaned, "/dev/") {
		return errors.Errorf("root directory must not be under a system mount, got %q", cleaned)
	}
	return nil
}

// CopyFileContents copies the contents of the file at src to dst, creating or
// truncating dst. Both files are closed before returning. dst is created with
// permissions 0o644 (an existing dst keeps its own permissions).
func CopyFileContents(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return errors.Wrapf(err, "failed to open source file %q", src)
	}
	defer func() { _ = srcFile.Close() }()

	dstFile, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return errors.Wrapf(err, "failed to create destination file %q", dst)
	}
	defer func() { _ = dstFile.Close() }()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return errors.Wrapf(err, "failed to copy %q to %q", src, dst)
	}
	return nil
}

// CopyDir recursively copies the directory tree from src to dst. It creates
// destination directories as needed and copies each file with io.Copy.
// Symlinks inside the tree are skipped: os.Open follows symlinks, so copying
// them would pull the symlink target's content (possibly from outside the
// root) into the destination (CWE-59). When skipDotGit is true, directories
// named .git are skipped as well. Returns the number of files copied and the
// total bytes written.
func CopyDir(src, dst string, skipDotGit bool) (fileCount int, totalBytes int64, err error) {
	err = filepath.WalkDir(src, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, relErr := filepath.Rel(src, path)
		if relErr != nil {
			return relErr
		}
		target := filepath.Join(dst, rel)

		if d.IsDir() {
			if skipDotGit && d.Name() == ".git" {
				return filepath.SkipDir
			}
			if mkErr := os.MkdirAll(target, 0o755); mkErr != nil {
				return mkErr
			}
			return nil
		}
		// DirEntry.Type() uses Lstat semantics, so this detects symlinks
		// without an extra syscall.
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}

		srcFile, openErr := os.Open(path)
		if openErr != nil {
			return openErr
		}
		defer func() { _ = srcFile.Close() }()

		// 0o644 matches the file tools' permission convention (os.Create
		// would use 0o666 before umask).
		dstFile, createErr := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
		if createErr != nil {
			return createErr
		}
		defer func() { _ = dstFile.Close() }()

		written, copyErr := io.Copy(dstFile, srcFile)
		if copyErr != nil {
			return copyErr
		}

		totalBytes += written
		fileCount++
		return nil
	})
	return fileCount, totalBytes, err
}

// WalkDirFiles returns the relative paths (from root, slash-normalized) of all
// regular files in the directory tree. Symlinks are skipped. When skipDotGit
// is true, directories named .git are skipped as well.
func WalkDirFiles(root string, skipDotGit bool) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		if skipDotGit && d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		fi, err := os.Lstat(path)
		if err != nil {
			return nil // skip on stat error
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return nil // skip symlinks
		}
		if fi.IsDir() {
			return nil // descend into dirs but don't add them
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	return files, err
}

// ApplyLineRange extracts the 1-indexed [startLine, endLine] range of lines
// from content and returns the extracted text along with the actual applied
// bounds (actualStart is the first returned line, actualEnd is one past the
// last returned line). startLine or endLine <= 0 means "from the beginning" /
// "to the end" respectively; out-of-range bounds are clamped. When the range
// is empty the result is the empty string.
func ApplyLineRange(content string, startLine, endLine int) (result string, actualStart, actualEnd int) {
	lines := strings.Split(content, "\n")
	startIdx := 0
	endIdx := len(lines)

	if startLine > 0 {
		startIdx = startLine - 1
		if startIdx > len(lines) {
			startIdx = len(lines)
		}
	}
	if endLine > 0 {
		endIdx = endLine
		if endIdx > len(lines) {
			endIdx = len(lines)
		}
	}
	if startIdx < endIdx {
		result = strings.Join(lines[startIdx:endIdx], "\n")
	} else {
		result = ""
	}
	return result, startIdx + 1, endIdx
}
