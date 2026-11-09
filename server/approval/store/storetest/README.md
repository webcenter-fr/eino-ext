# storetest — reusable `ApprovalStore` conformance suite

`storetest` is a test-helper package (not a runtime dependency). It provides one
suite that every `ApprovalStore` backend runs, so all backends share identical
semantics.

## Usage

```go
func TestMyStoreConformance(t *testing.T) {
    storetest.Run(t, func(t *testing.T) store.ApprovalStore {
        s, err := New(context.Background(), &Config{…})
        require.NoError(t, err)
        t.Cleanup(func() { _ = s.Close() })
        return s
    })
}
```

The factory must return a fresh, isolated store for each subtest.

## Coverage

- create/get, missing record
- pending listing with filters and ordering
- approve/reject, double-decide rejection, invalid decision
- TTL expiry (get and decide)
- `WaitDecision` waking on decide and timing out while pending
- GC removing records past retention

`conformance_test.go` runs the suite against the in-memory store to guard the
helper itself.

## Notes

- Backends that need an external service gate their test on an environment
  variable (`REDIS_ADDR`, `POSTGRES_DSN`, `OPENSEARCH_URL`) and skip cleanly
  when it is unset.
- The suite uses short waits (≤2s) so it stays fast.
