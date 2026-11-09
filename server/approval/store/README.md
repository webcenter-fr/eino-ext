# store — approval record model and `ApprovalStore` interface

`server/approval/store` defines the approval record model, lifecycle states and
the `ApprovalStore` interface shared by every backend. It also ships the default
in-memory implementation.

## Model

```go
type State string // pending | approved | rejected | expired

type Record struct {
    ID, ServerName, ToolName, Instance, Operation string
    Args        json.RawMessage
    ArgsHash    string
    Preview     string
    PreviewWarning string
    Requester   Requester
    State       State
    Policy      string
    CreatedAt, ExpiresAt time.Time
    DecidedAt   *time.Time
    ApproverSub, ApproverUser, Decision, ReceiptJTI string
}
```

`DefaultTTL` is 15m (longer than the MCP client timeout, so a decision survives a
client that gave up polling). `DefaultRetention` is 1h after expiry before GC
removes a record.

## Interface

```go
type ApprovalStore interface {
    Create(ctx context.Context, rec *Record) error
    Get(ctx context.Context, id string) (*Record, error)
    ListPending(ctx context.Context, filter ListFilter) ([]*Record, error)
    Decide(ctx context.Context, id string, d Decision) (*Record, error)
    WaitDecision(ctx context.Context, id string, wait time.Duration) (*Record, error)
    GC(ctx context.Context, now time.Time) (int, error)
}
```

Implementations must be safe for concurrent use. `Get` transitions a pending
record past its expiry to `expired`; `Decide` fails with `ErrAlreadyDecided`
when the record is no longer pending and `ErrExpired` when it has expired.

## In-memory default

```go
st := store.NewMemory(
    store.WithTTL(15 * time.Minute),
    store.WithRetention(time.Hour),
)
```

`MemoryStore` is valid for a single approval-server replica. Persistent backends
exist for restart survival and HA, not for cross-server sharing (the approval
service owns all state).

## Helpers

- `ApplyDefaults(rec, now, ttl)` fills `ID`, `State`, `CreatedAt`, `ExpiresAt`.
- `ExpireIfNeeded(rec, now)` transitions an overdue pending record.
- `Clone(rec)` deep-copies a record (args and groups).
- `PollDecision(ctx, store, id, wait, interval)` is the shared polling fallback
  for backends without a native wake-up mechanism.

## Backends

| Package | Mechanism |
|---|---|
| [`file`](./file) | one JSON file per record, atomic rename, mtime TTL |
| [`redis`](./redis) | native key TTL + pub/sub wake-up |
| [`postgresql`](./postgresql) | LISTEN/NOTIFY + GC job |
| [`opensearch`](./opensearch) | `libs/toolkit/osclient` v4 |

[`storetest`](./storetest) provides the conformance suite every backend runs.

## Security notes

- `NewID` returns a 256-bit random, URL-safe identifier; it is an opaque lookup
  key with no authority.
- The file backend rejects path traversal in record ids.
- Store records contain full tool arguments; protect the backing store
  (filesystem permissions, Redis/PostgreSQL/OpenSearch auth and network policy)
  as you would any audit data.
