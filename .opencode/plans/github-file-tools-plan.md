# GitHub File Tools — Implementation Plan

## Overview and Motivation

The GitHub tools component (`components/tool/github/`) wraps `go-git/v5` for local clone operations and `go-github/v71` for REST API calls. The existing `github_repo_clone` tool clones repos to `<CloneDir>/<owner>/<repo>`, and `github_branch_create` can create local branches in those clones. Currently there is no way for an LLM agent to inspect or modify file contents inside a cloned repo.

This plan adds four new tools:

| Tool | Kind | Purpose |
|------|------|---------|
| `github_file_read` | read-only | Read file contents (full or line range) from a cloned repo |
| `github_file_search` | read-only | Grep (regex, line-by-line) within a cloned repo |
| `github_file_list` | read-only | List files/dirs in a cloned repo with depth control |
| `github_file_write` | write | Create/overwrite a file, commit, and push to a remote branch |

`github_pr_create` already exists and needs **no changes** — after `github_file_write` pushes a branch, the existing PR tool can open a PR from it.

## Resolved Design Decisions

1. **`github_file_write` branch handling**: Auto-create the target branch from an optional `BaseBranch` param (defaults to current HEAD) if the branch does not exist locally. This avoids a mandatory `github_branch_create` call but still supports the explicit-branch workflow.
2. **`github_file_read` output**: JSON-wrapped object with metadata (`path`, `content`, `bytes`, `truncated`, `startLine`, `endLine`).
3. **`github_file_write` author identity**: Generic constant signature `{Name: "eino-ext", Email: "eino-ext@users.noreply.github.com"}`.
4. **Checkup**: No checkup entries for file tools — they operate on the local filesystem, not the REST API. Documented as explicit out-of-scope.
5. **Large file truncation**: 1MB threshold (`maxFileReadBytes = 1 << 20`). Truncated output includes `truncated: true` and a `note`.
6. **`github_file_list` MaxDepth**: `1` = immediate children only (default), `2` = one level of recursion, ..., `0` = unlimited (capped by `MaxResults`).
7. **`github_file_search` regex**: Use `regexp.Compile` directly (not `filter.Compile`, which treats empty as "no filter"). `Pattern` is `validate:"required"`.
8. **`github_file_write` push rejection**: Return a wrapped error; never force-push. No `Force` parameter.

## File-by-File Changes

### New files

| File | Content |
|------|---------|
| `components/tool/github/file_read.go` | `FileReadTool`, `FileReadParams`, `FileReadOutput`, `NewFileReadTool`, `newFileReadTool` |
| `components/tool/github/file_search.go` | `FileSearchTool`, `FileSearchParams`, `FileSearchOutput`, `NewFileSearchTool`, `newFileSearchTool` |
| `components/tool/github/file_list.go` | `FileListTool`, `FileListParams`, `FileListOutput`, `NewFileListTool`, `newFileListTool` |
| `components/tool/github/file_write.go` | `FileWriteTool`, `FileWriteParams`, `FileWriteOutput`, `NewFileWriteTool`, `newFileWriteTool` |
| `components/tool/github/file_test.go` | Tests for all four tools |

### Modified files

| File | Change |
|------|--------|
| `components/tool/github/helper.go` | Add `validateFilePath()`, `isBinary()`, `maxFileReadBytes` constant, `commitIdentity` constant |
| `components/tool/github/registry.go` | Register 3 read constructors + 1 write constructor; add interface compliance checks; add `github_file_write` to `WriteToolNames()` |
| `components/tool/github/README.md` | Add the 4 new tools to the tool tables |

No changes to `base.go`, `config.go`, `check.go`, `pr_create.go`, or any other existing tool.

## Data Structures

### `file_read.go`

```go
const fileReadDescription = `
** General Purpose **
It reads the contents of a file from a previously cloned GitHub repository.

** Output **
It returns a JSON object with the file path, content (or a line range), byte size,
and a truncated flag if the file exceeds the read limit.

** Important **
- The repository must already be cloned under <CloneDir>/<owner>/<repo>.
- Paths are validated to stay within the clone directory; traversal attempts are rejected.
- Binary files are detected and refused.
- Files larger than 1MB are truncated with a note.
`

type FileReadParams struct {
    Instance  string `json:"instance"  validate:"required" jsonschema:"(required) The GitHub instance to connect to."`
    Owner     string `json:"owner"     validate:"required" jsonschema:"(required) Repository owner."`
    Repo      string `json:"repo"      validate:"required" jsonschema:"(required) Repository name."`
    Path      string `json:"path"      validate:"required" jsonschema:"(required) Relative file path inside the cloned repo."`
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
    *baseTool
    tool.InvokableTool
}
```

