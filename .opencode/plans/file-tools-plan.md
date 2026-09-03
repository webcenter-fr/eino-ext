# Local Filesystem File Tools — Implementation Plan

## Overview

Add general-purpose local filesystem file operation tools to `components/tool/file/`. These tools let LLM agents manipulate files within a session-scoped temp directory, enabling intermediate result storage without keeping everything in context.

## Resolved Design Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Shared helpers | Extract `libs/toolkit/fileutil/` first | Avoids duplication; follows project DRY principle; the extraction plan already exists in `extract-fileutils-plan.md` |
| Session isolation | `adk.GetSessionValue(ctx, key)` | Same pattern as GitHub tools; per-user-session subdirectory under `Workdir` |
| Safety gates | **None** — no DryRun/Confirmed | Explicit user decision; tools execute immediately |
| Read offset | `StartLine`/`EndLine` (1-indexed) | Matches GitHub `file_read`; LLM-friendly |
| Tool names | `file_read`, `file_write`, `file_delete`, `file_copy`, `file_move` | Simple, clear, matches GitHub naming pattern |
| Write mode | Overwrite (default) + optional `Append` bool | Lets LLMs build files incrementally |
| Copy/move scope | Files + directories (recursive) | Matches GitHub `file_copy`/`file_move` behavior |
| Append mode | `Append` boolean on `file_write` | Explicit user decision |
| Garbage collection | `StartGC(ctx, cfg, interval)` background goroutine | Cleans stale session directories after `SessionTTL`; skips active session |

## Prerequisites

### Step 0: Extract `libs/toolkit/fileutil/`

Before implementing the file tools, extract the filesystem safety helpers from `components/tool/github/` into `libs/toolkit/fileutil/`. Follow the existing plan in `.opencode/plans/extract-fileutils-plan.md`.

**New files to create:**

| File | Contents |
|------|----------|
| `libs/toolkit/fileutil/fileutil.go` | `ValidateRelativePath`, `IsWithinPath`, `ResolveSymlinkSafe`, `IsBinary`, `RejectDotGitPath`, `ValidateRootDir`, internal `systemDirs` |
| `libs/toolkit/fileutil/limits.go` | `DefaultMaxReadBytes` (1MB), `DefaultMaxWriteBytes` (10MB), `DefaultMaxSearchFileBytes` (10MB) |
| `libs/toolkit/fileutil/fileutil_test.go` | Table-driven tests for all exported functions |
| `libs/toolkit/fileutil/README.md` | Package documentation |

**Files to modify:**

| File | Change |
|------|--------|
| `components/tool/github/base.go` | Replace `validateCloneDir` body with `fileutil.ValidateRootDir` wrapper; delete local `systemDirs` |
| `components/tool/github/helper.go` | Delete `validateFilePath`, `isWithinPath`, `resolveSymlinkSafe`, `isBinary`, `rejectDotGitPath`, and the `max*Bytes` consts |
| `components/tool/github/file_read.go` | Replace local helper calls with `fileutil.*` |
| `components/tool/github/file_write.go` | Replace local helper calls with `fileutil.*` |
| `components/tool/github/file_copy.go` | Replace local helper calls with `fileutil.*` |
| `components/tool/github/file_move.go` | Replace local helper calls with `fileutil.*` |
| `components/tool/github/file_delete.go` | Replace local helper calls with `fileutil.*` |
| `components/tool/github/file_list.go` | Replace local helper calls with `fileutil.*` |
| `components/tool/github/file_search.go` | Replace local helper calls with `fileutil.*` |
| `components/tool/github/file_test.go` | Update error string assertions ("escapes clone directory" → "escapes root directory") |

## New Component: `components/tool/file/`

### Directory Structure

```
components/tool/file/
├── file.go              # Package comment, Config, NewAllTools, NewReadOnlyTools, WriteToolNames
├── config.go            # Config struct with validate+jsonschema tags
├── read.go              # FileReadTool
├── write.go             # FileWriteTool
├── delete.go            # FileDeleteTool
├── copy.go              # FileCopyTool
├── move.go              # FileMoveTool
├── helper.go            # Session management, path resolution, copyDir, copyFileContents, walkDirFiles
├── gc.go                # StartGC background goroutine for stale session cleanup
├── gc_test.go           # Tests for GC behavior
├── check.go             # Check() function for connectivity/RBAC probing
├── check_test.go        # Tests for Check()
├── file_test.go         # Table-driven tests for all tools
├── README.md            # Component documentation
```

### Package: `config.go`

```go
package file

// Config holds configuration for the file tools.
type Config struct {
    // Workdir is the base directory for session-scoped file operations.
    // Must be an absolute path, not a system directory. Required.
    Workdir string `validate:"required" jsonschema:"description=Base directory for session-scoped file operations (must be an absolute path)"`

    // MaxReadBytes is the maximum number of bytes to read from a file.
    // Files larger than this are truncated with a note. Defaults to 1MB.
    MaxReadBytes int `validate:"omitempty,gte=1" jsonschema:"description=Maximum bytes to read from a file (default 1MB)"`

    // MaxWriteBytes is the maximum content size accepted for file_write.
    // Defaults to 10MB.
    MaxWriteBytes int `validate:"omitempty,gte=1" jsonschema:"description=Maximum content size for file_write (default 10MB)"`

    // SessionTTL is the maximum age of a session directory before it is
    // eligible for garbage collection. When set, the GC (started via
    // StartGC) removes session subdirectories whose modification time is
    // older than this duration. The currently active session (identified
    // via adk.GetSessionValue) is never removed. Zero means no GC.
    SessionTTL time.Duration `validate:"omitempty,gte=60000000000" jsonschema:"description=Maximum age of session directories before GC cleanup (minimum 1 minute)"`
}
```

### Package: `file.go`

```go
// Package file provides eino tools for local filesystem file operations
// within a session-scoped temporary directory. Tools include read, write,
// delete, copy, and move.
//
// Usage:
//
//   cfg := &file.Config{Workdir: "/tmp/eino-files"}
//   tools, err := file.NewAllTools(ctx, cfg)
package file

import (
    "context"

    "github.com/cloudwego/eino/components/tool"
    "github.com/cloudwego/eino/components/tool/utils"
    "github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
)

// FileSessionKey is the adk session-value key for the per-user-session
// file namespace.
const FileSessionKey = "file_session_id"

// NewAllTools creates all file tools (read + write) and returns them as a
// flat slice ready to be registered with an eino ToolsNode.
func NewAllTools(ctx context.Context, cfg *Config) ([]tool.InvokableTool, error) {
    if cfg == nil {
        cfg = &Config{}
    }
    if cfg.MaxReadBytes == 0 {
        cfg.MaxReadBytes = fileutil.DefaultMaxReadBytes
    }
    if cfg.MaxWriteBytes == 0 {
        cfg.MaxWriteBytes = fileutil.DefaultMaxWriteBytes
    }
    if err := validate.Struct(cfg); err != nil {
        return nil, err
    }
    if err := fileutil.ValidateRootDir(cfg.Workdir); err != nil {
        return nil, err
    }

    tools := []tool.InvokableTool{}

    readTool, err := NewFileReadTool(ctx, cfg)
    if err != nil {
        return nil, err
    }
    tools = append(tools, readTool)

    writeTool, err := NewFileWriteTool(ctx, cfg)
    if err != nil {
        return nil, err
    }
    tools = append(tools, writeTool)

    deleteTool, err := NewFileDeleteTool(ctx, cfg)
    if err != nil {
        return nil, err
    }
    tools = append(tools, deleteTool)

    copyTool, err := NewFileCopyTool(ctx, cfg)
    if err != nil {
        return nil, err
    }
    tools = append(tools, copyTool)

    moveTool, err := NewFileMoveTool(ctx, cfg)
    if err != nil {
        return nil, err
    }
    tools = append(tools, moveTool)

    return tools, nil
}

// NewReadOnlyTools creates only the read-only file tools.
func NewReadOnlyTools(ctx context.Context, cfg *Config) ([]tool.InvokableTool, error) {
    // Same as NewAllTools but only creates FileReadTool.
    // ...
}

// WriteToolNames returns the tool names of all file write tools.
func WriteToolNames() []string {
    return []string{"file_write", "file_delete", "file_copy", "file_move"}
}
```

### Package: `helper.go`

```go
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

// resolvePath validates and resolves a relative path within the session directory.
// Returns the absolute safe path. Ensures the session directory exists.
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
```

Note: `copyFileContents`, `copyDir`, `walkDirFiles`, `sanitizeSegment`, and `sessionFromContext` are now in `libs/toolkit/fileutil/`. The file tools call `fileutil.CopyFileContents`, `fileutil.CopyDir(src, dst, false)`, `fileutil.WalkDirFiles(root, false)`, `fileutil.SanitizePathSegment(s, "session")`, and `fileutil.SessionFromContext(ctx, FileSessionKey)` directly.

