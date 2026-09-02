# Plan: GitHub File Copy, Move, and Delete Tools (Unified File + Directory)

## Summary

Add three new write tools to the GitHub component at `components/tool/github/`:

- `github_file_delete` — delete a file or directory from the local clone
- `github_file_copy` — copy a file or directory within the local clone
- `github_file_move` — move/rename a file or directory within the local clone

Each tool handles **both files and directories** — there are no separate folder tools. This is consistent with `github_file_list` and `github_file_search`, which already handle both. The `github_file_*` naming convention is about the filesystem domain, not a restriction to regular files.

All three operate on the local clone only (no git commit/push). They follow the exact same patterns as existing file tools: path validation, symlink safety, dot-git rejection, clone existence check, DryRun/Confirmed gating, and the standard tool struct + constructor pattern.

## Design Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Git commit/push? | **No** — local filesystem only | These are primitive operations. Users compose them with `github_file_write` for commit+push. Keeps tools simple and orthogonal. |
| Handle directories? | **Yes** — each tool handles both files and dirs | Consistent with `github_file_list` and `github_file_search`. Fewer tools for the LLM agent to reason about. |
| Copy overwrite? | **Yes** — overwrite destination if it exists | For files: truncate. For dirs: merge (source files overwrite matching dest files; extra dest files remain). Matches `cp -r` semantics. |
| Copy directory size limit? | **50 MB total** per directory copy, **10 MB per individual file** | Per-file limit matches existing `maxFileWriteBytes`. Total cap prevents runaway copies. |
| Move cross-device? | **Fallback to copy+delete** | `os.Rename` fails across mount points. Fallback ensures robustness. For directories, use recursive copy then `os.RemoveAll`. |
| Source == dest? | **Error** | No-op is confusing; explicit error is clearer. |
| Symlink handling | **Reject** — all symlinks rejected | Consistent with all existing file tools. |
| `.git` directory | **Always rejected** | Consistent with all existing file tools. |
| Delete directory? | **Yes** — `os.RemoveAll` for recursive delete | Gated behind DryRun + Confirmed. DryRun preview lists every file that would be deleted. |
| Output type field | **`"type": "file"` or `"type": "dir"`** | So the agent knows what was operated on. |

## Constants to Add (in `helper.go`)

```go
const (
    maxDirCopyTotalBytes = 50 << 20 // 50MB total for directory copy
    // maxFileWriteBytes = 10 << 20 already exists for per-file limit
)
```

## Helper Functions to Add (in `helper.go`)

### `walkDirFiles(root string) ([]string, error)`

Walks a directory tree and returns relative paths (from `root`) of all regular files. Skips `.git` directories and symlinks. Used by DryRun previews for delete and copy.

```go
// walkDirFiles returns the relative paths (from root) of all regular files
// in the directory tree. Skips .git directories and symlinks.
func walkDirFiles(root string) ([]string, error) {
    var files []string
    err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
        if walkErr != nil {
            return walkErr
        }
        if path == root {
            return nil
        }
        if d.IsDir() && d.Name() == ".git" {
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
```

### `copyDir(src, dst string) (fileCount int, totalBytes int64, err error)`

Recursively copies a directory tree from `src` to `dst`. Creates destination directories. Copies each file with `io.Copy`. Enforces per-file 10MB limit and total 50MB limit. Returns file count and total bytes copied.

```go
// copyDir recursively copies the directory tree from src to dst.
// It enforces per-file and total size limits. Returns the number of files
// copied and total bytes written.
func copyDir(src, dst string) (fileCount int, totalBytes int64, err error) {
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
            if mkErr := os.MkdirAll(target, 0o755); mkErr != nil {
                return mkErr
            }
            return nil
        }

        // Check per-file size before copying.
        fi, statErr := os.Lstat(path)
        if statErr != nil {
            return statErr
        }
        if fi.Size() > maxFileWriteBytes {
            return errors.Errorf("file %q size %d bytes exceeds the per-file maximum %d bytes", rel, fi.Size(), maxFileWriteBytes)
        }

        srcFile, openErr := os.Open(path)
        if openErr != nil {
            return openErr
        }
        defer srcFile.Close()

        dstFile, createErr := os.Create(target)
        if createErr != nil {
            return createErr
        }
        defer dstFile.Close()

        written, copyErr := io.Copy(dstFile, srcFile)
        if copyErr != nil {
            return copyErr
        }

        totalBytes += written
        if totalBytes > maxDirCopyTotalBytes {
            return errors.Errorf("directory copy total size %d bytes exceeds the maximum %d bytes", totalBytes, maxDirCopyTotalBytes)
        }

        fileCount++
        return nil
    })
    return fileCount, totalBytes, err
}
```