### `file_search.go`

```go
const fileSearchDescription = `
** General Purpose **
It searches file contents in a previously cloned GitHub repository using a regex
(equivalent to 'grep -rn pattern dir/').

** Output **
It returns a JSON array of matches, each with the file path, line number, and
matched line content.

** Important **
- The repository must already be cloned under <CloneDir>/<owner>/<repo>.
- Binary files and the .git directory are skipped.
- MaxResults (default 100) caps the number of returned matches.
- Pattern uses Go RE2 syntax; lookahead/lookbehind/backreferences are NOT supported.
`

type FileSearchParams struct {
    Instance   string `json:"instance"   validate:"required" jsonschema:"(required) The GitHub instance to connect to."`
    Owner      string `json:"owner"      validate:"required" jsonschema:"(required) Repository owner."`
    Repo       string `json:"repo"       validate:"required" jsonschema:"(required) Repository name."`
    Pattern    string `json:"pattern"    validate:"required" jsonschema:"(required) Go RE2 regex to search for in file contents."`
    PathPrefix string `json:"pathPrefix,omitempty" jsonschema:"(optional) Subdirectory to search within. Defaults to repo root."`
    MaxResults int    `json:"maxResults,omitempty" jsonschema:"(optional) Maximum number of matches to return. Defaults to 100."`
}

type FileSearchOutput struct {
    Path    string `json:"path"`
    Line    int    `json:"line"`
    Content string `json:"content"`
}

type FileSearchTool struct {
    *baseTool
    tool.InvokableTool
}
```

### `file_list.go`

```go
const fileListDescription = `
** General Purpose **
It lists files and directories in a previously cloned GitHub repository.

** Output **
It returns a JSON array of entries with name, type (file/dir), size, and relative path.

** Important **
- The repository must already be cloned under <CloneDir>/<owner>/<repo>.
- MaxDepth=1 (default) lists immediate children; 0 = unlimited depth (capped by MaxResults).
- Symlinks are skipped. The .git directory is always skipped.
`

type FileListParams struct {
    Instance   string `json:"instance"   validate:"required" jsonschema:"(required) The GitHub instance to connect to."`
    Owner      string `json:"owner"      validate:"required" jsonschema:"(required) Repository owner."`
    Repo       string `json:"repo"       validate:"required" jsonschema:"(required) Repository name."`
    SubPath    string `json:"subPath,omitempty" jsonschema:"(optional) Relative subdirectory to list. Defaults to repo root."`
    MaxDepth   int    `json:"maxDepth,omitempty" jsonschema:"(optional) Maximum recursion depth. 1 = immediate children (default). 0 = unlimited."`
    MaxResults int    `json:"maxResults,omitempty" jsonschema:"(optional) Maximum number of entries to return. Defaults to 100."`
}

type FileListOutput struct {
    Name string `json:"name"`
    Type string `json:"type"` // "file" or "dir"
    Size int64  `json:"size"`
    Path string `json:"path"` // relative to repo root
}

type FileListTool struct {
    *baseTool
    tool.InvokableTool
}
```

### `file_write.go`

```go
const fileWriteDescription = `
** General Purpose **
It creates or overwrites a file in a previously cloned GitHub repository, commits
the change, and pushes to the specified remote branch.

** Output **
It returns the file path, commit SHA, and push status.

** Important **
- The repository must already be cloned under <CloneDir>/<owner>/<repo>.
- If the target branch does not exist locally, it is created from BaseBranch
  (defaults to current HEAD).