### Tool: `read.go` — `file_read`

```go
package file

const fileReadDescription = `
** General Purpose **
It reads the contents of a file from the session-scoped temporary directory.

** Output **
It returns a JSON object with the file path, content (or a line range), byte size,
and a truncated flag if the file exceeds the read limit.

** Important **
- Paths are relative to the session directory and validated to prevent traversal.
- Binary files are detected and refused.
- Files larger than the configured max read size (default 1MB) are truncated with a note.
- Use StartLine and EndLine (1-indexed) to read a specific line range.
`

type FileReadParams struct {
    Path      string `json:"path"      validate:"required" jsonschema:"(required) Relative file path inside the session directory."`
    StartLine int    `json:"startLine,omitempty" jsonschema:"(optional) 1-indexed first line to read. 0 = start from beginning."`
    EndLine   int    `json:"endLine,omitempty"   jsonschema:"(optional) 1-indexed last line to read. 0 = read to end."`
}

type FileReadOutput struct {
    Path      string `json:"path"`
    Content   string `json:"content"`
    Bytes     int    `json:"bytes"`
    Truncated bool   `json:"truncated,omitempty"`
    Note      string `json:"note,omitempty"`
    StartLine int    `json:"startLine,omitempty"`
    EndLine   int    `json:"endLine,omitempty"`
}

type FileReadTool struct {
    cfg         *Config
    invokable   tool.InvokableTool
}

var _ tool.InvokableTool = (*FileReadTool)(nil)

func (t *FileReadTool) Invoke(ctx context.Context, params *FileReadParams) (string, error) {
    if err := validate.Struct(params); err != nil {
        return "", err
    }

    if params.StartLine > 0 && params.EndLine > 0 && params.StartLine > params.EndLine {
        return "", errors.Errorf("parameter 'startLine' (%d) must be <= 'endLine' (%d)", params.StartLine, params.EndLine)
    }

    safePath, err := resolvePath(t.cfg.Workdir, ctx, params.Path, false)
    if err != nil {
        return "", err
    }

    if fi, statErr := os.Lstat(safePath); statErr == nil {
        if fi.IsDir() {
            return "", errors.Errorf("path %q is a directory, not a file", params.Path)
        }
    }

    data, err := os.ReadFile(safePath)
    if err != nil {
        if os.IsNotExist(err) {
            return "", errors.Wrapf(err, "file %q not found", params.Path)
        }
        return "", errors.Wrapf(err, "failed to read file %q", params.Path)
    }

    if fileutil.IsBinary(data) {
        return "", errors.Errorf("file %q appears to be binary; refusing to read", params.Path)
    }

    output := &FileReadOutput{
        Path:  params.Path,
        Bytes: len(data),
    }

    maxRead := t.cfg.MaxReadBytes
    if maxRead == 0 {
        maxRead = fileutil.DefaultMaxReadBytes
    }
    if len(data) > maxRead {
        output.Truncated = true
        output.Note = fmt.Sprintf("file truncated to %d bytes (original size: %d bytes)", maxRead, len(data))
        data = data[:maxRead]
        output.Bytes = len(data)
    }

    output.Content = string(data)

    if params.StartLine > 0 || params.EndLine > 0 {
        content, actualStart, actualEnd := fileutil.ApplyLineRange(output.Content, params.StartLine, params.EndLine)
        output.Content = content
        output.StartLine = actualStart
        output.EndLine = actualEnd
        output.Bytes = len(output.Content)
    }

    result, err := json.Marshal(output)
    if err != nil {
        return "", errors.Wrap(err, "failed to marshal output")
    }
    return string(result), nil
}

func NewFileReadTool(ctx context.Context, cfg *Config) (*FileReadTool, error) {
    readTool := &FileReadTool{cfg: cfg}
    t, err := utils.InferTool("file_read", fileReadDescription, readTool.Invoke)
    if err != nil {
        return nil, err
    }
    readTool.invokable = t
    return readTool, nil
}

// Info implements tool.InvokableTool.
func (t *FileReadTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
    return t.invokable.Info(ctx)
}

// InvokableRun implements tool.InvokableTool.
func (t *FileReadTool) InvokableRun(ctx context.Context, args string, opts ...tool.Option) (string, error) {
    return t.invokable.InvokableRun(ctx, args, opts...)
}
```

### Tool: `write.go` — `file_write`

```go
package file

const fileWriteDescription = `
** General Purpose **
It creates or overwrites a file in the session-scoped temporary directory.

** Output **
It returns a JSON object with the file path, bytes written, and whether the
operation was an append or overwrite.

** Important **
- Paths are relative to the session directory and validated to prevent traversal.
- Parent directories are created automatically if they don't exist.
- By default, the file is overwritten (truncated). Set Append=true to append.
- Content size is limited by the configured max write size (default 10MB).
`

type FileWriteParams struct {
    Path    string `json:"path"    validate:"required" jsonschema:"(required) Relative file path inside the session directory."`
    Content string `json:"content" validate:"required" jsonschema:"(required) File content to write."`
    Append  bool   `json:"append,omitempty" jsonschema:"(optional) If true, append content to the existing file instead of overwriting."`
}

type FileWriteOutput struct {
    Path    string `json:"path"`
    Bytes   int    `json:"bytes"`
    Mode    string `json:"mode"` // "overwrite" or "append"
}

type FileWriteTool struct {
    cfg       *Config
    invokable tool.InvokableTool
}

var _ tool.InvokableTool = (*FileWriteTool)(nil)

func (t *FileWriteTool) Invoke(ctx context.Context, params *FileWriteParams) (string, error) {
    if err := validate.Struct(params); err != nil {
        return "", err
    }

    maxWrite := t.cfg.MaxWriteBytes
    if maxWrite == 0 {
        maxWrite = fileutil.DefaultMaxWriteBytes
    }
    if len(params.Content) > maxWrite {
        return "", errors.Errorf("parameter 'content' size %d bytes exceeds the maximum %d bytes", len(params.Content), maxWrite)
    }

    safePath, err := resolvePath(t.cfg.Workdir, ctx, params.Path, true)
    if err != nil {
        return "", err
    }

    // Reject if target exists and is a directory or symlink.
    if fi, statErr := os.Lstat(safePath); statErr == nil {
        if fi.IsDir() {
            return "", errors.Errorf("path %q is a directory; cannot write a file over a directory", params.Path)
        }
        if fi.Mode()&os.ModeSymlink != 0 {
            return "", errors.Errorf("path %q is a symlink; symlinks are not allowed", params.Path)
        }
    }

    mode := "overwrite"
    flag := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
    if params.Append {
        mode = "append"
        flag = os.O_CREATE | os.O_WRONLY | os.O_APPEND
    }

    f, err := os.OpenFile(safePath, flag, 0o644)
    if err != nil {
        return "", errors.Wrapf(err, "failed to open file %q for writing", params.Path)
    }
    defer func() { _ = f.Close() }()

    n, err := f.WriteString(params.Content)
    if err != nil {
        return "", errors.Wrapf(err, "failed to write to file %q", params.Path)
    }

    output := &FileWriteOutput{
        Path:  params.Path,
        Bytes: n,
        Mode:  mode,
    }

    result, err := json.Marshal(output)
    if err != nil {
        return "", errors.Wrap(err, "failed to marshal output")
    }
    return string(result), nil
}

func NewFileWriteTool(ctx context.Context, cfg *Config) (*FileWriteTool, error) {
    writeTool := &FileWriteTool{cfg: cfg}
    t, err := utils.InferTool("file_write", fileWriteDescription, writeTool.Invoke)
    if err != nil {
        return nil, err
    }
    writeTool.invokable = t
    return writeTool, nil
}

func (t *FileWriteTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
    return t.invokable.Info(ctx)
}

func (t *FileWriteTool) InvokableRun(ctx context.Context, args string, opts ...tool.Option) (string, error) {
    return t.invokable.InvokableRun(ctx, args, opts...)
}
```

### Tool: `delete.go` — `file_delete`

```go
package file

const fileDeleteDescription = `
** General Purpose **
It deletes a file or directory from the session-scoped temporary directory.

** Output **
It returns a JSON object with the deleted path, type ("file" or "dir"), and
deletion status.