Imports needed in `helper.go`: add `"io"` and `"io/fs"`.

---

## Files to Create

### 1. `components/tool/github/file_delete.go` (NEW)

#### Description constant

```go
const fileDeleteDescription = `
** General Purpose **
It deletes a file or directory from a previously cloned GitHub repository on the
local filesystem. This tool does NOT commit or push — use github_file_write to
persist changes.

** Output **
It returns a JSON object with the deleted path, type ("file" or "dir"), deletion
status, and branch.

** Important **
- The repository must already be cloned under <CloneDir>/<owner>/<repo>.
- Both regular files and directories can be deleted. Directory deletion is recursive.
- The .git directory is always rejected.
- Symlinks are always rejected.
- Path traversal attempts are rejected.
- Requires Confirmed=true. Use DryRun=true first to preview.
`
```

#### Params struct

```go
type FileDeleteParams struct {
    Instance  string `json:"instance"  validate:"required" jsonschema:"(required) The GitHub instance to connect to."`
    Owner     string `json:"owner"     validate:"required" jsonschema:"(required) Repository owner."`
    Repo      string `json:"repo"      validate:"required" jsonschema:"(required) Repository name."`
    Path      string `json:"path"      validate:"required" jsonschema:"(required) Relative file or directory path inside the cloned repo to delete."`
    Branch    string `json:"branch"    validate:"required" jsonschema:"(required) Target branch (for context; no checkout is performed)."`
    DryRun    bool   `json:"dryRun,omitempty"    jsonschema:"(optional) If true, preview the deletion without making changes."`
    Confirmed bool   `json:"confirmed,omitempty" jsonschema:"(optional) Must be true to actually execute. Set after approving the dry-run result."`
}
```

#### Output struct

```go
type FileDeleteOutput struct {
    Path    string `json:"path"`
    Type    string `json:"type"`    // "file" or "dir"
    Deleted bool   `json:"deleted"`
    Branch  string `json:"branch"`
}
```

#### Tool struct

```go
type FileDeleteTool struct {
    *baseTool
    tool.InvokableTool
}
```

#### Invoke method logic (ordered)

1. `validateParams(params)` — return error if validation fails
2. If `params.DryRun`:
   - Resolve clone path, ensure clone exists, reject `.git`, validate path, resolve symlinks
   - `os.Lstat(safePath)` to determine type
   - If directory: call `walkDirFiles(safePath)` to get list of all files
   - Return JSON: `{"dryRun": true, "wouldDelete": {"path": "<params.Path>", "type": "file|dir", "branch": "<params.Branch>"[, "files": ["rel/path1", ...]]}}`
   - The `"files"` field is present only for directories