- Requires Confirmed=true. Use DryRun=true first to preview.
- Paths are validated to stay within the clone directory.
- Never force-pushes; a non-fast-forward rejection returns an error.
`

type FileWriteParams struct {
    Instance       string `json:"instance"       validate:"required" jsonschema:"(required) The GitHub instance to connect to."`
    Owner          string `json:"owner"          validate:"required" jsonschema:"(required) Repository owner."`
    Repo           string `json:"repo"           validate:"required" jsonschema:"(required) Repository name."`
    Path           string `json:"path"           validate:"required" jsonschema:"(required) Relative file path inside the cloned repo."`
    Content        string `json:"content"        validate:"required" jsonschema:"(required) File content to write."`
    Branch         string `json:"branch"         validate:"required" jsonschema:"(required) Target branch to commit and push to."`
    BaseBranch     string `json:"baseBranch,omitempty" jsonschema:"(optional) Base branch to create Branch from if Branch does not exist locally. Defaults to current HEAD."`
    CommitMessage  string `json:"commitMessage"  validate:"required" jsonschema:"(required) Git commit message."`
    DryRun         bool   `json:"dryRun,omitempty"         jsonschema:"(optional) If true, preview the write without making changes."`
    Confirmed       bool   `json:"confirmed,omitempty"       jsonschema:"(optional) Must be true to actually execute. Set after approving the dry-run result."`
}

type FileWriteOutput struct {
    Path      string `json:"path"`
    Branch    string `json:"branch"`
    CommitSHA string `json:"commitSha"`
    Pushed    bool   `json:"pushed"`
}

type FileWriteTool struct {
    *baseTool
    tool.InvokableTool
}
```

## Function Signatures

### `file_read.go`

```go
func (t *FileReadTool) Invoke(ctx context.Context, params *FileReadParams) (string, error)
func NewFileReadTool(ctx context.Context, configs Configs) (*FileReadTool, error)
func newFileReadTool(ctx context.Context, base *baseTool) (*FileReadTool, error)
```

### `file_search.go`

```go
func (t *FileSearchTool) Invoke(ctx context.Context, params *FileSearchParams) (string, error)
func NewFileSearchTool(ctx context.Context, configs Configs) (*FileSearchTool, error)
func newFileSearchTool(ctx context.Context, base *baseTool) (*FileSearchTool, error)
```

### `file_list.go`

```go
func (t *FileListTool) Invoke(ctx context.Context, params *FileListParams) (string, error)
func NewFileListTool(ctx context.Context, configs Configs) (*FileListTool, error)
func newFileListTool(ctx context.Context, base *baseTool) (*FileListTool, error)
```

### `file_write.go`

```go
func (t *FileWriteTool) Invoke(ctx context.Context, params *FileWriteParams) (string, error)
func NewFileWriteTool(ctx context.Context, configs Configs) (*FileWriteTool, error)
func newFileWriteTool(ctx context.Context, base *baseTool) (*FileWriteTool, error)
```

## Validation Logic

### Common (all tools)

1. `validateParams(params)` — runs `validate.Struct` on the params struct (enforces `required` tags).
2. Resolve clone path: `clonePath(t.cloneDir, params.Owner, params.Repo)`.
3. Check the clone exists: `os.Stat(clonePath)`; if `os.IsNotExist`, return `toolutil.NotFoundError("cloned repository", clonePath, nil)`-style error wrapped with `emperror.dev/errors`:
   ```go
   if _, err := os.Stat(clonePath); err != nil {
       if os.IsNotExist(err) {
           return "", errors.Wrapf(err, "repository %s/%s is not cloned at %q; run github_repo_clone first", params.Owner, params.Repo, clonePath)
       }
       return "", errors.Wrapf(err, "failed to stat clone directory %q", clonePath)
   }
   ```

### `github_file_read`

1. Validate `StartLine`/`EndLine`:
   - If both > 0: require `StartLine <= EndLine`.
   - If `StartLine > 0` and `EndLine == 0`: read from `StartLine` to end.
   - If `StartLine == 0` and `EndLine > 0`: read from line 1 to `EndLine`.
2. Resolve full path via `validateFilePath(clonePath, params.Path)`.
3. `os.Lstat` — reject symlinks (mode & `os.ModeSymlink`).
4. `os.ReadFile` — read full content.
5. Binary detection via `isBinary(data)` — if binary, return error: `"file %q appears to be binary; refusing to read"`.
6. If `len(data) > maxFileReadBytes`: truncate, set `Truncated=true`, `Note="file truncated to 1MB (original size: N bytes)"`.
7. If `StartLine`/`EndLine` set: split by `\n`, slice, rejoin. Set `StartLine`/`EndLine` in output.
8. Marshal `FileReadOutput` to JSON, return.

### `github_file_search`

