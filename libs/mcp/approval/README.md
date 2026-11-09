# approval — MCP-server-side approval client

`libs/mcp/approval` is the lightweight client MCP servers use to talk to the
standalone [approval service](../../../server/approval). It owns the canonical
`argsHash`, signed receipt issue/verify, and the REST client (register +
long-poll). It has no OIDC, cookie, SPA or store dependencies, so importing it
does not pull the approval service into the MCP binaries.

## Pieces

| File | Contents |
|---|---|
| `canonical.go` | `Canonicalize`, `ArgsHash` — RFC 8785-style canonical JSON |
| `receipt.go` | `ReceiptSigner`, `ReceiptClaims`, `ReceiptConfig` (HS256/RS256/EdDSA) |
| `client.go` | `Client` — `Register`, `Decision`, `WaitDecision`, `VerifyReceipt` |

## `argsHash` canonicalization

SHA-256 over JCS-style canonical JSON (sorted keys, no insignificant whitespace,
stable number formatting) of the tool arguments, **excluding** `dryRun`,
`confirmed` and `approvalId`. Both the MCP server and the approval service must
produce identical bytes; the canonicalization vectors are unit-tested.

```go
hash, err := approval.ArgsHash(json.RawMessage(`{"b":2,"a":1,"confirmed":true}`))
// hash == SHA-256(`{"a":1,"b":2}`)
```

## Client

```go
client, err := approval.NewClient(approval.Config{
    BaseURL:     "https://approve.example.com",
    ServerName:  "grafana",
    ServerToken: "s3cr3t",
    Receipt: approval.ReceiptConfig{
        Secret:    []byte("receipt-secret"), // or PrivateKeyPEM + Algorithm
        Algorithm: approval.AlgorithmHS256,
    },
    PollInterval:   250 * time.Millisecond,
    RequestTimeout: 35 * time.Second,
})

registered, err := client.Register(ctx, approval.RegisterRequest{
    ToolName: "grafana_dashboard_write",
    Instance: "prod",
    Args:     args,
    ArgsHash: hash,
    Requester: approval.Requester{Sub: "alice", User: "alice"},
})

decision, err := client.WaitDecision(ctx, registered.ApprovalID, 5*time.Minute)
claims, err := client.VerifyReceipt(decision.Receipt, approval.Expected{
    ServerName: "grafana",
    ToolName:   "grafana_dashboard_write",
    Instance:   "prod",
    ArgsHash:   hash,
})
```

`WaitDecision` long-polls in slices of at most 30s with exponential backoff on
`ErrServiceUnavailable`, until the decision is final or the timeout elapses. A
timeout returns `ErrTimeout` while the record may still be pending server-side,
so a later `approvalId` re-call can succeed.

## Receipt verification

`VerifyReceipt` checks, in order: signature and algorithm → `exp`/`iat` →
`decision == approve` → `serverName`/`toolName`/`instance` match the call →
`argsHash` matches the args about to execute. Any failure returns
`ErrReceiptInvalid` or `ErrHashMismatch`; callers must fail closed.

Receipts are reusable within their short `exp` (default 2m); every use is
audited with the `jti`.

## Errors

`ErrNotFound`, `ErrUnauthorized`, `ErrForbidden`, `ErrBadRequest`,
`ErrHashMismatch`, `ErrDenied`, `ErrExpired`, `ErrTimeout`,
`ErrServiceUnavailable`, `ErrReceiptInvalid`. All are wrapped with
`emperror.dev/errors` and comparable with `errors.Is`.

## Security notes

- Never trust a receipt without recomputing `argsHash` from the exact args about
  to execute.
- Never downgrade to elicitation when the service is unreachable or returns 5xx;
  fail closed.
- The server token is sent as a bearer token to the approval service only; the
  end user's MCP token is never forwarded.
