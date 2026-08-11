package github

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"emperror.dev/errors"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/goccy/go-json"
	ghlib "github.com/google/go-github/v71/github" // aliased to avoid conflict with package name
	"github.com/webcenter-fr/eino-ext/libs/toolkit/filter"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/marshal"
)

// labelList splits a comma-separated labels string into a slice.
func labelList(labels string) []string {
	if labels == "" {
		return nil
	}
	parts := strings.Split(labels, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// truncate returns a truncated string with "..." if longer than maxLen.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// clonePath returns the safe local path for cloning a repository.
func clonePath(cloneDir, owner, repo string) string {
	return fmt.Sprintf("%s/%s/%s", cloneDir, sanitizeSegment(owner), sanitizeSegment(repo))
}

// sanitizeSegment ensures a path segment does not contain path traversal characters.
func sanitizeSegment(s string) string {
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
		s = "repo"
	}
	return s
}

// defaultMaxPages is the fallback page cap used when a caller does not specify
// one. It is intentionally high so that list tools traverse the full result
// set by default rather than silently truncating large organizations.
const defaultMaxPages = 1000

// paginateList iterates through paginated GitHub API results, accumulating all
// items into a single slice. The fetch callback is called for each page; it
// must set opts.Page to the supplied page number before making the API call.
//
// maxPages bounds the number of fetched pages to prevent runaway loops:
//   - maxPages > 0: fetch at most maxPages pages;
//   - maxPages == 0: use the default safety cap (1000 pages, effectively all pages);
//   - maxPages < 0: fetch every page until the API reports no next page (no cap).
//
// Iteration also stops as soon as the API response reports no next page
// (resp.NextPage == 0), so passing a high cap does not cause extra requests.
func paginateList[T any](
	fetch func(page int) ([]T, *ghlib.Response, error),
	maxPages int,
) ([]T, error) {
	if maxPages == 0 {
		maxPages = defaultMaxPages
	}
	var allItems []T
	page := 1
	for pagesFetched := 0; maxPages < 0 || pagesFetched < maxPages; pagesFetched++ {
		items, resp, err := fetch(page)
		if err != nil {
			return nil, errors.Wrapf(err, "paginateList fetch page %d", page)
		}
		if resp == nil {
			return nil, errors.Errorf("paginateList fetch page %d: nil response", page)
		}
		allItems = append(allItems, items...)
		if resp.NextPage == 0 {
			break
		}
		page = resp.NextPage
	}
	return allItems, nil
}

//go:embed prompts/list_output_guidance.md
var listOutputGuidance string

//go:embed prompts/describe_output_guidance.md
var describeOutputGuidance string

// filterMapMarshal maps each source item to an output value, marshals it, keeps
// only items whose JSON matches re, and returns the JSON array of survivors.
func filterMapMarshal[T, O any](items []T, re *regexp.Regexp, toOutput func(T) O) (string, error) {
	outputs := make([]json.RawMessage, 0, len(items))
	for _, item := range items {
		outputJSON := json.RawMessage(marshal.MustMarshal(toOutput(item)))
		if !filter.Match(outputJSON, re) {
			continue
		}
		outputs = append(outputs, outputJSON)
	}
	return marshal.Outputs(outputs)
}

// applyExcludes clears each requested field using the provided setter map.
func applyExcludes(excludeFields []string, setters map[string]func()) error {
	for _, field := range excludeFields {
		setter, ok := setters[field]
		if !ok {
			return errors.Errorf("invalid exclude field: %s", field)
		}
		setter()
	}
	return nil
}

// stringPtr returns a pointer to the given string value.
func stringPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// boolPtr returns a pointer to the given bool value.
func boolPtr(b bool) *bool {
	return &b
}

const (
	maxFileReadBytes   = 1 << 20 // 1MB — truncation threshold for file_read.
	maxFileWriteBytes  = 10 << 20 // 10MB — max content size for file_write.
	maxSearchFileBytes = 10 << 20 // 10MB — skip files larger than this in file_search.
)

var commitIdentity = &object.Signature{
	Name:  "eino-ext",
	Email: "eino-ext@users.noreply.github.com",
}

// validateFilePath resolves a relative path under cloneRoot and returns the
// absolute, cleaned path. It rejects:
//   - paths that escape cloneRoot after cleaning (contain ".." that resolves
//     outside the root)
//   - absolute paths and drive letters
//   - NUL bytes (which can truncate paths at the OS level)
//
// This is a purely lexical check. Callers MUST additionally call
// resolveSymlinkSafe to verify that no path component is a symlink that
// resolves outside cloneRoot.
//
// relPath may be empty (returns cloneRoot itself) or a relative path with
// forward slashes.
func validateFilePath(cloneRoot, relPath string) (string, error) {
	cleanRoot := filepath.Clean(cloneRoot)
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
	// Must be within cloneRoot.
	if cleaned != cleanRoot && !strings.HasPrefix(cleaned, cleanRoot+string(filepath.Separator)) {
		return "", errors.Errorf("path %q escapes clone directory %q", relPath, cloneRoot)
	}
	// Reject any remaining ".." segments (defense in depth).
	if strings.Contains(filepath.ToSlash(cleaned), "/../") || strings.HasSuffix(filepath.ToSlash(cleaned), "/..") {
		return "", errors.Errorf("path %q contains directory traversal", relPath)
	}
	return cleaned, nil
}

// isWithinPath returns true if path is equal to root or a descendant of root.
// Both paths are cleaned before comparison.
func isWithinPath(path, root string) bool {
	p := filepath.Clean(path)
	r := filepath.Clean(root)
	return p == r || strings.HasPrefix(p, r+string(filepath.Separator))
}

// resolveSymlinkSafe walks each component of fullPath from cloneRoot, rejecting
// any component that is a symlink. This prevents symlink-based directory
// traversal where a malicious repo contains a symlink (e.g., "link -> /etc")
// that would redirect file reads/writes outside the clone directory.
//
// If createDirs is true, missing intermediate directories are created (for
// file_write). If createDirs is false, missing components (including the final
// one) cause an error for intermediate components; a missing final component is
// allowed (the caller will get a not-exist error from the subsequent file
// operation).
//
// The returned path is verified to be within cloneRoot.
func resolveSymlinkSafe(cloneRoot, fullPath string, createDirs bool) (string, error) {
	cleanRoot := filepath.Clean(cloneRoot)
	cleanFull := filepath.Clean(fullPath)

	rel, err := filepath.Rel(cleanRoot, cleanFull)
	if err != nil {
		return "", errors.Wrapf(err, "failed to compute relative path for symlink check")
	}
	rel = filepath.ToSlash(rel)
	if rel == "." {
		return cleanRoot, nil
	}
	// Defense-in-depth: reject if the relative path escapes upward. validateFilePath
	// should have already caught this, but double-check.
	if strings.HasPrefix(rel, "../") || rel == ".." {
		return "", errors.Errorf("path escapes clone directory")
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
					if mkErr := os.Mkdir(next, 0o755); mkErr != nil {
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

// isBinary returns true if the first 8KB of data contain a null byte, which is
// the standard heuristic for detecting binary files.
func isBinary(data []byte) bool {
	n := len(data)
	if n > 8192 {
		n = 8192
	}
	return bytes.Contains(data[:n], []byte{0})
}

// ensureCloneExists stats the clone directory and returns a descriptive error
// if it does not exist, suggesting the user run github_repo_clone first.
func ensureCloneExists(clonePath_, owner, repo string) error {
	if _, err := os.Stat(clonePath_); err != nil {
		if os.IsNotExist(err) {
			return errors.Wrapf(err, "repository %s/%s is not cloned at %q; run github_repo_clone first", owner, repo, clonePath_)
		}
		return errors.Wrapf(err, "failed to stat clone directory %q", clonePath_)
	}
	return nil
}

// rejectDotGitPath returns an error if the path refers to the .git directory at
// any level. The path is cleaned before checking so that prefixes like "./" or
// "subdir/../" cannot bypass the check.
func rejectDotGitPath(path string) error {
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