1. Compile `params.Pattern` with `regexp.Compile`; wrap error: `"invalid search pattern %q"`.
2. Resolve search root via `validateFilePath(clonePath, params.PathPrefix)` (PathPrefix may be empty → root).
3. Default `MaxResults` to 100 if `<= 0`.
4. `filepath.WalkDir(searchRoot, fn)`:
   - Skip `.git` directory (return `filepath.SkipDir`).
   - Skip symlinks (`os.Lstat`, check mode).
   - Skip directories (only read files).
   - Read file; if `isBinary`, skip.
   - Split into lines; for each line, if `re.MatchString(line)`, append `FileSearchOutput{Path: relPath, Line: i, Content: line}`.
   - Stop when `len(results) >= MaxResults` (return `filepath.SkipAll` — Go 1.20+).
5. Marshal `[]FileSearchOutput` to JSON array, return.

### `github_file_list`

1. Resolve list root via `validateFilePath(clonePath, params.SubPath)` (SubPath may be empty → root).
2. Default `MaxDepth` to 1 if `<= 0` is NOT applied — `0` means unlimited. So: `if params.MaxDepth == 0 { maxDepth = -1 } else if params.MaxDepth < 0 { maxDepth = 1 } else { maxDepth = params.MaxDepth }`. (Treat unset/0-from-JSON as "default 1".) **Implementation note**: since `omitempty` int `0` is the JSON default, distinguish "user set 0" from "unset" is impossible. Decision: `MaxDepth <= 0` means **unlimited**; default behavior for a missing field is `0` → unlimited. To get immediate-only, user must set `1`. **Revisit**: this contradicts the spec's "default 1". 
   - **Final decision**: Apply default in code: `maxDepth := params.MaxDepth; if maxDepth == 0 { maxDepth = 1 }`. Then `0` is impossible from JSON (int zero value). Users wanting unlimited set a large number (e.g. `999`). Document that `maxDepth=0` is treated as `1` (default). This matches the spec's "default 1".
3. Default `MaxResults` to 100 if `<= 0`.
4. `filepath.WalkDir(listRoot, fn)` with depth tracking:
   - Compute depth as `strings.Count(relPath, "/") + 1`.
   - Skip entries with `depth > maxDepth`.
   - Skip `.git` directory (return `filepath.SkipDir`).
   - Skip symlinks.
   - Append `FileListOutput{Name, Type, Size, Path: relPath}`.
   - Stop at `MaxResults`.
5. Marshal `[]FileListOutput` to JSON array, return.

### `github_file_write`

1. DryRun: return `{"dryRun": true, "wouldWrite": {"path": ..., "branch": ..., "bytes": N, "commitMessage": ...}}`.
2. `confirm.RequireConfirmationForAction("write file", params.Confirmed)`.
3. Resolve clone path; `git.PlainOpen(clonePath)`.
4. Resolve file path via `validateFilePath(clonePath, params.Path)`.
5. Reject if path is a directory or symlink.
6. Get worktree: `repo.Worktree()`.
7. Branch handling:
   - Try `wt.Checkout(&git.CheckoutOptions{Branch: plumbing.NewBranchReferenceName(params.Branch)})`.
   - If error indicates branch doesn't exist:
     - Resolve base: if `params.BaseBranch != ""`, `repo.ResolveRevision(plumbing.Revision(params.BaseBranch))`; else `repo.Head()`.
     - `wt.Checkout(&git.CheckoutOptions{Hash: baseHash, Branch: plumbing.NewBranchReferenceName(params.Branch), Create: true})`.
   - Wrap errors with context: `"failed to checkout branch %q"`.
8. Create parent directories: `os.MkdirAll(filepath.Dir(fullPath), 0o755)`.
9. Write file: `os.WriteFile(fullPath, []byte(params.Content), 0o644)`.
10. Stage: `wt.Add(params.Path)` (relative path).
11. Commit:
    ```go
    hash, err := wt.Commit(params.CommitMessage, &git.CommitOptions{
        Author: commitIdentity,
    })
    ```
12. Push:
    ```go
    tok, _ := t.token(params.Instance)
    host, _ := t.gitHost(params.Instance)
    err = repo.Push(&git.PushOptions{
        RemoteURL: fmt.Sprintf("https://%s/%s/%s.git", host, params.Owner, params.Repo),
        Auth:      &http.BasicAuth{Username: "x-access-token", Password: tok},
    })
    ```
    - If `err == git.NoErrAlreadyUpToDate`: `Pushed=false` (no-op).
    - If `err != nil`: redact token from error string, return wrapped error.
13. Marshal `FileWriteOutput`, return.

## Path Safety Design