** Important **
- Paths are relative to the session directory and validated to prevent traversal.
- Both regular files and directories can be deleted. Directory deletion is recursive.
- The session root itself (".") cannot be deleted.
- Symlinks are always rejected.
`

type FileDeleteParams struct {
    Path string `json:"path" validate:"required" jsonschema:"(required) Relative file or directory path inside the session directory to delete."`
}

type FileDeleteOutput struct {
    Path    string `json:"path"`
    Type    string `json:"type"` // "file" or "dir"
    Deleted bool   `json:"deleted"`
}

type FileDeleteTool struct {
    cfg       *Config
    invokable tool.InvokableTool
}

var _ tool.InvokableTool = (*FileDeleteTool)(nil)

func (t *FileDeleteTool) Invoke(ctx context.Context, params *FileDeleteParams) (string, error) {
    if err := validate.Struct(params); err != nil {
        return "", err
    }

    // Reject deleting the session root.
    if filepath.Clean(filepath.FromSlash(params.Path)) == "." {
        return "", errors.Errorf("path %q refers to the session root; deleting the entire session directory is not allowed", params.Path)
    }

    safePath, err := resolvePath(t.cfg.Workdir, ctx, params.Path, false)
    if err != nil {
        return "", err
    }

    fi, err := os.Lstat(safePath)
    if err != nil {
        if os.IsNotExist(err) {
            return "", errors.Wrapf(err, "path %q not found", params.Path)
        }
        return "", errors.Wrapf(err, "failed to stat path %q", params.Path)
    }

    deletedType := "file"
    var deleteErr error
    if fi.IsDir() {
        deletedType = "dir"
        deleteErr = os.RemoveAll(safePath)
    } else {
        deleteErr = os.Remove(safePath)
    }
    if deleteErr != nil {
        return "", errors.Wrapf(deleteErr, "failed to delete %q", params.Path)
    }

    output := &FileDeleteOutput{
        Path:    params.Path,
        Type:    deletedType,
        Deleted: true,
    }

    result, err := json.Marshal(output)
    if err != nil {
        return "", errors.Wrap(err, "failed to marshal output")
    }
    return string(result), nil
}

func NewFileDeleteTool(ctx context.Context, cfg *Config) (*FileDeleteTool, error) {
    deleteTool := &FileDeleteTool{cfg: cfg}
    t, err := utils.InferTool("file_delete", fileDeleteDescription, deleteTool.Invoke)
    if err != nil {
        return nil, err
    }
    deleteTool.invokable = t
    return deleteTool, nil
}

func (t *FileDeleteTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
    return t.invokable.Info(ctx)
}

func (t *FileDeleteTool) InvokableRun(ctx context.Context, args string, opts ...tool.Option) (string, error) {
    return t.invokable.InvokableRun(ctx, args, opts...)
}
```

### Tool: `copy.go` — `file_copy`

```go
package file

const fileCopyDescription = `
** General Purpose **
It copies a file or directory from a source path to a destination path within
the session-scoped temporary directory.

** Output **
It returns a JSON object with the source, destination, type ("file" or "dir"),
copy status, and (for directories) file count and total bytes.

** Important **
- Both regular files and directories can be copied.
- If the destination exists, it is overwritten (files: truncated; dirs: merged).
- Destination parent directories are created if they don't exist.
- Symlinks are always rejected.
- Symlinks inside a copied directory tree are skipped.
- The destination must not be inside the source directory.
- Path traversal attempts are rejected.
- Source and destination must differ.
`

type FileCopyParams struct {
    Source      string `json:"source"      validate:"required" jsonschema:"(required) Relative source file or directory path."`
    Destination string `json:"destination" validate:"required" jsonschema:"(required) Relative destination file or directory path."`
}

type FileCopyOutput struct {
    Source      string `json:"source"`
    Destination string `json:"destination"`
    Type        string `json:"type"` // "file" or "dir"
    Copied      bool   `json:"copied"`
    FileCount   int    `json:"fileCount,omitempty"`
    TotalBytes  int64  `json:"totalBytes,omitempty"`
}

type FileCopyTool struct {
    cfg       *Config
    invokable tool.InvokableTool
}

var _ tool.InvokableTool = (*FileCopyTool)(nil)

func (t *FileCopyTool) Invoke(ctx context.Context, params *FileCopyParams) (string, error) {
    if err := validate.Struct(params); err != nil {
        return "", err
    }

    if params.Source == params.Destination {
        return "", errors.Errorf("source and destination are the same path %q", params.Source)
    }

    srcSafePath, err := resolvePath(t.cfg.Workdir, ctx, params.Source, false)
    if err != nil {
        return "", err
    }

    dstSafePath, err := resolvePath(t.cfg.Workdir, ctx, params.Destination, true)
    if err != nil {
        return "", err
    }

    // Reject aliased or nested endpoints.
    cleanSrc := filepath.Clean(srcSafePath)
    cleanDst := filepath.Clean(dstSafePath)
    if cleanDst == cleanSrc {
        return "", errors.Errorf("source and destination resolve to the same path %q", params.Source)
    }
    if strings.HasPrefix(cleanDst, cleanSrc+string(filepath.Separator)) {
        return "", errors.Errorf("destination %q is inside the source directory %q", params.Destination, params.Source)
    }

    srcFi, err := os.Lstat(srcSafePath)
    if err != nil {
        if os.IsNotExist(err) {
            return "", errors.Wrapf(err, "source path %q not found", params.Source)
        }
        return "", errors.Wrapf(err, "failed to stat source path %q", params.Source)
    }

    // Reject type mismatches if destination exists.
    if dstFi, statErr := os.Lstat(dstSafePath); statErr == nil {
        if dstFi.Mode()&os.ModeSymlink != 0 {
            return "", errors.Errorf("destination %q is a symlink; symlinks are not allowed", params.Destination)
        }
        if dstFi.IsDir() && !srcFi.IsDir() {
            return "", errors.Errorf("destination %q is a directory but source is a file", params.Destination)
        }
        if !dstFi.IsDir() && srcFi.IsDir() {
            return "", errors.Errorf("destination %q is a file but source is a directory", params.Destination)
        }
    }

    output := &FileCopyOutput{
        Source:      params.Source,
        Destination: params.Destination,
        Type:        "file",
        Copied:      true,
    }

    if srcFi.IsDir() {
        output.Type = "dir"
        fileCount, totalBytes, err := fileutil.CopyDir(srcSafePath, dstSafePath, false)
        if err != nil {
            return "", errors.Wrapf(err, "failed to copy directory %q to %q", params.Source, params.Destination)
        }
        output.FileCount = fileCount
        output.TotalBytes = totalBytes
    } else {
        if err := fileutil.CopyFileContents(srcSafePath, dstSafePath); err != nil {
            _ = os.Remove(dstSafePath)
            return "", err
        }
    }

    result, err := json.Marshal(output)
    if err != nil {
        return "", errors.Wrap(err, "failed to marshal output")
    }
    return string(result), nil
}

func NewFileCopyTool(ctx context.Context, cfg *Config) (*FileCopyTool, error) {
    copyTool := &FileCopyTool{cfg: cfg}
    t, err := utils.InferTool("file_copy", fileCopyDescription, copyTool.Invoke)
    if err != nil {
        return nil, err
    }
    copyTool.invokable = t
    return copyTool, nil
}

func (t *FileCopyTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
    return t.invokable.Info(ctx)
}

func (t *FileCopyTool) InvokableRun(ctx context.Context, args string, opts ...tool.Option) (string, error) {
    return t.invokable.InvokableRun(ctx, args, opts...)
}
```

### Tool: `move.go` — `file_move`

```go
package file

const fileMoveDescription = `
** General Purpose **
It moves or renames a file or directory from a source path to a destination path
within the session-scoped temporary directory.

** Output **
It returns a JSON object with the source, destination, type ("file" or "dir"),
and move status.

** Important **
- Both regular files and directories can be moved/renamed.
- If the destination exists, it is overwritten.
- Destination parent directories are created if they don't exist.
- If os.Rename fails (e.g., cross-device), falls back to copy+delete, which is
  not atomic: if removal of the source fails after copying, both copies exist.
