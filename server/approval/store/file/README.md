# file — file-backed `ApprovalStore`

One JSON file per approval record. Writes go to a temporary file in the same
directory and are atomically renamed into place; the file mtime tracks the
record expiry for GC. Suitable for a single approval-server replica that needs
restart survival without an external database.

## Configuration

```go
st, err := file.New(ctx, &file.Config{
    Dir:       "/var/lib/approval", // required
    TTL:       15 * time.Minute,    // default 15m
    Retention: time.Hour,           // default 1h after expiry
})
```

`New` creates the directory with mode `0700` when needed and validates the
config.

## Usage

```go
rec := &store.Record{
    ServerName: "grafana",
    ToolName:   "grafana_dashboard_write",
    Args:       json.RawMessage(`{"dashboard":"{}"}`),
    ArgsHash:   hash,
    Requester:  store.Requester{Sub: "alice"},
}
if err := st.Create(ctx, rec); err != nil { … }
```

`WaitDecision` uses the shared `store.PollDecision` fallback (250ms interval).

## Security notes

- Record ids are validated against `^[A-Za-z0-9_-]+$` before being used as a
  filename, preventing path traversal.
- The directory is created `0700`; records contain full tool arguments, so keep
  the directory on a volume with restrictive permissions and appropriate
  backups.
- Atomic rename avoids torn reads but does not provide multi-writer
  coordination; use a single replica or a shared backend for HA.