### New helper: `validateFilePath` (in `helper.go`)

```go
// validateFilePath resolves a relative path under cloneRoot and returns the
// absolute, cleaned path. It rejects:
//   - paths that escape cloneRoot after cleaning (contain ".." that resolves
//     outside the root)
//   - symlinks (caller must also Lstat the result before reading/writing)
//
// relPath may be empty (returns cloneRoot itself) or a relative path with
// forward slashes.
func validateFilePath(cloneRoot, relPath string) (string, error) {
    if relPath == "" {
        return cloneRoot, nil
    }
    // Normalize separators.
    relPath = filepath.FromSlash(relPath)
    // Reject absolute paths and drive letters.
    if filepath.IsAbs(relPath) {
        return "", errors.Errorf("path must be relative, got absolute path %q", relPath)
    }
    joined := filepath.Join(cloneRoot, relPath)
    cleaned := filepath.Clean(joined)
    // Must be within cloneRoot.
    if cleaned != cloneRoot && !strings.HasPrefix(cleaned, cloneRoot+string(filepath.Separator)) {
        return "", errors.Errorf("path %q escapes clone directory %q", relPath, cloneRoot)
    }
    // Reject any remaining ".." segments (defense in depth).
    if strings.Contains(filepath.ToSlash(cleaned), "/../") || strings.HasSuffix(filepath.ToSlash(cleaned), "/..") {
        return "", errors.Errorf("path %q contains directory traversal", relPath)
    }
    return cleaned, nil
}
```

### New helper: `isBinary` (in `helper.go`)

```go
// isBinary returns true if the first 8KB of data contain a null byte, which is
// the standard heuristic for detecting binary files.
func isBinary(data []byte) bool {
    n := len(data)
    if n > 8192 {
        n = 8192
    }
    return bytes.Contains(data[:n], []byte{0})
}
```

### New constants (in `helper.go`)

```go
const maxFileReadBytes = 1 << 20 // 1MB

var commitIdentity = &object.Signature{
    Name:  "eino-ext",
    Email: "eino-ext@users.noreply.github.com",
}
```

### Symlink handling

Every tool calls `os.Lstat` on the resolved path and rejects/skips if `mode&os.ModeSymlink != 0`. This prevents symlink-based traversal outside the clone directory.

### `.git` directory

`github_file_search` and `github_file_list` skip `.git` via `filepath.SkipDir`. `github_file_read` and `github_file_write` reject paths starting with `.git/` (the `validateFilePath` check keeps them inside cloneRoot, but `.git` internals should not be touched — add an explicit check: if `strings.HasPrefix(filepath.ToSlash(relPath), ".git/")` return error).

## Error Handling Strategy

- All errors wrapped with `emperror.dev/errors` including operation context.
- Token redaction: after any go-git error, run `strings.ReplaceAll(err.Error(), tok, "***REDACTED***")` before wrapping (matches `repo_clone.go` pattern).
- "Not cloned" errors: clear message telling user to run `github_repo_clone` first.
- "File not found": wrap with `errors.Wrapf(err, "file %q not found in clone", params.Path)` — `os.IsNotExist` check for a distinct message.
- "Binary file": return error, not empty content.
- "Path traversal": return error immediately, do not read.
- "Branch not found" (file_write): auto-create from BaseBranch; if BaseBranch also fails, wrap with `"failed to resolve base branch %q for creating branch %q"`.
- "Push rejection": wrap go-git error, redact token, suggest user re-clone or pull.

## Registration in `registry.go`

### Add to `readOnlyConstructors`

```go
func(ctx context.Context, b *baseTool) (tool.InvokableTool, error) { return newFileReadTool(ctx, b) },
func(ctx context.Context, b *baseTool) (tool.InvokableTool, error) { return newFileSearchTool(ctx, b) },
func(ctx context.Context, b *baseTool) (tool.InvokableTool, error) { return newFileListTool(ctx, b) },
```

### Add to `writeConstructors`

```go
func(ctx context.Context, b *baseTool) (tool.InvokableTool, error) { return newFileWriteTool(ctx, b) },
```

### Add to `WriteToolNames()`

```go
"github_file_write",
```

### Add interface compliance checks

```go
_ tool.InvokableTool = (*FileReadTool)(nil)
_ tool.InvokableTool = (*FileSearchTool)(nil)
_ tool.InvokableTool = (*FileListTool)(nil)
_ tool.InvokableTool = (*FileWriteTool)(nil)
```

