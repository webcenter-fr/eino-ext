# redis — Redis-backed `ApprovalStore`

Stores approval records in Redis with native key TTLs and wakes long-pollers
through pub/sub. Suitable for HA approval-server replicas sharing one Redis.

## Configuration

```go
st, err := redis.New(ctx, &redis.Config{
    Addr:      "localhost:6379", // required
    Password:  "",               // optional
    DB:        0,
    Prefix:    "approval:",      // default "approval:"
    TTL:       15 * time.Minute, // default 15m
    Retention: time.Hour,        // default 1h after expiry
})
defer st.Close()
```

`New` pings Redis and fails if it is unreachable.

## Usage

```go
if err := st.Create(ctx, rec); err != nil { … }
decided, err := st.WaitDecision(ctx, rec.ID, 30*time.Second)
```

Keys: `<prefix>record:<id>` (JSON wrapper with the record and expiry),
`<prefix>pending` (sorted set ordered by creation time) and
`<prefix>decided:<id>` (pub/sub channel). `Decide` runs a Lua script so the
state transition is atomic; it publishes the new state to wake waiters.

## Security notes

- Use Redis AUTH/TLS and network policies; records contain full tool arguments.
- The key prefix isolates approval data from other tenants of the same Redis.
- `Decide` is atomic (Lua) and rejects already-decided or expired records.
- Integration tests are gated on `REDIS_ADDR` and skip cleanly when unset.
