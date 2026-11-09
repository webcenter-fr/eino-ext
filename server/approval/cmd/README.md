# cmd — standalone approval server binary

Thin `main` for the approval service: parse flags, select a store backend, build
the server, run a GC loop and shut down gracefully on `SIGINT`/`SIGTERM`.

## Build and run

```bash
go build -o approval ./server/approval/cmd

./approval \
  -addr :8080 \
  -base-url https://approve.example.com \
  -store memory \
  -receipt-secret "$RECEIPT_SECRET" \
  -server-credential 'grafana=s3cr3t@prod,staging' \
  -server-credential 'kubernetes=sha256:<hex-digest>' \
  -oidc-issuer https://idp.example.com \
  -oidc-client-id approval \
  -oidc-client-secret "$OIDC_SECRET" \
  -oidc-redirect-url https://approve.example.com/auth/callback
```

## Flags

| Flag | Default | Purpose |
|---|---|---|
| `-addr` | `:8080` | HTTP listen address |
| `-base-url` | derived from `-addr` | Public base URL for approval links |
| `-store` | `memory` | `memory\|file\|redis\|postgresql\|opensearch` |
| `-store-dir` | `./approval-data` | File store directory |
| `-redis-addr` | `localhost:6379` | Redis address |
| `-postgres-dsn` | — | PostgreSQL DSN |
| `-opensearch-url` | — | OpenSearch URL |
| `-receipt-secret` | — | HS256 receipt secret |
| `-receipt-key` | — | PEM private key for RS256/EdDSA |
| `-receipt-alg` | `HS256` | `HS256\|RS256\|EdDSA` |
| `-oidc-issuer`, `-oidc-client-id`, `-oidc-client-secret`, `-oidc-redirect-url` | — | OIDC browser login |
| `-policy-file` | — | JSON `PolicyConfig` file |
| `-server-credential` | — (repeatable) | `name=token[@instance,…]` or `name=sha256:<hex>` |

`-server-credential` accepts a plaintext token (hashed in memory at startup) or
an already-hashed `sha256:<hex>` value. At least one credential is required.

## Security notes

- Prefer `sha256:<hex>` credentials or a secret manager over plaintext flags;
  command-line arguments can be visible in process listings.
- The GC loop runs every minute and expires overdue records and deletes records
  past retention.
- The HTTP server sets read/write/idle timeouts and shuts down with a 10s grace
  period.