## Test Coverage Plan (`file_test.go`)

Tests use the existing `GitHubToolTestSuite`. Since file tools operate on the local filesystem (not the mock HTTP server), tests create a real temp directory, init a git repo with `git.PlainInit`, write sample files, and point a `Configs` at it. A helper sets up the clone dir per test.

### Test helper

```go
func (s *GitHubToolTestSuite) setupClone() (cloneDir string, cleanup func()) {
    dir, _ := os.MkdirTemp("", "eino-ext-file-test")
    repoPath := filepath.Join(dir, "testowner", "testrepo")
    os.MkdirAll(repoPath, 0o755)
    repo, _ := git.PlainInit(repoPath, false)
    wt, _ := repo.Worktree()
    // write sample files
    os.WriteFile(filepath.Join(repoPath, "README.md"), []byte("line1\nline2\nline3\n"), 0o644)
    os.MkdirAll(filepath.Join(repoPath, "sub"), 0o755)
    os.WriteFile(filepath.Join(repoPath, "sub", "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644)
    os.WriteFile(filepath.Join(repoPath, "binary.bin"), []byte{0x00, 0x01, 0x02}, 0o644)
    wt.Add("README.md"); wt.Add("sub/main.go"); wt.Add("binary.bin")
    wt.Commit("initial", &git.CommitOptions{Author: commitIdentity})
    return dir, func() { os.RemoveAll(dir) }
}

func (s *GitHubToolTestSuite) fileConfigs(cloneDir string) Configs {
    return Configs{
        "test": {
            Token:    "test-token",
            CloneDir: cloneDir,
            BaseURL:  s.server.URL + "/",
        },
    }
}
```

### Test cases

**`github_file_read`**
1. `TestFileReadFull` — read full file, assert content and bytes.
2. `TestFileReadLineRange` — `startLine=2, endLine=3`, assert only those lines.
3. `TestFileReadStartLineOnly` — `startLine=2`, assert from line 2 to end.
4. `TestFileReadNotFound` — non-existent path, assert error contains "not found".
5. `TestFileReadPathTraversal` — `path="../../../etc/passwd"`, assert error contains "escapes clone directory".
6. `TestFileReadBinary` — `path="binary.bin"`, assert error contains "binary".
7. `TestFileReadNotCloned` — point at empty cloneDir, assert error mentions `github_repo_clone`.
8. `TestFileReadDotGit` — `path=".git/HEAD"`, assert error.
9. `TestFileReadEmptyFile` — write empty file, assert `content=""`, `bytes=0`.
10. `TestFileReadLargeFile` — write >1MB file, assert `truncated=true` and note present.

**`github_file_search`**
1. `TestFileSearchBasic` — pattern `"line"`, assert 3 matches in README.md.
2. `TestFileSearchRegex` — pattern `"func .*\\{"`, assert 1 match in sub/main.go.
3. `TestFileSearchPathPrefix` — `pathPrefix="sub"`, assert only sub/main.go matches.
4. `TestFileSearchMaxResults` — `maxResults=1`, assert exactly 1 result.
5. `TestFileSearchInvalidRegex` — pattern `"(invalid"`, assert error contains "invalid search pattern".
6. `TestFileSearchNoMatches` — pattern `"zzz"`, assert empty array.
7. `TestFileSearchSkipsBinary` — pattern that would match binary content, assert binary files skipped.
8. `TestFileSearchSkipsGit` — pattern `".*"`, assert no `.git/` paths in results.
9. `TestFileSearchNotCloned` — assert error.

**`github_file_list`**
1. `TestFileListRoot` — list root, assert README.md, sub/, binary.bin present.
2. `TestFileListSubPath` — `subPath="sub"`, assert main.go.
3. `TestFileListMaxDepth1` — default, assert no entries from sub/.
4. `TestFileListMaxDepth2` — `maxDepth=2`, assert sub/main.go included.
5. `TestFileListMaxResults` — `maxResults=1`, assert 1 entry.
6. `TestFileListSkipsGit` — assert no `.git` entries.
7. `TestFileListSkipsSymlinks` — create a symlink, assert it's absent.
8. `TestFileListNotCloned` — assert error.
9. `TestFileListPathTraversal` — `subPath="../"`, assert error.

