package file

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"emperror.dev/errors"
	"github.com/goccy/go-json"
	"github.com/pmezard/go-difflib/difflib"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/fileutil"
)

// maxPreviewFiles caps the file list in directory delete previews.
const maxPreviewFiles = 100

// targetStat is the result of a read-only (non-mutating) inspection of a path.
type targetStat struct {
	exists bool
	isDir  bool
	size   int64
	head   []byte // first maxRead bytes of an existing regular file
}

// statReadOnly walks the EXISTING components of fullPath under root, rejecting
// symlinks at any level, and returns the terminal stat without creating
// anything. A missing intermediate or terminal component yields exists=false
// (no error). fullPath may be absolute (under root) or relative to root.
func statReadOnly(root, fullPath string, maxRead int) (targetStat, error) {
	cleanRoot := filepath.Clean(root)
	rel := filepath.Clean(filepath.FromSlash(fullPath))
	if filepath.IsAbs(rel) {
		computed, err := filepath.Rel(cleanRoot, rel)
		if err != nil {
			return targetStat{}, errors.Wrap(err, "failed to compute relative path for read-only stat")
		}
		rel = computed
	}

	if rel == "." {
		fi, err := os.Lstat(cleanRoot)
		if err != nil {
			if os.IsNotExist(err) {
				return targetStat{exists: false}, nil
			}
			return targetStat{}, errors.Wrapf(err, "failed to stat path %q", cleanRoot)
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return targetStat{}, errors.Errorf("symlink at path component %q; symlinks are not allowed", cleanRoot)
		}
		return targetStat{exists: true, isDir: fi.IsDir(), size: fi.Size()}, nil
	}

	parts := strings.Split(filepath.ToSlash(rel), "/")
	current := cleanRoot
	for i, part := range parts {
		next := filepath.Join(current, part)
		fi, err := os.Lstat(next)
		if err != nil {
			if os.IsNotExist(err) {
				// The target below this missing component does not exist.
				return targetStat{exists: false}, nil
			}
			return targetStat{}, errors.Wrapf(err, "failed to stat path component %q", next)
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return targetStat{}, errors.Errorf("symlink at path component %q; symlinks are not allowed", next)
		}
		if i < len(parts)-1 {
			if !fi.IsDir() {
				return targetStat{}, errors.Errorf("path component %q is not a directory", next)
			}
			current = next
			continue
		}
		if fi.IsDir() {
			return targetStat{exists: true, isDir: true, size: fi.Size()}, nil
		}
		ts := targetStat{exists: true, size: fi.Size()}
		if maxRead > 0 && fi.Size() > 0 {
			head, readErr := readHead(next, maxRead)
			if readErr != nil {
				return targetStat{}, readErr
			}
			ts.head = head
		}
		return ts, nil
	}
	return targetStat{exists: false}, nil
}

// readHead reads up to maxRead bytes from the regular file at path.
func readHead(path string, maxRead int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to open file %q", path)
	}
	defer func() { _ = f.Close() }()

	head, err := io.ReadAll(io.LimitReader(f, int64(maxRead)))
	if err != nil {
		return nil, errors.Wrapf(err, "failed to read file %q", path)
	}
	return head, nil
}

// unifiedDiff returns a unified diff of oldContent -> newContent truncated to
// maxBytes on a UTF-8 rune boundary.
func unifiedDiff(oldContent, newContent string, maxBytes int) string {
	diff, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A:        difflib.SplitLines(oldContent),
		B:        difflib.SplitLines(newContent),
		FromFile: "before",
		ToFile:   "after",
		Context:  3,
	})
	if err != nil {
		return ""
	}
	return truncateUTF8(diff, maxBytes)
}

// truncateUTF8 truncates s to at most maxBytes without splitting a rune.
func truncateUTF8(s string, maxBytes int) string {
	if maxBytes <= 0 || len(s) <= maxBytes {
		return s
	}
	for maxBytes > 0 && !utf8.RuneStart(s[maxBytes]) {
		maxBytes--
	}
	return s[:maxBytes]
}

