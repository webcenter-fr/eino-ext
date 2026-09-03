# fileutil — Filesystem safety helpers

`fileutil` provides pure filesystem safety helpers shared across components:
path validation under a root directory, symlink rejection at every path
component, binary-content detection, VCS metadata exclusion, file and directory
tree copying, and host-side root directory validation. It contains no business
logic; callers wrap returned errors with component-specific context using
`emperror.dev/errors`.

## Size limits

```go
const DefaultMaxReadBytes       = 1 << 20  // 1MB  — truncation threshold for reads
const DefaultMaxWriteBytes      = 10 << 20 // 10MB — max content size for writes
const DefaultMaxSearchFileBytes = 10 << 20 // 10MB — skip files larger than this in search
```

## Functions

### Path validation

```go
func ValidateRelativePath(root, relPath string) (string, error)
func IsWithinPath(root, path string) bool
func ResolveSymlinkSafe(root, fullPath string, createDirs bool) (string, error)
```

- `ValidateRelativePath` — resolves a relative path under root and returns the
  absolute, cleaned path. Rejects paths escaping root after cleaning, absolute
  paths, and NUL bytes. Purely lexical.
- `IsWithinPath` — reports whether path equals root or is a descendant of root.
- `ResolveSymlinkSafe` — walks every component of fullPath from root, rejecting
  any symlink (prevents symlink-based traversal, CWE-59 / CWE-22). With
  `createDirs=true`, missing intermediate directories are created for writes; a
  missing final component is always allowed (the subsequent file operation
  reports not-exist).

Always pair the two: `ValidateRelativePath` for the lexical check, then
`ResolveSymlinkSafe` for the on-disk symlink check.

### Content and metadata

```go
func IsBinary(data []byte) bool
func RejectDotGitPath(path string) error
func ApplyLineRange(content string, startLine, endLine int) (result string, actualStart, actualEnd int)
```

- `IsBinary` — true when the first 8KB of data contain a null byte.
- `RejectDotGitPath` — rejects any path with a `.git` component; the path is
  cleaned first so `./.git/HEAD` and `subdir/../.git/HEAD` cannot bypass it.
- `ApplyLineRange` — extracts a 1-indexed line range from content, clamping
  out-of-range bounds; returns the extracted text and the actual applied bounds.

### Root directory validation

```go
func ValidateRootDir(dir string) error
```

Rejects empty, relative, or `..`-containing paths, system directories
(`/`, `/etc`, `/bin`, `/usr`, `/var`, `/proc`, `/sys`, `/dev`, `/tmp`), and
paths under `/proc/`, `/sys/`, `/dev/`.

### Copy and walk

```go
func CopyFileContents(src, dst string) error
func CopyDir(src, dst string, skipDotGit bool) (fileCount int, totalBytes int64, err error)
func WalkDirFiles(root string, skipDotGit bool) ([]string, error)
```

- `CopyFileContents` — copies a single file, creating or truncating dst with
  permissions 0o644 (an existing dst keeps its own permissions).
- `CopyDir` — recursively copies a directory tree, creating destination
  directories as needed. Symlinks inside the tree are always skipped (os.Open
  follows symlinks, which would leak external content into the destination).
  `.git` directories are skipped when `skipDotGit` is true.
- `WalkDirFiles` — returns slash-normalized relative paths of all regular
  files in the tree, skipping symlinks (and `.git` when `skipDotGit` is true).

### Session and segment helpers

```go
func SessionFromContext(ctx context.Context, key string) string
func SanitizePathSegment(s string, fallback string) string
```

- `SessionFromContext` — reads the session ID from adk session values under
  `key`; empty string when absent or not a string.
- `SanitizePathSegment` — removes NUL bytes, path separators, and control
  characters, collapses `..` sequences, and substitutes `fallback` when the
  result would be empty or ".".

## Usage

```go
import "github.com/webcenter-fr/eino-ext/libs/toolkit/fileutil"

root := "/data/workdir"
fullPath, err := fileutil.ValidateRelativePath(root, relPath)
if err != nil {
    return err
}
safePath, err := fileutil.ResolveSymlinkSafe(root, fullPath, false)
if err != nil {
    return err
}
data, err := os.ReadFile(safePath)
if err != nil {
    return err
}
if fileutil.IsBinary(data) {
    return errors.Errorf("refusing to read binary file %q", relPath)
}
```
