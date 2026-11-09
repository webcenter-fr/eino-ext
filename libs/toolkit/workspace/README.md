# `libs/toolkit/workspace`

Shared on-disk workspace layout for the GitHub clone tools and the Dagger-backed
shell tool. The host filesystem is the single source of truth: the shell tool
mounts a workspace directory into a container and only writes container changes
back on an explicit opt-in.

## Layout

```
<Root>/<sessionSegment>/<owner>/<repo>
```

- An empty session id maps to the literal `default` segment.
- A non-empty session id is hashed with `fileutil.SessionDirName` (16 hex chars)
  so distinct ids never collide.
- `owner` and `repo` are sanitized with `fileutil.SanitizePathSegment`.

## Usage

```go
layout, err := workspace.NewLayout(&workspace.Layout{
    Root:           "/var/lib/eino/workspaces",
    SessionKey:     github.CloneSessionKey, // share the GitHub session namespace
    RequireSession: true,
})
if err != nil {
    return err
}

// <Root>/<sessionHash>/<owner>/<repo>
repoDir, err := layout.RepoDir(ctx, "owner", "repo")

// Refresh the session mtime so the GC keeps it.
err = layout.Touch(ctx)

// Sweep session namespaces idle for longer than ttl.
layout.StartGC(ctx, time.Hour, 5*time.Minute)

// Stat-and-evict helper for cached containers.
exists, err := workspace.Exists(repoDir)
```

`ResolvePath(root, relPath, createDirs)` validates a relative path under a
workspace root and rejects symlinks at every component.

## Checkup

`Check(ctx, layout)` returns `checkup.Results` and validates the root directory
and session resolution (including the fail-closed `RequireSession` behaviour).
