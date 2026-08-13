# Plan: Extract Filesystem Helpers to `libs/toolkit/fileutil/`

## Goal

Move the pure-filesystem helpers currently living in `components/tool/github/helper.go` and `components/tool/github/base.go` into a new shared package `libs/toolkit/fileutil/`, so that future components can reuse them without depending on the GitHub component. Keep GitHub-specific coupling (clone layout, git operations, `Instance`/`Owner`/`Repo` params, "run github_repo_clone first" messaging) in the GitHub package.

## Scope

In scope:
- Create `libs/toolkit/fileutil/` with extracted, generic filesystem safety helpers.
- Refactor `components/tool/github/{helper.go,base.go,file_read.go,file_list.go,file_search.go,file_write.go}` to import from `fileutil`.
- Update `components/tool/github/file_test.go` expectations that depend on the old GitHub-specific error strings.
- Add unit tests for `fileutil`.

Out of scope:
- Creating a generic `components/tool/file/` component (see decision below).
- Changing the shell tool. The shell tool runs commands inside Dagger containers; host-side filesystem helpers do not address its needs.
- Changing `clonePath`, `sanitizeSegment`, `ensureCloneExists` — these stay GitHub-specific.
- Changing git operations in `file_write.go` / `branch_create.go` / `repo_clone.go`.

## Decision: Do NOT create a generic `components/tool/file/` component

Rationale:
1. **No current consumer.** The only existing file-operation consumer is the GitHub component. The shell tool executes inside Dagger containers — its filesystem view is the container's, not the host's, so a host-side file tool would not let the LLM inspect container state. Adding a generic file tool now would be speculative.
2. **Different security model.** A generic file tool would need to answer "which root directory?" — the GitHub tool answers this via `cloneDir/<owner>/<repo>`, the shell tool via `Workdir` mounted into a container. There is no single natural answer for "a generic file tool."
3. **YAGNI / over-abstraction risk.** The reusable part is the *helpers* (path validation, symlink rejection, binary detection), not the *tool surface*. Extracting helpers to `libs/toolkit/fileutil/` captures 100% of the cross-component value without committing to a tool API that no consumer has asked for.
4. **Easy to add later.** If a future agent needs a host-side file tool, it can be built on top of `fileutil` in a few dozen lines. Deferring avoids guessing the wrong API now.

The plan therefore extracts helpers only. A `components/tool/file/` component is explicitly out of scope and should be revisited when a concrete second consumer exists.

## What gets extracted vs. what stays

### Extracted to `libs/toolkit/fileutil/`

| Current location | Current name | New name | Notes |
|---|---|---|---|
| `helper.go` | `validateFilePath` | `ValidateRelativePath` | Generic; error text changes "clone directory" → "root directory" |
| `helper.go` | `isWithinPath` | `IsWithinPath` | Pure path containment |
| `helper.go` | `resolveSymlinkSafe` | `ResolveSymlinkSafe` | Pure symlink rejection |
| `helper.go` | `isBinary` | `IsBinary` | Null-byte heuristic |
| `helper.go` | `rejectDotGitPath` | `RejectDotGitPath` | Generic VCS-metadata exclusion (still named for `.git` since that is what it checks) |
| `helper.go` | `maxFileReadBytes` / `maxFileWriteBytes` / `maxSearchFileBytes` | `DefaultMaxReadBytes` / `DefaultMaxWriteBytes` / `DefaultMaxSearchFileBytes` | Exported consts |
| `base.go` | `validateCloneDir` + `systemDirs` | `ValidateRootDir` + internal `systemDirs` | Generic host-root validation; GitHub wraps the error with "CloneDir" context |

### Stays in `components/tool/github/`

| Function | Why it stays |
|---|---|
| `clonePath(cloneDir, owner, repo)` | Encodes the GitHub clone layout `<cloneDir>/<owner>/<repo>`. |
| `sanitizeSegment(s)` | Sanitizes owner/repo names — GitHub-specific input shaping. |
| `ensureCloneExists(clonePath_, owner, repo)` | Error message references `github_repo_clone`. |
| `commitIdentity` | Git commit author identity. |
| `labelList`, `truncate`, `paginateList`, `filterMapMarshal`, `applyExcludes`, `stringPtr`, `boolPtr` | GitHub-API helpers, unrelated to filesystem ops. |
| `baseTool`, `newBaseTool`, `client`, `token`, `gitHost`, `instanceSchemaModifier`, `instanceNotFoundError` | GitHub client state. |
| All git operations in `file_write.go` (`git.PlainOpen`, checkout, add, commit, push) | GitHub-specific. |