**`github_file_write`**
1. `TestFileWriteDryRun` — `dryRun=true`, assert preview JSON, no file written.
2. `TestFileWriteConfirmed` — `confirmed=true`, assert file written, commit created, `pushed` field present. (Push will fail against mock server; test asserts the commit succeeded and the error is a push error, OR use a local bare repo as remote.)
3. `TestFileWriteNotConfirmed` — assert confirmation error.
4. `TestFileWriteNewBranch` — `branch="new-feature"`, `baseBranch="master"`, assert branch created and file written.
5. `TestFileWriteOverwrite` — write to existing file, assert content overwritten.
6. `TestFileWritePathTraversal` — `path="../../evil.txt"`, assert error.
7. `TestFileWriteNotCloned` — assert error.
8. `TestFileWriteCreatesParentDirs` — `path="deep/nested/file.txt"`, assert dirs created.
9. `TestFileWritePushRejection` — set up a remote with divergent history, assert push error (token redacted).

**Push testing strategy**: For push tests, create a bare git repo in a temp dir, set it as the remote, and push there. This avoids needing the mock HTTP server for git operations. The mock server is only used for the `BaseURL` resolution in `gitHost`.

## README.md Update

Add to the **Read Tools** table:

```
| `github_file_read` | Read file contents from a cloned repo |
| `github_file_search` | Grep (regex) within a cloned repo |
| `github_file_list` | List files/dirs in a cloned repo |
```

Add to the **Write Tools** table:

```
| `github_file_write` | Write/create a file, commit, and push |
```

Add a **File Tools** section explaining the clone-first workflow:

```markdown
### File Tools

File tools operate on repositories cloned by `github_repo_clone`. The workflow is:

1. `github_repo_clone` — clone the repo to <CloneDir>/<owner>/<repo>
2. `github_file_read` / `github_file_search` / `github_file_list` — inspect
3. `github_branch_create` (optional) — create a working branch
4. `github_file_write` — modify files, commit, push
5. `github_pr_create` — open a PR from the pushed branch

All file paths are validated to stay within the clone directory. Symlinks and
the `.git` directory are always skipped or rejected.
```

## Edge Cases Summary

| Edge case | Handling |
|-----------|----------|
| Repo not cloned | Error: "run github_repo_clone first" |
| File not found | Wrapped `os.IsNotExist` error |
| Path traversal (`..`) | `validateFilePath` rejects |
| Absolute path input | `validateFilePath` rejects |
| Symlink | `os.Lstat` check, skip or reject |
| `.git/` path | Explicit prefix check, reject |
| Binary file (read/search) | `isBinary` detects null bytes, refuse/skip |
| Empty file | Return `content=""`, `bytes=0` |
| Large file (>1MB) | Truncate, set `truncated=true` + note |
| Invalid regex (search) | `regexp.Compile` error wrapped |
| No matches (search) | Empty JSON array `[]` |
| Empty directory (list) | Empty JSON array `[]` |
| Branch doesn't exist (write) | Auto-create from `BaseBranch` or HEAD |
| Push rejection (write) | Wrapped error, token redacted, never force |
| DryRun mode | Return preview JSON, no side effects |
| Unconfirmed write | `confirm.RequireConfirmationForAction` error |

## Out of Scope

- **Checkup entries** for file tools (they're local FS operations, not API probes).
- **`.gitignore` support** in `github_file_search` (noted as future enhancement).
- **File deletion** (not in requirements).
- **File move/rename** (not in requirements).
- **Streaming** (file tools return complete results).
- **Changes to `github_pr_create`** (already exists, works with pushed branches as-is).
- **Configurable commit identity** (generic constant for now; can add `Config.AuthorName/AuthorEmail` later if needed).
- **Configurable truncation threshold** (1MB constant for now).

## Validation Checklist (for implementer)

- [ ] `go build ./...` passes
- [ ] `go vet ./...` passes
- [ ] `go test ./components/tool/github/...` passes
- [ ] Every new `Params` struct has `validate` + `jsonschema` tags
- [ ] Every `new...Tool` calls `validateParams` in `Invoke`
- [ ] All errors wrapped with `emperror.dev/errors`
- [ ] Token redaction in `file_write.go` push error path
- [ ] `var _ tool.InvokableTool = (*FileReadTool)(nil)` etc. in `registry.go`
- [ ] `WriteToolNames()` includes `github_file_write`
- [ ] README.md updated with new tools and workflow section
- [ ] No license banners added
- [ ] No duplication of `sanitizeSegment`/`clonePath`/`filter.Compile` — reuse where appropriate
