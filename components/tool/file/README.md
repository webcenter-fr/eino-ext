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

Read-only setups can use `file.NewReadOnlyTools(ctx, cfg)`, which creates only
`file_read`. `file.WriteToolNames()` returns the names of all write tools
(useful for filtering a ToolsNode).

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

## Checkup

`Check(ctx, cfg)` probes that the configured `Workdir` is a valid root and is
writable, returning `checkup.Results` for integration with the shared checkup
tooling.

## Security

- All paths are validated to stay within the session directory
  (`fileutil.ValidateRelativePath` + `fileutil.ResolveSymlinkSafe`).
- Symlinks at any path component are rejected.
- Binary files are detected and refused by `file_read`.
- Content size limits prevent resource exhaustion (`MaxReadBytes`,
  `MaxWriteBytes`).
- The session root cannot be deleted.
- GC only removes directories, never regular files in Workdir.

## Requirements

- A writable directory specified in `Config.Workdir`.
- The directory must not be a system directory (/, /etc, /proc, etc.).