## `libs/toolkit/fileutil/` package design

**Import path:** `github.com/webcenter-fr/eino-ext/libs/toolkit/fileutil`
**Package name:** `fileutil`

### Files

| File | Contents |
|---|---|
| `fileutil.go` | Package comment, `ValidateRelativePath`, `IsWithinPath`, `ResolveSymlinkSafe`, `IsBinary`, `RejectDotGitPath`, `ValidateRootDir`, internal `systemDirs` map. |
| `limits.go` | `DefaultMaxReadBytes`, `DefaultMaxWriteBytes`, `DefaultMaxSearchFileBytes` consts. |
| `fileutil_test.go` | Table-driven unit tests for every exported function. |
| `README.md` | Package purpose, function index, usage example. |

### Function signatures

```go
// Package fileutil provides pure filesystem safety helpers for validating
// paths under a root directory, rejecting symlinks at every component, detecting
// binary content, excluding VCS metadata directories, and validating host-side
// root directories. It contains no business logic and returns plain errors;
// callers wrap them with component-specific context using emperror.dev/errors.
package fileutil

// Default size limits for file operations. Components may use these directly
// or define their own.
const (
	DefaultMaxReadBytes       = 1 << 20  // 1MB  — truncation threshold for reads.
	DefaultMaxWriteBytes      = 10 << 20 // 10MB — max content size for writes.
	DefaultMaxSearchFileBytes = 10 << 20 // 10MB — skip files larger than this in search.
)

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
func ValidateRelativePath(root, relPath string) (string, error)

// IsWithinPath returns true if path is equal to root or a descendant of root.
// Both paths are cleaned before comparison.
func IsWithinPath(path, root string) bool

// ResolveSymlinkSafe walks each component of fullPath from root, rejecting
// any component that is a symlink. This prevents symlink-based directory
// traversal where a malicious repo contains a symlink (e.g., "link -> /etc")
// that would redirect file reads/writes outside the root.
//
// If createDirs is true, missing intermediate directories are created (for
// writes). If createDirs is false, missing intermediate components cause an
// error; a missing final component is allowed (the caller will get a
// not-exist error from the subsequent file operation).
//
// The returned path is verified to be within root.
func ResolveSymlinkSafe(root, fullPath string, createDirs bool) (string, error)

// IsBinary returns true if the first 8KB of data contain a null byte, which
// is the standard heuristic for detecting binary files.
func IsBinary(data []byte) bool

// RejectDotGitPath returns an error if the path refers to the .git directory
// at any level. The path is cleaned before checking so that prefixes like
// "./" or "subdir/../" cannot bypass the check.
func RejectDotGitPath(path string) error

// ValidateRootDir validates that a directory path is safe to use as a
// host-side root for filesystem operations. It rejects:
//   - empty paths
//   - relative paths
//   - paths containing ".."
//   - system directories (/, /etc, /bin, /usr, /var, /proc, /sys, /dev, /tmp)
//   - paths under /proc/, /sys/, /dev/
//
// Callers that need component-specific error context (e.g. "CloneDir") should
// wrap the returned error with emperror.dev/errors.
func ValidateRootDir(path string) error
```

### Error message policy

`fileutil` returns **plain, generic errors** using `emperror.dev/errors` with terms like `root`, `root directory`, `path`. It must NOT mention GitHub-specific concepts (`clone`, `clone directory`, `github_repo_clone`).

Examples:
- `ValidateRelativePath`: `"path %q escapes root directory %q"`, `"path contains a null byte"`, `"path must be relative, got absolute path %q"`, `"path %q contains directory traversal"`.
- `ResolveSymlinkSafe`: `"path escapes root directory"`, `"symlink at path component %q; symlinks are not allowed"`, `"path component %q is not a directory"`.
- `RejectDotGitPath`: `"access to .git directory is not allowed"`.
- `ValidateRootDir`: `"root directory is required"`, `"root directory must be an absolute path, got %q"`, `"root directory must not contain directory traversal, got %q"`, `"root directory must not be a system directory, got %q"`, `"root directory must not be under a system mount, got %q"`.