3. `confirm.RequireConfirmationForAction("delete file", params.Confirmed)` — return error if not confirmed
4. `clonePath_ := clonePath(t.cloneDir, params.Owner, params.Repo)`
5. `ensureCloneExists(clonePath_, params.Owner, params.Repo)`
6. `rejectDotGitPath(params.Path)`
7. `fullPath, err := validateFilePath(clonePath_, params.Path)`
8. `safePath, err := resolveSymlinkSafe(clonePath_, fullPath, false)` (createDirs=false — we're deleting, not creating)
9. `fi, err := os.Lstat(safePath)` — if not found: error `"path %q not found in clone"`; if symlink: error (already caught by resolveSymlinkSafe, but defense in depth)
10. Determine type:
    - If `fi.IsDir()`: `err = os.RemoveAll(safePath)`; type = `"dir"`
    - Else: `err = os.Remove(safePath)`; type = `"file"`
11. If error: return wrapped error `"failed to delete %q"`
12. Marshal `FileDeleteOutput{Path: params.Path, Type: type, Deleted: true, Branch: params.Branch}` and return

#### DryRun preview format

For file:
```json
{"dryRun": true, "wouldDelete": {"path": "README.md", "type": "file", "branch": "main"}}
```

For directory:
```json
{"dryRun": true, "wouldDelete": {"path": "sub/", "type": "dir", "branch": "main", "files": ["sub/main.go", "sub/helper.go"]}}
```

#### Constructors

```go
func NewFileDeleteTool(ctx context.Context, configs Configs) (*FileDeleteTool, error) {
    base, err := newBaseTool(ctx, configs)
    if err != nil {
        return nil, err
    }
    return newFileDeleteTool(ctx, base)
}

func newFileDeleteTool(ctx context.Context, base *baseTool) (*FileDeleteTool, error) {
    t := &FileDeleteTool{baseTool: base}
    inv, err := utils.InferTool("github_file_delete", fileDeleteDescription, t.Invoke, utils.WithSchemaModifier(base.instanceSchemaModifier()))
    if err != nil {
        return nil, err
    }
    t.InvokableTool = inv
    return t, nil
}
```

#### Compile-time check

```go
var _ tool.InvokableTool = (*FileDeleteTool)(nil)
```

#### Imports needed

`context`, `os`, `emperror.dev/errors`, `github.com/cloudwego/eino/components/tool`, `github.com/cloudwego/eino/components/tool/utils`, `github.com/goccy/go-json`, `github.com/webcenter-fr/eino-ext/libs/toolkit/confirm`

---

### 2. `components/tool/github/file_copy.go` (NEW)

#### Description constant

```go
const fileCopyDescription = `
** General Purpose **
It copies a file or directory from a source path to a destination path within a
previously cloned GitHub repository on the local filesystem. This tool does NOT
commit or push — use github_file_write to persist changes.

** Output **
It returns a JSON object with the source, destination, type ("file" or "dir"),
copy status, branch, and (for directories) file count and total bytes.

** Important **
- The repository must already be cloned under <CloneDir>/<owner>/<repo>.
- Both regular files and directories can be copied.
- If the destination exists, it is overwritten (files: truncated; dirs: merged).
- Destination parent directories are created if they don't exist.
- Per-file size limit: 10 MB. Directory total size limit: 50 MB.
- The .git directory is always rejected.
- Symlinks are always rejected.
- Path traversal attempts are rejected.
- Source and destination must differ.
- Requires Confirmed=true. Use DryRun=true first to preview.
`
```

#### Params struct

```go
type FileCopyParams struct {
    Instance    string `json:"instance"    validate:"required" jsonschema:"(required) The GitHub instance to connect to."`
    Owner       string `json:"owner"       validate:"required" jsonschema:"(required) Repository owner."`
    Repo        string `json:"repo"        validate:"required" jsonschema:"(required) Repository name."`
    Source      string `json:"source"      validate:"required" jsonschema:"(required) Relative source file or directory path inside the cloned repo."`
    Destination string `json:"destination" validate:"required" jsonschema:"(required) Relative destination file or directory path inside the cloned repo."`
    Branch      string `json:"branch"      validate:"required" jsonschema:"(required) Target branch (for context; no checkout is performed)."`
    DryRun      bool   `json:"dryRun,omitempty"    jsonschema:"(optional) If true, preview the copy without making changes."`
    Confirmed   bool   `json:"confirmed,omitempty" jsonschema:"(optional) Must be true to actually execute. Set after approving the dry-run result."`
}
```

#### Output struct

```go
type FileCopyOutput struct {
    Source      string `json:"source"`
    Destination string `json:"destination"`
    Type        string `json:"type"`                  // "file" or "dir"
    Copied      bool   `json:"copied"`
    Branch      string `json:"branch"`
    FileCount   int    `json:"fileCount,omitempty"`   // only for directory copies
    TotalBytes  int64  `json:"totalBytes,omitempty"`  // only for directory copies
}
```

#### Tool struct

```go
type FileCopyTool struct {
    *baseTool
    tool.InvokableTool
}
```

#### Invoke method logic (ordered)

1. `validateParams(params)`
2. If `params.Source == params.Destination`: error `"source and destination are the same path %q"`
3. If `params.DryRun`:
   - Resolve clone path, ensure clone exists, reject `.git` on both source and dest, validate both paths
   - Resolve symlinks on source (createDirs=false)
   - `os.Lstat(srcSafePath)` to determine type
   - If directory: call `walkDirFiles(srcSafePath)` to get file list
   - Return JSON: `{"dryRun": true, "wouldCopy": {"source": "<params.Source>", "destination": "<params.Destination>", "type": "file|dir", "branch": "<params.Branch>"[, "files": ["rel/path1", ...]]}}`
4. `confirm.RequireConfirmationForAction("copy file", params.Confirmed)`
5. `clonePath_ := clonePath(t.cloneDir, params.Owner, params.Repo)`
6. `ensureCloneExists(clonePath_, params.Owner, params.Repo)`
7. `rejectDotGitPath(params.Source)` and `rejectDotGitPath(params.Destination)`
8. Validate source path:
   - `srcFullPath, err := validateFilePath(clonePath_, params.Source)`
   - `srcSafePath, err := resolveSymlinkSafe(clonePath_, srcFullPath, false)`
   - `fi, err := os.Lstat(srcSafePath)` — not found → error `"source path %q not found in clone"`; symlink → error (defense in depth)
   - Determine `isDir := fi.IsDir()`
9. Validate destination path:
   - `dstFullPath, err := validateFilePath(clonePath_, params.Destination)`
   - `dstSafePath, err := resolveSymlinkSafe(clonePath_, dstFullPath, true)` (createDirs=true for parent dirs)
   - If destination exists: `os.Lstat(dstSafePath)` — if symlink → error; if it's a dir but source is a file → error `"destination %q is a directory but source is a file"`; if it's a file but source is a dir → error `"destination %q is a file but source is a directory"`
10. If source is a **file**:
    - Check size: if `fi.Size() > maxFileWriteBytes` → error `"source file %q size %d bytes exceeds the maximum %d bytes"`
    - `srcFile, err := os.Open(srcSafePath)`
    - `dstFile, err := os.Create(dstSafePath)` (truncates if exists)
    - `written, err := io.Copy(dstFile, srcFile)`
    - Close both files (defer or explicit)
    - If copy error: clean up partial destination with `os.Remove(dstSafePath)` (best effort, log but don't fail on cleanup error)
    - Marshal `FileCopyOutput{Source: params.Source, Destination: params.Destination, Type: "file", Copied: true, Branch: params.Branch}`
11. If source is a **directory**:
    - `fileCount, totalBytes, err := copyDir(srcSafePath, dstSafePath)`
    - If error: return wrapped error (partial state may exist at destination)
    - Marshal `FileCopyOutput{Source: params.Source, Destination: params.Destination, Type: "dir", Copied: true, Branch: params.Branch, FileCount: fileCount, TotalBytes: totalBytes}`

#### DryRun preview format

For file:
```json
{"dryRun": true, "wouldCopy": {"source": "a.md", "destination": "b.md", "type": "file", "branch": "main"}}
```

For directory:
```json
{"dryRun": true, "wouldCopy": {"source": "src/", "destination": "dst/", "type": "dir", "branch": "main", "files": ["src/a.go", "src/b.go"]}}
```

#### Constructors

```go
func NewFileCopyTool(ctx context.Context, configs Configs) (*FileCopyTool, error) {
    base, err := newBaseTool(ctx, configs)
    if err != nil {
        return nil, err
    }
    return newFileCopyTool(ctx, base)
}

func newFileCopyTool(ctx context.Context, base *baseTool) (*FileCopyTool, error) {
    t := &FileCopyTool{baseTool: base}
    inv, err := utils.InferTool("github_file_copy", fileCopyDescription, t.Invoke, utils.WithSchemaModifier(base.instanceSchemaModifier()))
    if err != nil {
        return nil, err
    }
    t.InvokableTool = inv
    return t, nil
}
```

#### Compile-time check

```go
var _ tool.InvokableTool = (*FileCopyTool)(nil)
```

#### Imports needed

`context`, `io`, `os`, `emperror.dev/errors`, `github.com/cloudwego/eino/components/tool`, `github.com/cloudwego/eino/components/tool/utils`, `github.com/goccy/go-json`, `github.com/webcenter-fr/eino-ext/libs/toolkit/confirm`

---

### 3. `components/tool/github/file_move.go` (NEW)

#### Description constant

```go
const fileMoveDescription = `
** General Purpose **
It moves or renames a file or directory from a source path to a destination path
within a previously cloned GitHub repository on the local filesystem. This tool
does NOT commit or push — use github_file_write to persist changes.

** Output **
It returns a JSON object with the source, destination, type ("file" or "dir"),
move status, and branch.

** Important **
- The repository must already be cloned under <CloneDir>/<owner>/<repo>.
- Both regular files and directories can be moved/renamed.
- If the destination exists, it is overwritten.
- Destination parent directories are created if they don't exist.
- If os.Rename fails (e.g., cross-device), falls back to copy+delete.
- The .git directory is always rejected.
- Symlinks are always rejected.
- Path traversal attempts are rejected.
- Source and destination must differ.
- Requires Confirmed=true. Use DryRun=true first to preview.
`
```

#### Params struct

```go
type FileMoveParams struct {
    Instance    string `json:"instance"    validate:"required" jsonschema:"(required) The GitHub instance to connect to."`
    Owner       string `json:"owner"       validate:"required" jsonschema:"(required) Repository owner."`
    Repo        string `json:"repo"        validate:"required" jsonschema:"(required) Repository name."`
    Source      string `json:"source"      validate:"required" jsonschema:"(required) Relative source file or directory path inside the cloned repo."`
    Destination string `json:"destination" validate:"required" jsonschema:"(required) Relative destination file or directory path inside the cloned repo."`
    Branch      string `json:"branch"      validate:"required" jsonschema:"(required) Target branch (for context; no checkout is performed)."`
    DryRun      bool   `json:"dryRun,omitempty"    jsonschema:"(optional) If true, preview the move without making changes."`
    Confirmed   bool   `json:"confirmed,omitempty" jsonschema:"(optional) Must be true to actually execute. Set after approving the dry-run result."`
}
```

#### Output struct

```go
type FileMoveOutput struct {
    Source      string `json:"source"`
    Destination string `json:"destination"`
    Type        string `json:"type"`    // "file" or "dir"
    Moved       bool   `json:"moved"`
    Branch      string `json:"branch"`
}
```

#### Tool struct

```go
type FileMoveTool struct {
    *baseTool
    tool.InvokableTool
}
```

#### Invoke method logic (ordered)

1. `validateParams(params)`
2. If `params.Source == params.Destination`: error `"source and destination are the same path %q"`
3. If `params.DryRun`:
   - Resolve clone path, ensure clone exists, reject `.git` on both, validate both paths
   - Resolve symlinks on source (createDirs=false)
   - `os.Lstat(srcSafePath)` to determine type
   - If directory: call `walkDirFiles(srcSafePath)` to get file list
   - Return JSON: `{"dryRun": true, "wouldMove": {"source": "<params.Source>", "destination": "<params.Destination>", "type": "file|dir", "branch": "<params.Branch>"[, "files": ["rel/path1", ...]]}}`
4. `confirm.RequireConfirmationForAction("move file", params.Confirmed)`
5. `clonePath_ := clonePath(t.cloneDir, params.Owner, params.Repo)`
6. `ensureCloneExists(clonePath_, params.Owner, params.Repo)`
7. `rejectDotGitPath(params.Source)` and `rejectDotGitPath(params.Destination)`
8. Validate source path (same as copy):
   - `srcFullPath, err := validateFilePath(clonePath_, params.Source)`
   - `srcSafePath, err := resolveSymlinkSafe(clonePath_, srcFullPath, false)`
   - `fi, err := os.Lstat(srcSafePath)` — not found → error; symlink → error
   - Determine `isDir := fi.IsDir()`
9. Validate destination path (same as copy):
   - `dstFullPath, err := validateFilePath(clonePath_, params.Destination)`
   - `dstSafePath, err := resolveSymlinkSafe(clonePath_, dstFullPath, true)` (createDirs=true)
   - If destination exists: `os.Lstat(dstSafePath)` — symlink → error; type mismatch (file vs dir) → error
10. Attempt `os.Rename(srcSafePath, dstSafePath)`
11. If `os.Rename` succeeds → go to output
12. If `os.Rename` fails with cross-device error (`syscall.EXDEV`):
    - Check: `if linkErr, ok := err.(*os.LinkError); ok && linkErr.Err == syscall.EXDEV`
    - If source is a **file**: open source, create destination, `io.Copy`, close both. If copy succeeds: `os.Remove(srcSafePath)`. If copy fails: clean up partial destination, return error. If remove fails after successful copy: return error noting source was copied but not removed (partial state).
    - If source is a **directory**: call `copyDir(srcSafePath, dstSafePath)`. If copy succeeds: `os.RemoveAll(srcSafePath)`. If copy fails: return error. If remove fails after successful copy: return error noting source was copied but not removed.
13. If `os.Rename` fails for any other reason: return wrapped error
14. Marshal `FileMoveOutput{Source: params.Source, Destination: params.Destination, Type: type, Moved: true, Branch: params.Branch}` and return

#### DryRun preview format

For file:
```json
{"dryRun": true, "wouldMove": {"source": "old.md", "destination": "new.md", "type": "file", "branch": "main"}}
```

For directory:
```json
{"dryRun": true, "wouldMove": {"source": "olddir/", "destination": "newdir/", "type": "dir", "branch": "main", "files": ["olddir/a.go", "olddir/b.go"]}}
```

#### Constructors

```go
func NewFileMoveTool(ctx context.Context, configs Configs) (*FileMoveTool, error) {
    base, err := newBaseTool(ctx, configs)
    if err != nil {
        return nil, err
    }
    return newFileMoveTool(ctx, base)
}

func newFileMoveTool(ctx context.Context, base *baseTool) (*FileMoveTool, error) {
    t := &FileMoveTool{baseTool: base}
    inv, err := utils.InferTool("github_file_move", fileMoveDescription, t.Invoke, utils.WithSchemaModifier(base.instanceSchemaModifier()))
    if err != nil {
        return nil, err
    }
    t.InvokableTool = inv
    return t, nil
}
```

#### Compile-time check

```go
var _ tool.InvokableTool = (*FileMoveTool)(nil)
```

#### Imports needed

`context`, `io`, `os`, `syscall`, `emperror.dev/errors`, `github.com/cloudwego/eino/components/tool`, `github.com/cloudwego/eino/components/tool/utils`, `github.com/goccy/go-json`, `github.com/webcenter-fr/eino-ext/libs/toolkit/confirm`

---

## Files to Modify

### 4. `components/tool/github/helper.go` (MODIFIED)

Add to imports:
```go
"io"
"io/fs"
```

Add constants after the existing `maxSearchFileBytes` line:
```go
const (
    maxFileReadBytes    = 1 << 20  // 1MB — truncation threshold for file_read.
    maxFileWriteBytes   = 10 << 20 // 10MB — max content size for file_write.
    maxSearchFileBytes  = 10 << 20 // 10MB — skip files larger than this in file_search.
    maxDirCopyTotalBytes = 50 << 20 // 50MB — max total size for directory copy.
)
```

Add the `walkDirFiles` and `copyDir` helper functions (signatures and implementations as specified above).

### 5. `components/tool/github/registry.go` (MODIFIED)

**Add to `writeConstructors` slice** (before the closing `}`):
```go
func(ctx context.Context, b *baseTool) (tool.InvokableTool, error) { return newFileDeleteTool(ctx, b) },
func(ctx context.Context, b *baseTool) (tool.InvokableTool, error) { return newFileCopyTool(ctx, b) },
func(ctx context.Context, b *baseTool) (tool.InvokableTool, error) { return newFileMoveTool(ctx, b) },
```

**Add to `WriteToolNames()` return slice** (before the closing `}`):
```go
"github_file_delete",
"github_file_copy",
"github_file_move",
```

**Add compile-time checks** (before the closing `var (` block):
```go
_ tool.InvokableTool = (*FileDeleteTool)(nil)
_ tool.InvokableTool = (*FileCopyTool)(nil)
_ tool.InvokableTool = (*FileMoveTool)(nil)
```

### 6. `components/tool/github/file_test.go` (MODIFIED)

Add test functions to the existing `GitHubToolTestSuite`. All tests follow the existing pattern: use `s.setupClone()`, `s.fileConfigs(cloneDir)`, `tool.InvokableRun(ctx, jsonString)`, and `s.NoError`/`s.Error`/`s.Contains` assertions.

#### Delete Tests

| Test Name | Scenario | Assertions |
|-----------|----------|------------|
| `TestFileDeleteFile` | Delete `README.md` | File removed from disk, output has `Type: "file"`, `Deleted: true` |
| `TestFileDeleteFileDryRun` | DryRun=true on `README.md` | Returns preview JSON with `"type":"file"`, file still exists on disk |
| `TestFileDeleteNotConfirmed` | Confirmed=false, no DryRun | Error contains "Confirmed" |
| `TestFileDeleteNotFound` | Delete nonexistent file | Error contains "not found" |
| `TestFileDeleteDirectory` | Delete `sub/` directory (contains `main.go`) | Directory and all contents gone, output has `Type: "dir"`, `Deleted: true` |
| `TestFileDeleteDirectoryDryRun` | DryRun=true on `sub/` | Returns preview JSON with `"type":"dir"` and `"files":["sub/main.go"]`, directory still exists |
| `TestFileDeleteDirectoryNested` | Create `sub/deep/` with files, delete `sub/` | Recursive delete works, all nested files gone |
| `TestFileDeleteDotGit` | Path `.git/HEAD` | Error contains ".git" |
| `TestFileDeleteDotGitDir` | Path `.git` | Error contains ".git" |
| `TestFileDeletePathTraversal` | Path `../../etc/passwd` | Error contains "escapes clone directory" |
| `TestFileDeleteSymlink` | Create symlink, try to delete through it | Error contains "symlink" |
| `TestFileDeleteNotCloned` | Clone dir doesn't exist | Error contains "github_repo_clone" |

#### Copy Tests

| Test Name | Scenario | Assertions |
|-----------|----------|------------|
| `TestFileCopyFile` | Copy `README.md` → `copy.md` | Both files exist, content matches, output `Type: "file"`, `Copied: true` |
| `TestFileCopyFileDryRun` | DryRun=true | Returns preview JSON, destination not created |
| `TestFileCopyNotConfirmed` | Confirmed=false | Error contains "Confirmed" |
| `TestFileCopySourceNotFound` | Source doesn't exist | Error contains "not found" |
| `TestFileCopySamePath` | Source == Destination | Error contains "same path" |
| `TestFileCopyFileOverwrite` | Copy over existing file | Destination content matches source |
| `TestFileCopyFileCreatesParentDirs` | Copy to `deep/nested/copy.md` | Parent dirs created, file copied |
| `TestFileCopyFileLargeFile` | Copy file >10MB | Error contains "exceeds the maximum" |
| `TestFileCopyDirectory` | Copy `sub/` → `sub2/` | Directory tree matches, output `Type: "dir"`, `FileCount` > 0, `TotalBytes` > 0 |
| `TestFileCopyDirectoryDryRun` | DryRun=true on directory | Returns preview with `"type":"dir"` and `"files"` list |
| `TestFileCopyDirectoryMerge` | Copy `sub/` to existing `sub2/` with extra files | Source files overwrite matching dest files, extra dest files remain |
| `TestFileCopyDirectoryCreatesParents` | Copy `sub/` to `deep/nested/sub/` | Full path created, directory copied |
| `TestFileCopyDirectoryTotalLimit` | Copy dir with total >50MB | Error contains "exceeds the maximum" |
| `TestFileCopyPathTraversalSource` | Source `../../etc/passwd` | Error contains "escapes clone directory" |
| `TestFileCopyPathTraversalDest` | Dest `../../etc/passwd` | Error contains "escapes clone directory" |
| `TestFileCopySymlinkSource` | Source through symlink | Error contains "symlink" |
| `TestFileCopyDotGit` | Dest `.git/evil` | Error contains ".git" |
| `TestFileCopyNotCloned` | Clone dir doesn't exist | Error contains "github_repo_clone" |
| `TestFileCopyTypeMismatch` | Copy file to existing dir path | Error about type mismatch |

#### Move Tests

| Test Name | Scenario | Assertions |
|-----------|----------|------------|
| `TestFileMoveFile` | Move `README.md` → `moved.md` | Source gone, dest exists, content matches, output `Type: "file"`, `Moved: true` |
| `TestFileMoveFileRename` | Move `README.md` → `README2.md` (same dir) | Source gone, dest exists |
| `TestFileMoveFileAcrossDirs` | Move `sub/main.go` → `main.go` | Source gone, dest at root |
| `TestFileMoveFileDryRun` | DryRun=true | Returns preview JSON, source still exists |
| `TestFileMoveNotConfirmed` | Confirmed=false | Error contains "Confirmed" |
| `TestFileMoveSourceNotFound` | Source doesn't exist | Error contains "not found" |
| `TestFileMoveSamePath` | Source == Destination | Error contains "same path" |
| `TestFileMoveFileOverwrite` | Move over existing file | Dest content matches source, source gone |
| `TestFileMoveFileCreatesParentDirs` | Move to `deep/nested/moved.md` | Parent dirs created, source gone |
| `TestFileMoveDirectory` | Move `sub/` → `sub2/` | Source gone, dest has all files, output `Type: "dir"` |
| `TestFileMoveDirectoryRename` | Move `sub/` → `renamed/` (same parent) | Source gone, dest exists with all files |
| `TestFileMoveDirectoryDryRun` | DryRun=true on directory | Returns preview with `"type":"dir"` and `"files"` list |
| `TestFileMovePathTraversalSource` | Source `../../etc/passwd` | Error contains "escapes clone directory" |
| `TestFileMovePathTraversalDest` | Dest `../../etc/passwd` | Error contains "escapes clone directory" |
| `TestFileMoveSymlinkSource` | Source through symlink | Error contains "symlink" |
| `TestFileMoveDotGit` | Dest `.git/evil` | Error contains ".git" |
| `TestFileMoveNotCloned` | Clone dir doesn't exist | Error contains "github_repo_clone" |
| `TestFileMoveTypeMismatch` | Move file to existing dir path | Error about type mismatch |

### 7. `components/tool/github/README.md` (MODIFIED)

**Add to the Write Tools table** (after `github_file_write`):
```markdown
| `github_file_delete` | Delete a file or directory from a cloned repo (local only, no commit/push) |
| `github_file_copy` | Copy a file or directory within a cloned repo (local only, no commit/push) |
| `github_file_move` | Move/rename a file or directory within a cloned repo (local only, no commit/push) |
```

**Update the File Tools workflow section** to mention the new tools:

Change:
```
4. `github_file_write` — modify files, commit, push
```
To:
```
4. `github_file_write` / `github_file_delete` / `github_file_copy` / `github_file_move` — modify files locally
5. `github_file_write` — commit and push changes
```

And renumber step 5 to step 6.

---

## Implementation Order

1. Add `walkDirFiles` and `copyDir` helpers and `maxDirCopyTotalBytes` constant to `helper.go`
2. Create `file_delete.go` (simplest, no cross-tool dependencies)
3. Create `file_copy.go` (medium complexity, uses `copyDir` helper)
4. Create `file_move.go` (depends on copy pattern for cross-device fallback)
5. Modify `registry.go` (add constructors, names, compile-time checks)
6. Modify `file_test.go` (add all test cases)
7. Modify `README.md` (document new tools)
8. Run `go build ./components/tool/github/...` and `go test ./components/tool/github/...`

## Validation Checklist

- [ ] All three tools compile without errors
- [ ] All new test cases pass
- [ ] Existing tests still pass (no regressions)
- [ ] `go vet ./components/tool/github/...` passes
- [ ] DryRun returns valid JSON preview without side effects for both files and directories
- [ ] Confirmed=false returns error without side effects
- [ ] Path traversal blocked for all three tools
- [ ] Symlink traversal blocked for all three tools
- [ ] `.git` access blocked for all three tools (including `.git` directory itself)
- [ ] Clone-not-found returns descriptive error suggesting `github_repo_clone`
- [ ] Copy handles binary files correctly (byte-identical)
- [ ] Copy directory preserves tree structure and file contents
- [ ] Copy directory merge behavior: source files overwrite matching dest files, extra dest files remain
- [ ] Copy directory enforces per-file 10MB limit and total 50MB limit
- [ ] Move handles cross-directory renames for both files and directories
- [ ] Move cross-device fallback works (copy+delete for files, recursive copy+delete for dirs)
- [ ] Copy overwrite works correctly for files
- [ ] Move overwrite works correctly for files
- [ ] Source == destination returns error for copy and move
- [ ] Type mismatch (file→dir or dir→file) returns error for copy and move
- [ ] Delete directory is recursive (nested subdirectories removed)
- [ ] Delete directory DryRun lists all files that would be deleted
- [ ] `WriteToolNames()` includes all three new tool names
- [ ] `NewAllTools` and `NewAllToolsWithSafety` include the new tools
- [ ] README updated with new tool entries

## Open Questions / Risks

- **Cross-device move fallback**: The `syscall.EXDEV` detection is Linux-specific. On Windows, the error type differs. Since this is a Linux-targeted codebase (per the environment), this is acceptable. If Windows support is needed later, add a platform-specific error check.
- **Atomicity of move fallback**: The copy+delete fallback is not atomic. If the process crashes between copy and delete, both files exist. This is documented as a known limitation in the tool description.
- **No git integration**: These tools don't commit or push. Users must call `github_file_write` separately. This is intentional to keep tools composable, but it means a delete+write workflow requires two tool calls. This is acceptable for an LLM agent workflow.
- **Directory copy merge semantics**: When copying a directory to an existing destination, source files overwrite matching destination files, but extra files in the destination are left untouched. This matches `cp -r` behavior and is documented in the tool description.
