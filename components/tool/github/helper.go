package github

import (
	"context"
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
	"github.com/webcenter-fr/eino-ext/libs/toolkit/fileutil"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/filter"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/marshal"
)

// CloneSessionKey is the adk session-value key under which the per-user-session
// clone namespace is stored. Callers set it at run start via:
//
//	adk.AddSessionValue(ctx, github.CloneSessionKey, sessionID)
const CloneSessionKey = "github_clone_session_id"

// defaultSession is the fallback clone namespace used when no session is set in
// the invocation context.
const defaultSession = "default"

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

// clonePath returns the safe, session-scoped local path for cloning a repository.
func clonePath(cloneDir, session, owner, repo string) string {
	if session == "" {
		session = defaultSession
	}
	return fmt.Sprintf("%s/%s/%s/%s",
		cloneDir,
		fileutil.SanitizePathSegment(session, "repo"),
		fileutil.SanitizePathSegment(owner, "repo"),
		fileutil.SanitizePathSegment(repo, "repo"))
}

// clonePathForSession returns the session-scoped clone path for owner/repo,
// deriving the session from the invocation context.
func (b *baseTool) clonePathForSession(ctx context.Context, owner, repo string) string {
	return clonePath(b.cloneDir, fileutil.SessionFromContext(ctx, CloneSessionKey), owner, repo)
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

// prefixedRelPaths joins each relative path with prefix and normalizes the
// result to forward slashes. Used by DryRun previews to display walked files
// relative to the clone root (e.g. "sub/main.go" for root "sub" and rel "main.go").
func prefixedRelPaths(relPaths []string, prefix string) []string {
	paths := make([]string, len(relPaths))
	for i, p := range relPaths {
		paths[i] = filepath.ToSlash(filepath.Join(prefix, p))
	}
	return paths
}

// transferDryRunPreview builds the DryRun preview JSON shared by the file copy
// and file move tools. previewKey is "wouldCopy" for github_file_copy and
// "wouldMove" for github_file_move. Both endpoints are validated (.git
// rejection, traversal, symlinks, source existence) without any side effect:
// no directory is created and no file is touched. The preview contains the
// source, destination, type ("file" or "dir"), branch, and — for directories —
// the list of files that would be transferred.
func transferDryRunPreview(clonePath_, source, destination, branch, previewKey string) (string, error) {
	if err := fileutil.RejectDotGitPath(source); err != nil {
		return "", err
	}
	if err := fileutil.RejectDotGitPath(destination); err != nil {
		return "", err
	}
	srcFullPath, err := fileutil.ValidateRelativePath(clonePath_, source)
	if err != nil {
		return "", err
	}
	if _, err := fileutil.ValidateRelativePath(clonePath_, destination); err != nil {
		return "", err
	}
	srcSafePath, err := fileutil.ResolveSymlinkSafe(clonePath_, srcFullPath, false)
	if err != nil {
		return "", err
	}
	fi, err := os.Lstat(srcSafePath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", errors.Wrapf(err, "source path %q not found in clone", source)
		}
		return "", errors.Wrapf(err, "failed to stat source path %q", source)
	}
	would := map[string]any{
		"source":      source,
		"destination": destination,
		"type":        "file",
		"branch":      branch,
	}
	if fi.IsDir() {
		would["type"] = "dir"
		files, err := fileutil.WalkDirFiles(srcSafePath, true)
		if err != nil {
			return "", errors.Wrapf(err, "failed to walk directory %q", source)
		}
		would["files"] = prefixedRelPaths(files, source)
	}
	preview, err := json.Marshal(map[string]any{
		"dryRun":   true,
		previewKey: would,
	})
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal dry-run preview")
	}
	return string(preview), nil
}

// validateTransferPaths validates the source and destination endpoints of a
// file copy or move. It rejects .git access, path traversal, symlinks, a
// missing source, and file/dir type mismatches between the endpoints.
// Destination parent directories are created as needed. It returns the safe
// source and destination paths and whether the source is a directory.
func validateTransferPaths(clonePath_, source, destination string) (srcSafePath, dstSafePath string, isDir bool, err error) {
	if err := fileutil.RejectDotGitPath(source); err != nil {
		return "", "", false, err
	}
	if err := fileutil.RejectDotGitPath(destination); err != nil {
		return "", "", false, err
	}
	srcFullPath, err := fileutil.ValidateRelativePath(clonePath_, source)
	if err != nil {
		return "", "", false, err
	}
	srcSafePath, err = fileutil.ResolveSymlinkSafe(clonePath_, srcFullPath, false)
	if err != nil {
		return "", "", false, err
	}
	srcFi, err := os.Lstat(srcSafePath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", "", false, errors.Wrapf(err, "source path %q not found in clone", source)
		}
		return "", "", false, errors.Wrapf(err, "failed to stat source path %q", source)
	}
	isDir = srcFi.IsDir()

	dstFullPath, err := fileutil.ValidateRelativePath(clonePath_, destination)
	if err != nil {
		return "", "", false, err
	}
	dstSafePath, err = fileutil.ResolveSymlinkSafe(clonePath_, dstFullPath, true)
	if err != nil {
		return "", "", false, err
	}
	// Reject aliased or nested endpoints. A destination that resolves to the
	// same path as the source (e.g. "sub/x" vs "sub/./x") would corrupt the
	// source in place, and a destination inside the source directory would
	// make fileutil.CopyDir write into the very tree it is walking, recursing
	// without bound until the OS path-length limit is hit.
	cleanSrc := filepath.Clean(srcSafePath)
	cleanDst := filepath.Clean(dstSafePath)
	if cleanDst == cleanSrc {
		return "", "", false, errors.Errorf("source and destination resolve to the same path %q", source)
	}
	if strings.HasPrefix(cleanDst, cleanSrc+string(filepath.Separator)) {
		return "", "", false, errors.Errorf("destination %q is inside the source directory %q; choose a destination outside of it", destination, source)
	}
	// Defense in depth: reject a symlink destination and file/dir type mismatches.
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

var commitIdentity = &object.Signature{
	Name:  "eino-ext",
	Email: "eino-ext@users.noreply.github.com",
}

// rejectCloneRootPath returns an error if the path refers to the clone root
// itself ("." after cleaning). Deleting the clone root would remove the entire
// clone, including the .git directory, destroying local history and breaking
// all subsequent git operations.
func rejectCloneRootPath(path string) error {
	if filepath.Clean(filepath.FromSlash(path)) == "." {
		return errors.Errorf("path %q refers to the clone root; deleting the entire clone is not allowed", path)
	}
	return nil
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