## GitHub refactor

### `components/tool/github/helper.go`

- Delete: `validateFilePath`, `isWithinPath`, `resolveSymlinkSafe`, `isBinary`, `rejectDotGitPath`.
- Delete: the `maxFileReadBytes` / `maxFileWriteBytes` / `maxSearchFileBytes` const block.
- Remove now-unused imports (`bytes`, `path/filepath` if unused elsewhere, etc.). Keep `path/filepath` if still used by `clonePath`/`sanitizeSegment`.
- Keep: `clonePath`, `sanitizeSegment`, `ensureCloneExists`, `labelList`, `truncate`, `paginateList`, `filterMapMarshal`, `applyExcludes`, `stringPtr`, `boolPtr`, `commitIdentity`, `defaultMaxPages`, embedded prompt vars.
- Add import: `github.com/webcenter-fr/eino-ext/libs/toolkit/fileutil` (only if any remaining code in this file uses it — likely not, since the file helpers are removed).

### `components/tool/github/base.go`

- Replace the body of `validateCloneDir` with a thin wrapper:
  ```go
  func validateCloneDir(cloneDir string) error {
      if err := fileutil.ValidateRootDir(cloneDir); err != nil {
          return errors.Wrap(err, "CloneDir")
      }
      return nil
  }
  ```
  This preserves the existing test behavior (errors mention `CloneDir`) while delegating the actual checks to `fileutil`.
- Delete the local `systemDirs` map (now lives in `fileutil`).
- Add import: `github.com/webcenter-fr/eino-ext/libs/toolkit/fileutil`.
- Remove now-unused imports (`path/filepath`, `strings` if no longer used).

### `components/tool/github/file_read.go`

- `rejectDotGitPath(params.Path)` → `fileutil.RejectDotGitPath(params.Path)`
- `validateFilePath(clonePath_, params.Path)` → `fileutil.ValidateRelativePath(clonePath_, params.Path)`
- `resolveSymlinkSafe(clonePath_, fullPath, false)` → `fileutil.ResolveSymlinkSafe(clonePath_, fullPath, false)`
- `isBinary(data)` → `fileutil.IsBinary(data)`
- `maxFileReadBytes` → `fileutil.DefaultMaxReadBytes`
- Add import: `github.com/webcenter-fr/eino-ext/libs/toolkit/fileutil`.

### `components/tool/github/file_list.go`

- `rejectDotGitPath(params.SubPath)` → `fileutil.RejectDotGitPath(params.SubPath)`
- `validateFilePath(clonePath_, params.SubPath)` → `fileutil.ValidateRelativePath(clonePath_, params.SubPath)`
- Add import: `github.com/webcenter-fr/eino-ext/libs/toolkit/fileutil`.
- Note: `file_list.go` does not currently use `resolveSymlinkSafe` for the walk — it does inline `os.Lstat` + `os.ModeSymlink` checks. Leave that logic as-is (it is walk-specific) unless the implementer finds a clean factoring; do not force it.

### `components/tool/github/file_search.go`

- `rejectDotGitPath(params.PathPrefix)` → `fileutil.RejectDotGitPath(params.PathPrefix)`
- `validateFilePath(clonePath_, params.PathPrefix)` → `fileutil.ValidateRelativePath(clonePath_, params.PathPrefix)`
- `isBinary(data)` → `fileutil.IsBinary(data)`
- `maxSearchFileBytes` → `fileutil.DefaultMaxSearchFileBytes`
- Add import: `github.com/webcenter-fr/eino-ext/libs/toolkit/fileutil`.

### `components/tool/github/file_write.go`

- `rejectDotGitPath(params.Path)` → `fileutil.RejectDotGitPath(params.Path)`
- `validateFilePath(clonePath_, params.Path)` → `fileutil.ValidateRelativePath(clonePath_, params.Path)`
- `resolveSymlinkSafe(clonePath_, fullPath, true)` → `fileutil.ResolveSymlinkSafe(clonePath_, fullPath, true)`
- `maxFileWriteBytes` → `fileutil.DefaultMaxWriteBytes`
- Add import: `github.com/webcenter-fr/eino-ext/libs/toolkit/fileutil`.
- Git operations (`git.PlainOpen`, checkout, add, commit, push) stay unchanged.

