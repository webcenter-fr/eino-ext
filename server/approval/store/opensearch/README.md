# opensearch — OpenSearch-backed `ApprovalStore`

Stores approval records in an OpenSearch index through the shared
[`libs/toolkit/osclient`](../../../../libs/toolkit/osclient) v4 client. Useful
when approval records should live alongside existing operational search data.

## Configuration

```go
st, err := opensearch.New(ctx, &opensearch.Config{
    Config: osclient.Config{URLs: []string{"https://opensearch.example.com"}},
    Index:  "approval-records", // required
    TTL:    15 * time.Minute,   // default 15m
    Retention: time.Hour,       // default 1h after expiry
})
```

`New` builds the client and creates the index with keyword mappings for
`state`, `serverName`, `toolName`, `instance` and long mappings for the expiry
timestamps when it does not exist.

## Usage

```go
if err := st.Create(ctx, rec); err != nil { … }
decided, err := st.WaitDecision(ctx, rec.ID, 30*time.Second)
```

`WaitDecision` uses the shared `store.PollDecision` fallback (250ms interval).
`ListPending` queries `state=pending` with `expiresAtUnix > now`, filtered by
server/instance/tool and sorted by creation time.

## Security notes

- Use OpenSearch authentication/TLS and index-level permissions; records contain
  full tool arguments.
- The index name is configuration-controlled; do not derive it from user input.
- Integration tests are gated on `OPENSEARCH_URL` and skip cleanly when unset.