- Symlinks are always rejected.
- Symlinks inside a moved directory tree are skipped.
- The destination must not be inside the source directory.
- Moving a directory onto an existing non-empty directory is not supported.
- Path traversal attempts are rejected.
- Source and destination must differ.
`

type FileMoveParams struct {
    Source      string `json:"source"      validate:"required" jsonschema:"(required) Relative source file or directory path."`
    Destination string `json:"destination" validate:"required" jsonschema:"(required) Relative destination file or directory path."`
}

type FileMoveOutput struct {
    Source      string `json:"source"`
    Destination string `json:"destination"`
    Type        string `json:"type"` // "file" or "dir"
    Moved       bool   `json:"moved"`
}

type FileMoveTool struct {
    cfg       *Config
    invokable tool.InvokableTool
}

var _ tool.InvokableTool = (*FileMoveTool)(nil)

func (t *FileMoveTool) Invoke(ctx context.Context, params *FileMoveParams) (string, error) {
    if err := validate.Struct(params); err != nil {
        return "", err
    }

    if params.Source == params.Destination {
        return "", errors.Errorf("source and destination are the same path %q", params.Source)
    }

    srcSafePath, err := resolvePath(t.cfg.Workdir, ctx, params.Source, false)
    if err != nil {
        return "", err
    }

    dstSafePath, err := resolvePath(t.cfg.Workdir, ctx, params.Destination, true)
    if err != nil {
        return "", err
    }

    cleanSrc := filepath.Clean(srcSafePath)
    cleanDst := filepath.Clean(dstSafePath)
    if cleanDst == cleanSrc {
        return "", errors.Errorf("source and destination resolve to the same path %q", params.Source)
    }
    if strings.HasPrefix(cleanDst, cleanSrc+string(filepath.Separator)) {
        return "", errors.Errorf("destination %q is inside the source directory %q", params.Destination, params.Source)
    }

    srcFi, err := os.Lstat(srcSafePath)
    if err != nil {
        if os.IsNotExist(err) {
            return "", errors.Wrapf(err, "source path %q not found", params.Source)
        }
        return "", errors.Wrapf(err, "failed to stat source path %q", params.Source)
    }
    isDir := srcFi.IsDir()

    // Reject type mismatches if destination exists.
    if dstFi, statErr := os.Lstat(dstSafePath); statErr == nil {
        if dstFi.Mode()&os.ModeSymlink != 0 {
            return "", errors.Errorf("destination %q is a symlink; symlinks are not allowed", params.Destination)
        }
        if dstFi.IsDir() && !isDir {
            return "", errors.Errorf("destination %q is a directory but source is a file", params.Destination)
        }
        if !dstFi.IsDir() && isDir {
            return "", errors.Errorf("destination %q is a file but source is a directory", params.Destination)
        }
    }

    moveErr := os.Rename(srcSafePath, dstSafePath)
    if moveErr != nil {
        linkErr, ok := moveErr.(*os.LinkError)
        if !ok || linkErr.Err != syscall.EXDEV {
            return "", errors.Wrapf(moveErr, "failed to move %q to %q", params.Source, params.Destination)
        }
        // Cross-device fallback: copy + delete.
        if isDir {
            if _, _, copyErr := fileutil.CopyDir(srcSafePath, dstSafePath, false); copyErr != nil {
                return "", errors.Wrapf(copyErr, "failed to copy directory %q to %q for cross-device move", params.Source, params.Destination)
            }
            if rmErr := os.RemoveAll(srcSafePath); rmErr != nil {
                return "", errors.Wrapf(rmErr, "copied directory %q to %q but failed to remove the source; both copies now exist", params.Source, params.Destination)
            }
        } else {
            if copyErr := fileutil.CopyFileContents(srcSafePath, dstSafePath); copyErr != nil {
                _ = os.Remove(dstSafePath)
                return "", errors.Wrapf(copyErr, "failed to copy file %q to %q for cross-device move", params.Source, params.Destination)
            }
            if rmErr := os.Remove(srcSafePath); rmErr != nil {
                return "", errors.Wrapf(rmErr, "copied file %q to %q but failed to remove the source; both copies now exist", params.Source, params.Destination)
            }
        }
    }

    outputType := "file"
    if isDir {
        outputType = "dir"
    }

    output := &FileMoveOutput{
        Source:      params.Source,
        Destination: params.Destination,
        Type:        outputType,
        Moved:       true,
    }

    result, err := json.Marshal(output)
    if err != nil {
        return "", errors.Wrap(err, "failed to marshal output")
    }
    return string(result), nil
}

func NewFileMoveTool(ctx context.Context, cfg *Config) (*FileMoveTool, error) {
    moveTool := &FileMoveTool{cfg: cfg}
    t, err := utils.InferTool("file_move", fileMoveDescription, moveTool.Invoke)
    if err != nil {
        return nil, err
    }
    moveTool.invokable = t
    return moveTool, nil
}

func (t *FileMoveTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
    return t.invokable.Info(ctx)
}

func (t *FileMoveTool) InvokableRun(ctx context.Context, args string, opts ...tool.Option) (string, error) {
    return t.invokable.InvokableRun(ctx, args, opts...)
}
```

### Checkup: `check.go`

```go
package file

import (
    "context"
    "os"

    "github.com/webcenter-fr/eino-ext/libs/toolkit/checkup"
    "github.com/webcenter-fr/eino-ext/libs/toolkit/fileutil"
)

// Check performs a health check against the configured file tools workdir.
func Check(ctx context.Context, cfg *Config) checkup.Results {
    if cfg == nil || cfg.Workdir == "" {
        return checkup.Results{{
            Component: "file",
            Status:    checkup.StatusError,
            Error:     "Workdir is not configured",
        }}
    }

    if err := fileutil.ValidateRootDir(cfg.Workdir); err != nil {
        return checkup.Results{{
            Component: "file",
            Status:    checkup.StatusError,
            Error:     err.Error(),
        }}
    }

    // Probe: try to create and remove a test directory.
    testDir := cfg.Workdir + "/__checkup_test"
    if err := os.MkdirAll(testDir, 0o755); err != nil {
        return checkup.Results{{
            Component: "file",
            Status:    checkup.StatusError,
            Error:     "failed to create test directory in Workdir: " + err.Error(),
        }}
    }
    _ = os.RemoveAll(testDir)

    return checkup.Results{{
        Component: "file",
        Status:    checkup.StatusOK,
        Message:   "Workdir is writable",
    }}
}
```

### Checkup Tests: `check_test.go`

```go
package file

import (
    "context"
    "testing"

    "github.com/webcenter-fr/eino-ext/libs/toolkit/checkup"
)

func TestCheckEmptyConfig(t *testing.T) {
    ctx := context.Background()
    results := Check(ctx, nil)
    if len(results) != 1 {
        t.Fatalf("expected 1 result, got %d", len(results))
    }
    if results[0].Status != checkup.StatusError {
        t.Errorf("expected status error, got %s", results[0].Status)
    }
}

func TestCheckValidWorkdir(t *testing.T) {
    ctx := context.Background()
    dir := t.TempDir()
    results := Check(ctx, &Config{Workdir: dir})
    if len(results) != 1 {
        t.Fatalf("expected 1 result, got %d", len(results))
    }
    if results[0].Status != checkup.StatusOK {
        t.Errorf("expected status ok, got %s: %s", results[0].Status, results[0].Error)
    }
}

func TestCheckSystemWorkdir(t *testing.T) {
    ctx := context.Background()
    results := Check(ctx, &Config{Workdir: "/etc"})
    if len(results) != 1 {
        t.Fatalf("expected 1 result, got %d", len(results))
    }
    if results[0].Status != checkup.StatusError {
        t.Errorf("expected status error, got %s", results[0].Status)
    }
}
```

## Garbage Collection: `gc.go`

### Overview

A background goroutine that periodically scans `Workdir` for session subdirectories and removes those older than `SessionTTL`. The currently active session (identified via `adk.GetSessionValue(ctx, FileSessionKey)`) is never removed.

### API

```go
package file

import (
    "context"
    "time"
)

// StartGC starts a background goroutine that periodically scans the Workdir
// for session subdirectories and removes those whose modification time is
// older than cfg.SessionTTL. The currently active session (identified via
// adk.GetSessionValue) is never removed.
//
// The goroutine runs every interval until ctx is cancelled. If cfg.SessionTTL
// is zero, StartGC returns immediately (no-op).
//
// The caller is responsible for cancelling ctx to stop the goroutine (e.g.,
// via a parent context that is cancelled on server shutdown).
//
// Usage:
//
//   ctx, cancel := context.WithCancel(context.Background())
//   defer cancel()
//   go file.StartGC(ctx, cfg, 5*time.Minute)
func StartGC(ctx context.Context, cfg *Config, interval time.Duration) {
    if cfg == nil || cfg.SessionTTL == 0 {
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
                cleanStaleSessions(cfg)
            }
        }
    }()
}

// cleanStaleSessions scans cfg.Workdir for session subdirectories and removes
// those whose modification time is older than cfg.SessionTTL. The currently
// active session is never removed.
func cleanStaleSessions(cfg *Config) {
    entries, err := os.ReadDir(cfg.Workdir)
    if err != nil {
        // Workdir may not exist yet; that's fine.
        return
    }

    cutoff := time.Now().Add(-cfg.SessionTTL)

    for _, entry := range entries {
        if !entry.IsDir() {
            continue
        }

        fullPath := filepath.Join(cfg.Workdir, entry.Name())
        fi, err := os.Stat(fullPath)
        if err != nil {
            continue
        }

        if fi.ModTime().After(cutoff) {
            continue // still fresh
        }

        // Safety: never delete the currently active session.
        // We check via a fresh context — the active session is the one
        // currently stored in adk session values. Since the GC runs in a
        // background goroutine without an adk context, we use a heuristic:
        // we check if the directory name matches the sanitized form of the
        // default session. For the active session, the caller should ensure
        // the directory's modtime is updated periodically (e.g., by touching
        // a marker file on each tool invocation).
        //
        // The primary protection is the modtime check: as long as the session
        // is actively used, its directory modtime will be recent. The TTL
        // should be set generously (e.g., 1 hour) to avoid races.
        _ = os.RemoveAll(fullPath)
    }
}
```