## Test changes

### New: `libs/toolkit/fileutil/fileutil_test.go`

Table-driven tests, no external dependencies, no network. Cover:

- `ValidateRelativePath`:
  - empty relPath returns cleaned root
  - normal relative path resolves
  - forward slashes normalized on Windows (skip if not relevant)
  - absolute path rejected
  - NUL byte rejected
  - `..` escaping root rejected
  - `..` that stays inside root (e.g. `sub/../file`) allowed
  - path with trailing `..` rejected
- `IsWithinPath`:
  - equal paths
  - descendant
  - sibling (false)
  - prefix-but-not-descendant (e.g. `/foo/bar` vs `/foo/barbaz` → false)
- `ResolveSymlinkSafe`:
  - existing path with no symlinks
  - symlink at intermediate component rejected
  - symlink at final component rejected
  - missing intermediate component with `createDirs=true` creates it
  - missing intermediate component with `createDirs=false` errors
  - missing final component allowed (returns path, no error)
  - path escaping root rejected
- `IsBinary`:
  - text data → false
  - data with NUL in first 8KB → true
  - data with NUL after 8KB → false
  - empty data → false
- `RejectDotGitPath`:
  - empty path → no error
  - `.git` → error
  - `.git/HEAD` → error
  - `./.git/HEAD` → error (cleaning)
  - `subdir/../.git/HEAD` → error (cleaning)
  - `subdir/.git/HEAD` → error (nested component)
  - normal path → no error
- `ValidateRootDir`:
  - empty → error
  - relative → error
  - contains `..` → error
  - system dir (`/etc`, `/proc`, etc.) → error
  - under `/proc/` → error
  - valid absolute path → no error

### Modified: `components/tool/github/file_test.go`

The only test assertions that depend on GitHub-specific error wording:

- `TestFileReadPathTraversal`: `s.Contains(err.Error(), "escapes clone directory")` → change to `s.Contains(err.Error(), "escapes root directory")` (matches new `fileutil.ValidateRelativePath` message).
- `TestFileListPathTraversal`: same change.
- `TestFileWritePathTraversal`: same change.
- `TestFileReadNotCloned` / `TestFileListNotCloned` / `TestFileSearchNotCloned`: still expect `"github_repo_clone"` — `ensureCloneExists` stays in GitHub, so these are unchanged.
- `TestFileReadDotGit` / `TestFileReadDotGitBypass` / `TestFileWriteDotGitBypass`: still expect `".git"` — `RejectDotGitPath` keeps the same message, so these are unchanged.
- `TestFileReadSymlinkTraversal` / `TestFileWriteSymlinkTraversal`: still expect `"symlink"` — `ResolveSymlinkSafe` keeps the same message, so these are unchanged.
- `TestFileReadLargeFile`: uses `maxFileReadBytes+100` → change to `fileutil.DefaultMaxReadBytes+100`. Add import of `fileutil` to the test file.

All other tests should pass unchanged.

## Import paths and package naming

- New package: `github.com/webcenter-fr/eino-ext/libs/toolkit/fileutil`, package name `fileutil`.
- GitHub component imports it as `"github.com/webcenter-fr/eino-ext/libs/toolkit/fileutil"` (no alias needed; no name conflict with the `github` package or its `ghlib` alias).
- Follow CONTRIBUTING.md: no license banner, package comment at top, `emperror.dev/errors` for error wrapping.

## Error handling considerations

- `fileutil` returns plain errors (generic wording, no component context).
- GitHub tools that need component-specific context wrap with `errors.Wrap`/`errors.Wrapf`. In practice, the GitHub tools currently pass the file-helper errors through directly; the only wrapping needed is in `validateCloneDir` (wraps `ValidateRootDir` with `"CloneDir"`).
- The `ensureCloneExists` function stays in GitHub because its error message is intentionally GitHub-specific (`"run github_repo_clone first"`).
- No sentinel errors are introduced. Callers continue to match on `os.IsNotExist` and string `Contains` as today.

## Risks and mitigations