// writePreview builds the file_write dry-run preview JSON (read-only):
//
//	{"dryRun":true,"wouldWrite":{"path":..., "mode":"create|overwrite|append",
//	  "oldSize":N,"newSize":N,"diff":"..."}}
//
// append mode puts the appended tail in "diff" instead of a unified diff.
func writePreview(root, relPath, content string, appendMode bool, maxRead int) (string, error) {
	ts, err := statReadOnly(root, relPath, maxRead)
	if err != nil {
		return "", err
	}

	mode := "create"
	oldSize := int64(0)
	if ts.exists {
		mode = "overwrite"
		oldSize = ts.size
	}

	wouldWrite := map[string]any{
		"path":    relPath,
		"mode":    mode,
		"oldSize": oldSize,
		"newSize": int64(len(content)),
	}
	if appendMode {
		wouldWrite["mode"] = "append"
		wouldWrite["newSize"] = oldSize + int64(len(content))
		wouldWrite["diff"] = truncateUTF8(content, maxRead)
	} else {
		wouldWrite["diff"] = unifiedDiff(string(ts.head), content, maxRead)
	}

	preview, err := json.Marshal(map[string]any{
		"dryRun":     true,
		"wouldWrite": wouldWrite,
	})
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal dry-run preview")
	}
	return string(preview), nil
}

// transferPreview builds the file_copy/file_move dry-run preview JSON
// (read-only), where key is "wouldCopy" or "wouldMove":
//
//	{"dryRun":true,key:{"source":..., "destination":..., "type":"file|dir",
//	   "fileCount":N,"totalBytes":N,"destinationExists":bool}}
func transferPreview(root, source, destination, key string) (string, error) {
	srcFullPath, err := fileutil.ValidateRelativePath(root, source)
	if err != nil {
		return "", err
	}
	dstFullPath, err := fileutil.ValidateRelativePath(root, destination)
	if err != nil {
		return "", err
	}

	srcStat, err := statReadOnly(root, source, 0)
	if err != nil {
		return "", err
	}
	if !srcStat.exists {
		return "", errors.Errorf("source path %q not found", source)
	}

	cleanSrc := filepath.Clean(srcFullPath)
	cleanDst := filepath.Clean(dstFullPath)
	if cleanDst == cleanSrc {
		return "", errors.Errorf("source and destination resolve to the same path %q", source)
	}
	if strings.HasPrefix(cleanDst, cleanSrc+string(filepath.Separator)) {
		return "", errors.Errorf("destination %q is inside the source directory %q", destination, source)
	}

	// Read-only destination inspection rejects symlinks anywhere on the path
	// without creating anything.
	dstStat, err := statReadOnly(root, destination, 0)
	if err != nil {
		return "", err
	}

	would := map[string]any{
		"source":            source,
		"destination":       destination,
		"type":              "file",
		"fileCount":         1,
		"totalBytes":        srcStat.size,
		"destinationExists": dstStat.exists,
	}
	if srcStat.isDir {
		would["type"] = "dir"
		files, walkErr := fileutil.WalkDirFiles(srcFullPath, false)
		if walkErr != nil {
			return "", errors.Wrapf(walkErr, "failed to walk directory %q", source)
		}
		var total int64
		for _, rel := range files {
			fi, statErr := os.Stat(filepath.Join(srcFullPath, filepath.FromSlash(rel)))
			if statErr != nil {
				continue
			}
			total += fi.Size()
		}
		would["fileCount"] = len(files)
		would["totalBytes"] = total
	}

	preview, err := json.Marshal(map[string]any{
		"dryRun": true,
		key:      would,
	})
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal dry-run preview")
	}
	return string(preview), nil
}

// deletePreview builds the file_delete dry-run preview JSON (read-only):
//
//	{"dryRun":true,"wouldDelete":{"path":..., "type":"file|dir"[,"files":[...],
//	  "truncated":true]}}
//
// A directory's file list is capped at maxPreviewFiles.
func deletePreview(relPath string, isDir bool, safePath string) (string, error) {
	wouldDelete := map[string]any{
		"path": relPath,
		"type": "file",
	}
	if isDir {
		wouldDelete["type"] = "dir"
		files, err := fileutil.WalkDirFiles(safePath, false)
		if err != nil {
			return "", errors.Wrapf(err, "failed to walk directory %q", relPath)
		}
		if len(files) > maxPreviewFiles {
			files = files[:maxPreviewFiles]
			wouldDelete["truncated"] = true
		}
		wouldDelete["files"] = prefixedRelPaths(files, relPath)
	}

	preview, err := json.Marshal(map[string]any{
		"dryRun":      true,
		"wouldDelete": wouldDelete,
	})
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal dry-run preview")
	}
	return string(preview), nil
}

// prefixedRelPaths joins each relative path with prefix and normalizes the
// result to forward slashes.
func prefixedRelPaths(relPaths []string, prefix string) []string {
	paths := make([]string, len(relPaths))
	for i, p := range relPaths {
		paths[i] = filepath.ToSlash(filepath.Join(prefix, p))
	}
	return paths
}