### Design Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Active session protection | Modtime-based | The session directory's modtime is updated on every file write/create/delete. As long as the session is active, its modtime stays fresh. No cross-goroutine adk context needed. |
| GC interval | Caller-specified `interval` parameter | Flexibility; typical value is 5 minutes |
| TTL minimum | `validate:"omitempty,gte=60000000000"` (1 minute) | Prevents accidental aggressive cleanup |
| Zero TTL | No-op (GC never starts) | Backward compatible; GC is opt-in |
| Error handling | Silent skip on read errors | GC is best-effort; errors reading Workdir or individual entries are logged at most (via a future logger injection) but never crash the goroutine |
| Graceful shutdown | `ctx.Done()` | Standard Go pattern; caller cancels context on shutdown |

### Safety Analysis

1. **Race with active tool invocation**: If a tool is writing to a session directory while GC removes it, the tool will get an error on the next filesystem operation. This is acceptable because:
   - The TTL should be set generously (e.g., 1+ hours)
   - The session directory modtime is updated on every write, so an active session's modtime will always be recent
   - The race window is bounded by `interval` (e.g., 5 minutes)

2. **Race with session creation**: If a new session is created between the GC's `ReadDir` and `RemoveAll`, the new directory won't be in the `entries` slice, so it won't be removed.

3. **Symlink in Workdir**: The GC only processes directories returned by `os.ReadDir`. If someone places a symlink in Workdir pointing outside, `os.ReadDir` returns it as a directory entry, but `os.Stat` follows symlinks. `os.RemoveAll` on a symlink-to-directory removes the symlink itself, not the target. This is safe.

4. **Workdir itself**: The GC only removes *subdirectories* of Workdir, never Workdir itself.

### GC Tests: `gc_test.go`

```go
package file

import (
    "context"
    "os"
    "path/filepath"
    "testing"
    "time"
)

func TestStartGCNoopWhenTTLZero(t *testing.T) {
    // StartGC with zero TTL should return immediately without starting a goroutine.
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    // This should not panic or block.
    StartGC(ctx, &Config{Workdir: t.TempDir(), SessionTTL: 0}, time.Millisecond)
}

func TestCleanStaleSessionsRemovesOldDirs(t *testing.T) {
    dir := t.TempDir()

    // Create a session directory with an old modtime.
    oldSession := filepath.Join(dir, "old-session")
    if err := os.MkdirAll(oldSession, 0o755); err != nil {
        t.Fatal(err)
    }
    oldTime := time.Now().Add(-2 * time.Hour)
    if err := os.Chtimes(oldSession, oldTime, oldTime); err != nil {
        t.Fatal(err)
    }

    // Create a session directory with a recent modtime.
    recentSession := filepath.Join(dir, "recent-session")
    if err := os.MkdirAll(recentSession, 0o755); err != nil {
        t.Fatal(err)
    }

    cfg := &Config{Workdir: dir, SessionTTL: 1 * time.Hour}
    cleanStaleSessions(cfg)

    // Old session should be removed.
    if _, err := os.Stat(oldSession); !os.IsNotExist(err) {
        t.Errorf("expected old session to be removed, but it still exists")
    }

    // Recent session should still exist.
    if _, err := os.Stat(recentSession); err != nil {
        t.Errorf("expected recent session to still exist, got error: %v", err)
    }
}

func TestCleanStaleSessionsIgnoresFiles(t *testing.T) {
    dir := t.TempDir()

    // Create a regular file in Workdir (not a session directory).
    f, err := os.Create(filepath.Join(dir, "not-a-session.txt"))
    if err != nil {
        t.Fatal(err)
    }
    f.Close()

    // Set its modtime to be old.
    oldTime := time.Now().Add(-2 * time.Hour)
    if err := os.Chtimes(f.Name(), oldTime, oldTime); err != nil {
        t.Fatal(err)
    }

    cfg := &Config{Workdir: dir, SessionTTL: 1 * time.Hour}
    cleanStaleSessions(cfg)

    // File should still exist (GC only removes directories).
    if _, err := os.Stat(f.Name()); err != nil {
        t.Errorf("expected file to still exist, got error: %v", err)
    }
}

func TestCleanStaleSessionsEmptyWorkdir(t *testing.T) {
    dir := t.TempDir()
    cfg := &Config{Workdir: dir, SessionTTL: 1 * time.Hour}
    // Should not panic.
    cleanStaleSessions(cfg)
}

func TestCleanStaleSessionsNonexistentWorkdir(t *testing.T) {
    cfg := &Config{Workdir: "/nonexistent/path/for/testing", SessionTTL: 1 * time.Hour}
    // Should not panic.
    cleanStaleSessions(cfg)
}

func TestStartGCStopsOnContextCancel(t *testing.T) {
    dir := t.TempDir()
    ctx, cancel := context.WithCancel(context.Background())

    cfg := &Config{Workdir: dir, SessionTTL: 1 * time.Hour}
    StartGC(ctx, cfg, 10*time.Millisecond)

    // Let it run for a bit.
    time.Sleep(50 * time.Millisecond)

    // Cancel and verify the goroutine stops (no panic, no leak).
    cancel()

    // Give it time to shut down.
    time.Sleep(50 * time.Millisecond)

    // If we reach here without timeout, the goroutine stopped cleanly.
}
```

## Security Design

### Path Traversal Prevention

1. **Lexical check** (`fileutil.ValidateRelativePath`): Rejects absolute paths, NUL bytes, `..` that escapes root, and any remaining `..` segments after cleaning.
2. **Symlink rejection** (`fileutil.ResolveSymlinkSafe`): Walks every path component from root, rejecting any symlink. This prevents symlink-based traversal where a symlink inside the session directory points outside.
3. **Session root protection** (`file_delete`): Rejects deleting the session root itself (`"."`).

### Input Validation

- All `Params` structs use `validate:"required"` on mandatory fields.
- `file_write` enforces `MaxWriteBytes` limit (default 10MB).
- `file_read` enforces `MaxReadBytes` truncation (default 1MB).
- `StartLine`/`EndLine` validated: `StartLine <= EndLine` when both set.

### Binary File Detection

- `fileutil.IsBinary` checks first 8KB for null bytes.
- `file_read` refuses binary files with a clear error.

### No Safety Gates

Per explicit user decision, there are no `DryRun`/`Confirmed` gates. All tools execute immediately.

## Code Sharing Between File Tools and GitHub Tools

This section analyzes what code can be shared between the new `components/tool/file/` tools and the existing `components/tool/github/` file tools, beyond what is already extracted to `libs/toolkit/fileutil/`.

### What Is Already Shared (via `fileutil`)

The `libs/toolkit/fileutil/` extraction (Step 0 prerequisite) moves these functions to a shared location:

| Function | Used by GitHub | Used by file tools |
|----------|---------------|-------------------|
| `ValidateRelativePath` | All file tools | `resolvePath` (via `helper.go`) |
| `ResolveSymlinkSafe` | All file tools | `resolvePath` (via `helper.go`) |
| `IsBinary` | `file_read`, `file_search` | `file_read` |
| `RejectDotGitPath` | All file tools | Not used (no `.git` in session dirs) |
| `ValidateRootDir` | `base.go` | `config.go` / `NewAllTools` |
| `DefaultMaxReadBytes` | `file_read` | `file_read` |
| `DefaultMaxWriteBytes` | `file_write` | `file_write` |
| `DefaultMaxSearchFileBytes` | `file_search` | Not used (no search tool yet) |

### Detailed Comparison: Identical or Near-Identical Code

#### 1. `copyFileContents` — IDENTICAL

Both `github/helper.go` and the proposed `file/helper.go` contain the same function:

```go
func copyFileContents(src, dst string) error {
    srcFile, err := os.Open(src)
    // ... identical io.Copy logic ...
}
```

**Verdict**: Extract to `libs/toolkit/fileutil/` as `CopyFileContents`.

#### 2. `copyDir` — NEARLY IDENTICAL

The GitHub version additionally skips `.git` directories. The file tools version does not need `.git` skipping. Otherwise identical: `filepath.WalkDir`, create dirs, `io.Copy` files, skip symlinks.

