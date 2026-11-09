# postgresql — PostgreSQL-backed `ApprovalStore`

Stores approval records in a PostgreSQL table. Decisions are delivered to
long-pollers through `LISTEN/NOTIFY`; GC is a separate job (call `GC`
periodically, as `cmd` does). Suitable for HA approval-server replicas sharing
one database.

## Configuration

```go
st, err := postgresql.New(ctx, &postgresql.Config{
    DSN:       "postgres://user:pass@localhost:5432/approval?sslmode=require", // required
    Table:     "approval_records", // default
    TTL:       15 * time.Minute,   // default 15m
    Retention: time.Hour,          // default 1h after expiry
})
defer st.Close()
```

`New` opens the connection, pings the database and creates the table and pending
index when missing. The table name is validated against
`^[A-Za-z_][A-Za-z0-9_]*$` before being interpolated into DDL/DML.

## Usage

```go
if err := st.Create(ctx, rec); err != nil { … }
decided, err := st.WaitDecision(ctx, rec.ID, 30*time.Second)
```

`Decide` runs in a transaction with `SELECT … FOR UPDATE`, so concurrent
decisions cannot both win. It emits `pg_notify('approval_decided', id)` before
committing.

## Security notes

- Use TLS (`sslmode=require`/`verify-full`) and a least-privilege role; records
  contain full tool arguments.
- The table name is validated to prevent SQL injection through configuration.
- Integration tests are gated on `POSTGRES_DSN` and skip cleanly when unset.