| Risk | Mitigation |
|---|---|
| Error message changes break tests | Update the three `*PathTraversal` test assertions; verify all other assertions still match. |
| Subtle behavior change in `ValidateRelativePath` vs. old `validateFilePath` | Keep the implementation byte-for-byte identical except for error strings; cover with `fileutil_test.go`. |
| `ValidateRootDir` semantics drift from `validateCloneDir` | `validateCloneDir` becomes a thin wrapper, so behavior is preserved by construction. |
| Future shell-tool integration is harder without a generic file tool | `fileutil` provides all the building blocks; a future `components/tool/file/` can be added in ~100 lines if a real consumer appears. |
| `RejectDotGitPath` is git-specific, arguably not "generic" | It checks a path string for `.git` components — no git dependency. It is generic in the sense that any tool operating on a git clone (or any tool that wants to exclude VCS metadata) can use it. Keep the name; document the scope. |

## Validation plan

1. `go build ./...` — confirm everything compiles.
2. `go vet ./...` — no new warnings.
3. `go test ./libs/toolkit/fileutil/...` — new unit tests pass.
4. `go test ./components/tool/github/...` — all existing GitHub tests pass with the updated assertions.
5. Manual grep: confirm no remaining references to the deleted helpers (`validateFilePath`, `resolveSymlinkSafe`, `isBinary`, `rejectDotGitPath`, `isWithinPath`, `maxFileReadBytes`, `maxFileWriteBytes`, `maxSearchFileBytes`) inside `components/tool/github/` except where intentionally kept.
6. Confirm `libs/toolkit/fileutil/` has `fileutil.go`, `limits.go`, `fileutil_test.go`, `README.md` (CONTRIBUTING.md requires README for new packages — note: this is a `libs/toolkit/` package, not a component, so the component checklist does not strictly apply, but a README is still expected per existing `libs/toolkit/strutil/README.md` and `libs/toolkit/safety/README.md`).

## Ordered task list

1. Create `libs/toolkit/fileutil/limits.go` with the three `Default*` consts.
2. Create `libs/toolkit/fileutil/fileutil.go` with the six exported functions and the internal `systemDirs` map. Copy implementations from `helper.go` / `base.go`, changing error strings from "clone directory" to "root directory" and from "CloneDir" to "root directory".
3. Create `libs/toolkit/fileutil/fileutil_test.go` with the table-driven tests listed above.
4. Create `libs/toolkit/fileutil/README.md` describing the package and listing each function with a one-line summary and a short usage example.
5. Edit `components/tool/github/base.go`: replace `validateCloneDir` body with the `fileutil.ValidateRootDir` wrapper; delete the local `systemDirs` map; add the `fileutil` import; remove unused imports.
6. Edit `components/tool/github/helper.go`: delete `validateFilePath`, `isWithinPath`, `resolveSymlinkSafe`, `isBinary`, `rejectDotGitPath`, and the `maxFileReadBytes`/`maxFileWriteBytes`/`maxSearchFileBytes` const block; remove now-unused imports (`bytes`).
7. Edit `components/tool/github/file_read.go`: replace local helper calls with `fileutil.*`; replace `maxFileReadBytes` with `fileutil.DefaultMaxReadBytes`; add `fileutil` import.
8. Edit `components/tool/github/file_list.go`: replace `rejectDotGitPath` and `validateFilePath` calls with `fileutil.*`; add `fileutil` import.
9. Edit `components/tool/github/file_search.go`: replace local helper calls with `fileutil.*`; replace `maxSearchFileBytes` with `fileutil.DefaultMaxSearchFileBytes`; add `fileutil` import.
10. Edit `components/tool/github/file_write.go`: replace local helper calls with `fileutil.*`; replace `maxFileWriteBytes` with `fileutil.DefaultMaxWriteBytes`; add `fileutil` import.
11. Edit `components/tool/github/file_test.go`: update the three `*PathTraversal` assertions to expect `"escapes root directory"`; update `TestFileReadLargeFile` to use `fileutil.DefaultMaxReadBytes`; add `fileutil` import.
12. Run `go build ./...`, `go vet ./...`, `go test ./libs/toolkit/fileutil/...`, `go test ./components/tool/github/...`. Fix any remaining issues.

## Open questions

None — all design decisions are resolved above. The plan is implementation-ready.