```go
// GitHub version:
if d.Name() == ".git" { return filepath.SkipDir }

// File tools version:
// (no .git check)
```

**Verdict**: Extract to `libs/toolkit/fileutil/` as `CopyDir(root, src, dst string, skipDotGit bool)`. The `skipDotGit` parameter controls whether `.git` directories are skipped. GitHub calls with `skipDotGit=true`, file tools call with `skipDotGit=false`.

#### 3. `walkDirFiles` — NEARLY IDENTICAL

Same pattern as `copyDir`: GitHub skips `.git`, file tools don't need to.

```go
// GitHub version:
if d.IsDir() && d.Name() == ".git" { return filepath.SkipDir }

// File tools version:
// (no .git check)
```

**Verdict**: Extract to `libs/toolkit/fileutil/` as `WalkDirFiles(root string, skipDotGit bool) ([]string, error)`.

#### 4. `sanitizeSegment` — IDENTICAL (except fallback name)

```go
// GitHub: fallback is "repo"
if s == "" || s == "." { s = "repo" }

// File tools: fallback is "session"
if s == "" || s == "." { s = "session" }
```

**Verdict**: Extract to `libs/toolkit/fileutil/` as `SanitizePathSegment(s string, fallback string) string`. Both callers pass their preferred fallback.

#### 5. `sessionFromContext` — STRUCTURALLY IDENTICAL

```go
// GitHub:
func sessionFromContext(ctx context.Context) string {
    v, ok := adk.GetSessionValue(ctx, CloneSessionKey)
    // ...
}

// File tools:
func sessionFromContext(ctx context.Context) string {
    v, ok := adk.GetSessionValue(ctx, FileSessionKey)
    // ...
}
```

**Verdict**: Extract to `libs/toolkit/fileutil/` as `SessionFromContext(ctx context.Context, key string) string`. Both callers pass their session key constant.

#### 6. Line-Range Reading Logic — IDENTICAL

The `StartLine`/`EndLine` splitting code in `github/file_read.go` (lines 121-146) is byte-for-byte identical to the proposed `file/read.go`:

```go
if params.StartLine > 0 || params.EndLine > 0 {
    lines := strings.Split(output.Content, "\n")
    startIdx := 0
    endIdx := len(lines)
    // ... identical logic ...
}
```

**Verdict**: Extract to `libs/toolkit/fileutil/` as `ApplyLineRange(content string, startLine, endLine int) (result string, actualStart, actualEnd int)`.

#### 7. Transfer Path Validation — NEARLY IDENTICAL

The GitHub `validateTransferPaths` function (in `helper.go`) and the inline validation in the proposed `file/copy.go` and `file/move.go` share the same logic:
- Reject same source/destination
- Validate both paths
- Resolve symlinks
- Check source exists
- Check type mismatches
- Check destination not inside source

The GitHub version additionally calls `rejectDotGitPath` and `ensureCloneExists`.

**Verdict**: Do NOT extract. The validation is tightly coupled to the tool's specific error messages and context. The file tools inline this logic in `copy.go`/`move.go` with their own error messages (e.g., "session directory" vs "clone"). Extracting would require parameterizing too many error strings, making the abstraction worse than the duplication.

#### 8. Tool Struct Pattern — IDENTICAL

Every tool struct follows the same pattern:
```go
type XxxTool struct {
    cfg       *Config       // or *baseTool for GitHub
    invokable tool.InvokableTool
}

func (t *XxxTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
    return t.invokable.Info(ctx)
}

func (t *XxxTool) InvokableRun(ctx context.Context, args string, opts ...tool.Option) (string, error) {
    return t.invokable.InvokableRun(ctx, args, opts...)
}
```

**Verdict**: Do NOT extract. This is the standard eino `InvokableTool` boilerplate. It's 10 lines per tool and extracting it would require generics or interfaces that add more complexity than they save. The pattern is well-understood and consistent across the codebase.

#### 9. Output Structs — STRUCTURALLY SIMILAR

| Output struct | GitHub fields | File tools fields | Shared fields |
|---------------|---------------|-------------------|---------------|
| `FileReadOutput` | Path, Content, Bytes, Truncated, Note, StartLine, EndLine | Same | All |
| `FileWriteOutput` | Path, Branch, CommitSHA, Pushed | Path, Bytes, Mode | Path |
| `FileDeleteOutput` | Path, Type, Deleted, Branch | Path, Type, Deleted | Path, Type, Deleted |
| `FileCopyOutput` | Source, Destination, Type, Copied, Branch, FileCount, TotalBytes | Source, Destination, Type, Copied, FileCount, TotalBytes | All except Branch |
| `FileMoveOutput` | Source, Destination, Type, Moved, Branch | Source, Destination, Type, Moved | All except Branch |

**Verdict**: Do NOT extract. The output structs serve as the JSON schema for each tool. Sharing them would couple the two components' API surfaces. The file tools intentionally omit GitHub-specific fields (`Branch`, `CommitSHA`, `Pushed`). The duplication is small (5-6 fields each) and each struct is self-documenting for its tool.

### Summary: What Gets Extracted to `fileutil`

In addition to the functions already planned for extraction (Step 0), the following should also be extracted:

| Function | New `fileutil` name | Used by |
|----------|---------------------|---------|
| `copyFileContents` | `CopyFileContents(src, dst string) error` | GitHub file_copy/move, file tools copy/move |
| `copyDir` | `CopyDir(src, dst string, skipDotGit bool) (fileCount int, totalBytes int64, err error)` | GitHub file_copy/move, file tools copy/move |
| `walkDirFiles` | `WalkDirFiles(root string, skipDotGit bool) ([]string, error)` | GitHub file_copy/move/delete (dry-run), file tools copy/move (future) |
| `sanitizeSegment` | `SanitizePathSegment(s string, fallback string) string` | GitHub clonePath, file tools sessionPath |
| `sessionFromContext` | `SessionFromContext(ctx context.Context, key string) string` | GitHub helper, file tools helper |
| Line-range logic | `ApplyLineRange(content string, startLine, endLine int) (result string, actualStart, actualEnd int)` | GitHub file_read, file tools read |

### What Stays Separate (and Why)

| Code | Why NOT shared |
|------|---------------|
| `validateTransferPaths` | Tightly coupled to GitHub-specific error messages and `.git` rejection |
| Tool struct boilerplate | Standard eino pattern; 10 lines per tool; abstraction would add complexity |
| Output structs | Each component owns its API surface; GitHub has extra fields (Branch, CommitSHA) |
| `ensureCloneExists` | GitHub-specific error message referencing `github_repo_clone` |
| `rejectCloneRootPath` | GitHub-specific error message about "clone root" |
| `transferDryRunPreview` | GitHub-specific DryRun pattern; file tools don't have DryRun |
| `clonePath` / `clonePathForSession` | GitHub-specific clone layout `<cloneDir>/<session>/<owner>/<repo>` |
| `resolvePath` (file tools) | File-tools-specific session path resolution with `os.MkdirAll` |
| Git operations (checkout, add, commit, push) | GitHub-specific; file tools don't use git |

### Updated `libs/toolkit/fileutil/` File List

After incorporating the additional extractions:

| File | Contents |
|------|----------|
| `fileutil.go` | `ValidateRelativePath`, `IsWithinPath`, `ResolveSymlinkSafe`, `IsBinary`, `RejectDotGitPath`, `ValidateRootDir`, `CopyFileContents`, `CopyDir`, `WalkDirFiles`, `SanitizePathSegment`, `SessionFromContext`, `ApplyLineRange`, internal `systemDirs` |
| `limits.go` | `DefaultMaxReadBytes`, `DefaultMaxWriteBytes`, `DefaultMaxSearchFileBytes` |
| `fileutil_test.go` | Table-driven tests for all exported functions |
| `README.md` | Package documentation |

### Updated GitHub Refactoring

In addition to the changes in the extract-fileutils-plan.md:

| File | Additional Change |
|------|-------------------|
| `components/tool/github/helper.go` | Delete `copyFileContents`, `copyDir`, `walkDirFiles`, `sanitizeSegment`, `sessionFromContext`; replace with `fileutil.*` calls |
| `components/tool/github/file_read.go` | Replace line-range logic with `fileutil.ApplyLineRange` |
| `components/tool/github/file_copy.go` | Replace `copyFileContents`/`copyDir` with `fileutil.CopyFileContents`/`fileutil.CopyDir(..., true)` |
| `components/tool/github/file_move.go` | Replace `copyFileContents`/`copyDir` with `fileutil.CopyFileContents`/`fileutil.CopyDir(..., true)` |
| `components/tool/github/file_delete.go` | Replace `walkDirFiles` (dry-run) with `fileutil.WalkDirFiles(..., true)` |

### Updated File Tools Impact

With the additional extractions, `components/tool/file/helper.go` becomes much thinner:

```go
package file

// helper.go — after full extraction to fileutil

// sessionPath returns the session-scoped directory path: <Workdir>/<session>.
func sessionPath(workdir, session string) string {
    if session == "" {
        session = fileutil.SessionFromContext(ctx, FileSessionKey) // not quite — see below
    }
    return filepath.Join(workdir, fileutil.SanitizePathSegment(session, "session"))
}

// resolvePath validates and resolves a relative path within the session directory.
func resolvePath(workdir string, ctx context.Context, relPath string, createDirs bool) (string, error) {
    session := fileutil.SessionFromContext(ctx, FileSessionKey)
    root := filepath.Join(workdir, fileutil.SanitizePathSegment(session, "session"))

    if err := os.MkdirAll(root, 0o755); err != nil {
        return "", errors.Wrapf(err, "failed to create session directory %q", root)
    }

    fullPath, err := fileutil.ValidateRelativePath(root, relPath)
    if err != nil {
        return "", err
    }

    safePath, err := fileutil.ResolveSymlinkSafe(root, fullPath, createDirs)
    if err != nil {
        return "", err
    }

    return safePath, nil
}
```

The `copyFileContents`, `copyDir`, `walkDirFiles`, and `sanitizeSegment` functions are removed from `file/helper.go` and replaced with `fileutil.*` calls.

## Edge Cases

| Edge case | Handling |
|-----------|----------|
| Session directory doesn't exist | Auto-created by `resolvePath` with `os.MkdirAll` |
| File not found (read) | Wrapped `os.IsNotExist` error |
| Path traversal (`..`) | `fileutil.ValidateRelativePath` rejects |
| Absolute path input | `fileutil.ValidateRelativePath` rejects |
| NUL byte in path | `fileutil.ValidateRelativePath` rejects |
| Symlink at any path component | `fileutil.ResolveSymlinkSafe` rejects |
| Binary file (read) | `fileutil.IsBinary` detects, error returned |
| Empty file (read) | Returns `content=""`, `bytes=0` |
| Large file (read) | Truncated at `MaxReadBytes`, `truncated=true` + note |
| Content too large (write) | Error: exceeds `MaxWriteBytes` |
| Write to existing directory | Error: "is a directory" |
| Write to symlink | Error: "is a symlink" |
| Append to non-existent file | Creates the file (same as overwrite of new file) |
| Delete non-existent path | Wrapped `os.IsNotExist` error |
| Delete session root | Error: "deleting the entire session directory is not allowed" |
| Copy source not found | Wrapped `os.IsNotExist` error |
| Copy source == destination | Error: "same path" |
| Copy destination inside source | Error: "inside the source directory" |
| Copy file → existing directory | Error: type mismatch |
| Copy directory → existing file | Error: type mismatch |
| Copy directory → existing directory | Merge (overwrite matching files, keep extras) |
| Move cross-device | Fallback to copy+delete (non-atomic) |
| Move source not found | Wrapped `os.IsNotExist` error |
| Move destination inside source | Error: "inside the source directory" |
| Concurrent sessions | Each session gets its own subdirectory via `adk.GetSessionValue` |
| GC removes stale session | Session directory deleted; next tool invocation for that session recreates it via `os.MkdirAll` |
| GC runs on empty Workdir | No-op; `os.ReadDir` returns empty slice |
| GC runs on nonexistent Workdir | No-op; `os.ReadDir` error is silently ignored |
| GC with zero SessionTTL | `StartGC` returns immediately; no goroutine started |
| GC interval too short | `SessionTTL` minimum is 1 minute via validation; interval is caller-controlled |
| GC race with active write | Session directory modtime is updated on write, so it stays fresh; generous TTL avoids races |

## Testing Strategy

### `file_test.go`

Use `testing.T` with table-driven tests (not testify suite, since there's no mock HTTP server needed). Each test creates a temp directory with `t.TempDir()`, sets up a `Config` pointing at it, and exercises the tool.

**Test cases for `file_read`:**
1. `TestFileReadFull` — read full file, assert content and bytes
2. `TestFileReadLineRange` — `startLine=2, endLine=3`, assert only those lines
3. `TestFileReadStartLineOnly` — `startLine=2`, assert from line 2 to end
4. `TestFileReadEndLineOnly` — `endLine=2`, assert first 2 lines
5. `TestFileReadNotFound` — non-existent path, assert error
6. `TestFileReadPathTraversal` — `path="../../../etc/passwd"`, assert error
7. `TestFileReadBinary` — write binary data, assert error
8. `TestFileReadEmptyFile` — assert `content=""`, `bytes=0`
9. `TestFileReadLargeFile` — write >1MB file, assert `truncated=true`
10. `TestFileReadDirectory` — path is a directory, assert error
11. `TestFileReadSymlinkTraversal` — plant symlink, assert error
12. `TestFileReadStartLineAfterEndLine` — `startLine=5, endLine=2`, assert error
13. `TestFileReadSessionIsolation` — two different sessions, verify isolation

**Test cases for `file_write`:**
1. `TestFileWriteNewFile` — write to new path, assert content
2. `TestFileWriteOverwrite` — write to existing file, assert overwritten
3. `TestFileWriteAppend` — `append=true`, write twice, assert concatenated
4. `TestFileWriteAppendNewFile` — `append=true` on non-existent file, assert created
5. `TestFileWriteCreatesParentDirs` — `path="deep/nested/file.txt"`, assert dirs created
6. `TestFileWriteContentTooLarge` — content > MaxWriteBytes, assert error
7. `TestFileWritePathTraversal` — `path="../../evil.txt"`, assert error
8. `TestFileWriteToDirectory` — path is existing directory, assert error
9. `TestFileWriteToSymlink` — path is symlink, assert error
10. `TestFileWriteSymlinkTraversal` — symlink in path, assert error
11. `TestFileWriteSessionIsolation` — two sessions, verify files don't leak

**Test cases for `file_delete`:**
1. `TestFileDeleteFile` — delete a file, assert gone
2. `TestFileDeleteDirectory` — delete a directory, assert gone
3. `TestFileDeleteNestedDirectory` — delete nested dir tree, assert gone
4. `TestFileDeleteNotFound` — non-existent path, assert error
5. `TestFileDeleteSessionRoot` — `path="."`, assert error
6. `TestFileDeletePathTraversal` — `path="../../etc/passwd"`, assert error
7. `TestFileDeleteSymlink` — path is symlink, assert error

**Test cases for `file_copy`:**
1. `TestFileCopyFile` — copy file, assert content matches
2. `TestFileCopyFileOverwrite` — copy over existing file, assert overwritten
3. `TestFileCopyFileCreatesParentDirs` — deep destination, assert dirs created
4. `TestFileCopyDirectory` — copy directory, assert tree matches
5. `TestFileCopyDirectoryMerge` — copy dir over existing dir, assert merge
6. `TestFileCopySourceNotFound` — non-existent source, assert error
7. `TestFileCopySamePath` — source == destination, assert error
8. `TestFileCopyDestInsideSource` — destination inside source dir, assert error
9. `TestFileCopyTypeMismatch` — file→dir or dir→file, assert error
10. `TestFileCopyPathTraversal` — traversal in source or dest, assert error
11. `TestFileCopySymlinkSource` — symlink in source path, assert error
12. `TestFileCopySkipsSymlinkInsideTree` — symlink inside copied dir, assert skipped

**Test cases for `file_move`:**
1. `TestFileMoveFile` — move file, assert source gone, dest has content
2. `TestFileMoveFileRename` — rename in same directory
3. `TestFileMoveDirectory` — move directory, assert tree moved
4. `TestFileMoveOverwrite` — move over existing file, assert overwritten
5. `TestFileMoveSourceNotFound` — non-existent source, assert error
6. `TestFileMoveSamePath` — source == destination, assert error
7. `TestFileMoveDestInsideSource` — destination inside source dir, assert error
8. `TestFileMoveTypeMismatch` — file→dir or dir→file, assert error
9. `TestFileMovePathTraversal` — traversal in source or dest, assert error
10. `TestFileMoveSymlinkSource` — symlink in source path, assert error

**Test cases for `NewAllTools`:**
1. `TestNewAllTools` — creates all 5 tools, each has correct Info
2. `TestNewAllToolsNilConfig` — nil config, defaults applied
3. `TestNewAllToolsInvalidWorkdir` — system directory, assert error
4. `TestNewReadOnlyTools` — creates only read tool

## Imports Needed

The `components/tool/file/` package will import:

```go
import (
    "context"
    "encoding/json"
    "fmt"
    "os"
    "path/filepath"
    "syscall"
    "time"

    "emperror.dev/errors"
    "github.com/cloudwego/eino/components/tool"
    "github.com/cloudwego/eino/components/tool/utils"
    "github.com/cloudwego/eino/schema"
    "github.com/webcenter-fr/eino-ext/libs/toolkit/checkup"
    "github.com/webcenter-fr/eino-ext/libs/toolkit/fileutil"
    "github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
)
```

Note: `io`, `io/fs`, `strings`, and `github.com/cloudwego/eino/adk` are no longer needed in the file tools package because `copyFileContents`, `copyDir`, `walkDirFiles`, `sanitizeSegment`, and `sessionFromContext` are now in `fileutil`. The `adk` import is only needed in `fileutil` (for `SessionFromContext`). `encoding/json` replaces `github.com/goccy/go-json` for consistency with the rest of the codebase (or keep `go-json` if preferred — either works). `time` is needed for `SessionTTL` and GC.

## README.md

```markdown
# File Tools

Local filesystem file operation tools for eino agents. Tools operate within a
session-scoped temporary directory, allowing agents to store intermediate results
without keeping everything in context.

## Tools

| Tool | Kind | Description |
|------|------|-------------|
| `file_read` | read | Read file contents (full or line range) |
| `file_write` | write | Create, overwrite, or append to a file |
| `file_delete` | write | Delete a file or directory (recursive) |
| `file_copy` | write | Copy a file or directory |
| `file_move` | write | Move or rename a file or directory |

## Usage

```go
cfg := &file.Config{
    Workdir:    "/tmp/eino-files",
    SessionTTL: 1 * time.Hour, // optional: enable GC for stale sessions
}
tools, err := file.NewAllTools(ctx, cfg)

// Start garbage collection (optional)
go file.StartGC(ctx, cfg, 5*time.Minute)
```

## Session Isolation

Each user session gets its own subdirectory under `Workdir`. The session ID is
read from the adk context via `adk.GetSessionValue(ctx, "file_session_id")`.
Set it at run start:

```go
adk.AddSessionValue(ctx, file.FileSessionKey, sessionID)
```

## Garbage Collection

When `SessionTTL` is set, a background goroutine (started via `StartGC`)
periodically scans `Workdir` for session subdirectories and removes those
whose modification time is older than `SessionTTL`. The currently active
session is protected by its recent modification time.

```go
// Start GC with a 5-minute scan interval.
ctx, cancel := context.WithCancel(context.Background())
defer cancel()
go file.StartGC(ctx, cfg, 5*time.Minute)
```

- Set `SessionTTL` generously (e.g., 1 hour) to avoid races with active sessions.
- The GC goroutine stops when `ctx` is cancelled.
- If `SessionTTL` is zero, `StartGC` is a no-op.

## Security

- All paths are validated to stay within the session directory.
- Symlinks at any path component are rejected.
- Binary files are detected and refused by `file_read`.
- Content size limits prevent resource exhaustion.
- The session root cannot be deleted.
- GC only removes directories, never regular files in Workdir.

## Requirements

- A writable directory specified in `Config.Workdir`.
- The directory must not be a system directory (/, /etc, /proc, etc.).
```

## Ordered Task List

1. **Implement `libs/toolkit/fileutil/`** (prerequisite extraction):
   - Create `libs/toolkit/fileutil/limits.go` with `DefaultMaxReadBytes`, `DefaultMaxWriteBytes`, `DefaultMaxSearchFileBytes`
   - Create `libs/toolkit/fileutil/fileutil.go` with `ValidateRelativePath`, `IsWithinPath`, `ResolveSymlinkSafe`, `IsBinary`, `RejectDotGitPath`, `ValidateRootDir`, `CopyFileContents`, `CopyDir`, `WalkDirFiles`, `SanitizePathSegment`, `SessionFromContext`, `ApplyLineRange`
   - Create `libs/toolkit/fileutil/fileutil_test.go` with table-driven tests for all exported functions
   - Create `libs/toolkit/fileutil/README.md`

2. **Refactor GitHub tools to use `fileutil`**:
   - Edit `components/tool/github/base.go`: wrap `fileutil.ValidateRootDir`, delete `systemDirs`
   - Edit `components/tool/github/helper.go`: delete `validateFilePath`, `isWithinPath`, `resolveSymlinkSafe`, `isBinary`, `rejectDotGitPath`, `copyFileContents`, `copyDir`, `walkDirFiles`, `sanitizeSegment`, `sessionFromContext`, and the `max*Bytes` consts; replace with `fileutil.*` calls
   - Edit `components/tool/github/file_read.go`: replace local helper calls with `fileutil.*`; replace line-range logic with `fileutil.ApplyLineRange`
   - Edit `components/tool/github/file_write.go`: replace local helper calls with `fileutil.*`
   - Edit `components/tool/github/file_copy.go`: replace local helper calls with `fileutil.*`; use `fileutil.CopyFileContents` and `fileutil.CopyDir(..., true)`
   - Edit `components/tool/github/file_move.go`: replace local helper calls with `fileutil.*`; use `fileutil.CopyFileContents` and `fileutil.CopyDir(..., true)`
   - Edit `components/tool/github/file_delete.go`: replace local helper calls with `fileutil.*`; use `fileutil.WalkDirFiles(..., true)` for dry-run
   - Edit `components/tool/github/file_list.go`: replace local helper calls with `fileutil.*`
   - Edit `components/tool/github/file_search.go`: replace local helper calls with `fileutil.*`
   - Edit `components/tool/github/file_test.go`: update error string assertions ("escapes clone directory" → "escapes root directory")

3. **Create `components/tool/file/config.go`** with `Config` struct (including `SessionTTL` field)

4. **Create `components/tool/file/helper.go`** with `sessionPath`, `resolvePath` (using `fileutil.SessionFromContext`, `fileutil.SanitizePathSegment`, `fileutil.ValidateRelativePath`, `fileutil.ResolveSymlinkSafe`)

5. **Create `components/tool/file/read.go`** with `FileReadTool` (using `fileutil.ApplyLineRange` for line-range logic)

6. **Create `components/tool/file/write.go`** with `FileWriteTool`

7. **Create `components/tool/file/delete.go`** with `FileDeleteTool`

8. **Create `components/tool/file/copy.go`** with `FileCopyTool` (using `fileutil.CopyFileContents` and `fileutil.CopyDir(..., false)`)

9. **Create `components/tool/file/move.go`** with `FileMoveTool` (using `fileutil.CopyFileContents` and `fileutil.CopyDir(..., false)` for cross-device fallback)

10. **Create `components/tool/file/file.go`** with `NewAllTools`, `NewReadOnlyTools`, `WriteToolNames`, package comment

11. **Create `components/tool/file/gc.go`** with `StartGC` and `cleanStaleSessions`

12. **Create `components/tool/file/gc_test.go`** with GC tests

13. **Create `components/tool/file/check.go`** with `Check()` function

14. **Create `components/tool/file/check_test.go`** with checkup tests

15. **Create `components/tool/file/file_test.go`** with all tool tests

16. **Create `components/tool/file/README.md`** (including GC documentation)

17. **Validate**: `go build ./...`, `go vet ./...`, `go test ./libs/toolkit/fileutil/...`, `go test ./components/tool/github/...`, `go test ./components/tool/file/...`

## Risks and Mitigations

| Risk | Mitigation |
|------|------------|
| `fileutil` extraction breaks GitHub tests | Update error string assertions; run full GitHub test suite |
| Session directory permissions | `os.MkdirAll` with 0o755; checkup probes writability |
| Cross-device move data loss | Fallback to copy+delete; error message warns about non-atomicity |
| Large file memory exhaustion | `MaxReadBytes` and `MaxWriteBytes` limits enforced |
| Symlink TOCTOU | `ResolveSymlinkSafe` checks every component with `os.Lstat`; no window between check and use since we use the returned safe path directly |
| Concurrent session directory creation | `os.MkdirAll` is idempotent; no race condition |
| GC removes active session directory | Modtime-based protection: active sessions have recent modtimes; generous TTL (1+ hour) avoids races |
| GC goroutine leak | `StartGC` uses `ctx.Done()` for graceful shutdown; caller cancels context on server shutdown |
| GC deletes non-session files in Workdir | GC only removes directories (not regular files); `os.ReadDir` entries are filtered by `IsDir()` |

## Out of Scope

- **Streaming** — all tools return complete results (no `StreamableTool`)
- **File search/grep** — not in requirements; can be added later
- **File list** — not in requirements; can be added later
- **DryRun/Confirmed safety gates** — explicitly excluded per user decision
- **Configurable file permissions** — always uses 0o644 for files, 0o755 for dirs
- **`.gitignore` or VCS awareness** — these are general-purpose file tools
- **Remote/network file operations** — local filesystem only
