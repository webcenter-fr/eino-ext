# cmd — standalone approval server binary

Thin `main` for the approval service: parse flags or a YAML config, select a
store backend, build the server, run a GC loop and shut down gracefully on
`SIGINT`/`SIGTERM`.

## Build and run

```bash
go build -o approval ./server/approval/cmd

./approval \
  --addr :8080 \
  --base-url https://approve.example.com \
  --store memory \
  --receipt-secret-file /run/secrets/receipt-secret \
  --server-credential-file /etc/eino-ext/credentials \
  --oidc-issuer https://idp.example.com \
  --oidc-client-id approval \
  --oidc-client-secret-file /run/secrets/oidc-client-secret \
  --oidc-redirect-url https://approve.example.com/auth/callback
```

Both `-flag` and `--flag` are accepted. The repeatable
`-server-credential` syntax is unchanged.

## Flags

| Flag | Default | Purpose |
|---|---|---|
| `--config` | — | YAML configuration file |
| `--addr` | `:8080` | HTTP listen address |
| `--base-url` | derived from `--addr` | Public base URL for approval links |
| `--store` | `memory` | `memory\|file\|redis\|postgresql\|opensearch` |
| `--store-dir` | `./approval-data` | File store directory |
| `--redis-addr` | `localhost:6379` | Redis address |
| `--postgres-dsn` | — | PostgreSQL DSN |
| `--postgres-dsn-file` | — | File containing the PostgreSQL DSN |
| `--opensearch-url` | — | OpenSearch URL |
| `--receipt-secret` | — | HS256 receipt secret |
| `--receipt-secret-file` | — | File containing the HS256 receipt secret |
| `--receipt-key` | — | PEM private key for RS256/EdDSA |
| `--receipt-key-file` | — | File containing the PEM private key |
| `--receipt-alg` | `HS256` | `HS256\|RS256\|EdDSA` |
| `--oidc-issuer`, `--oidc-client-id`, `--oidc-redirect-url` | — | OIDC browser login |
| `--oidc-client-secret` | — | OIDC client secret |
| `--oidc-client-secret-file` | — | File containing the OIDC client secret |
| `--policy-file` | — | JSON `PolicyConfig` file |
| `--server-credential` | — (repeatable) | `name=token[@instance,…]` or `name=sha256:<hex>` |
| `--server-credential-file` | — | One credential per line; blank lines and `#` comments skipped |
| `--log-level` | `info` | `trace\|debug\|info\|warn\|error\|fatal\|panic` |
| `--log-format` | `text` | `text\|json` |

## YAML configuration

```yaml
addr: ":8080"
baseURL: https://approve.example.com
store: file
storeDir: /data
receiptAlg: HS256
receiptSecretFile: /run/secrets/receipt-secret
oidcIssuer: https://idp.example.com
oidcClientID: approval
oidcClientSecretFile: /run/secrets/oidc-client-secret
oidcRedirectURL: https://approve.example.com/auth/callback
policyFile: /etc/eino-ext/policy.json
serverCredentialFile: /etc/eino-ext/credentials
logLevel: info
logFormat: json
```

Unknown keys are rejected. Precedence is flags > environment
(`EINO_APPROVAL_*`) > YAML file > defaults. Secret references are resolved
**after** viper unmarshalling, so secret values never appear in
`viper.AllSettings()`.

Secret precedence is file > environment > literal flag:

- `EINO_APPROVAL_RECEIPT_SECRET`
- `EINO_APPROVAL_OIDC_CLIENT_SECRET`
- `EINO_APPROVAL_POSTGRES_DSN`
- `EINO_APPROVAL_RECEIPT_KEY`

## Health

- `GET /healthz` — liveness.
- `GET /readyz` — readiness; performs a bounded store read and returns 503 when
  the backend is unreachable.

## Security notes

- Prefer `*File` flags or a secret manager over literal flags; command-line
  arguments can be visible in process listings.
- `--server-credential` accepts a plaintext token (hashed in memory at startup)
  or an already-hashed `sha256:<hex>` value. At least one credential is
  required.
- The GC loop runs every minute and expires overdue records and deletes records
  past retention.
- The HTTP server sets read/write/idle timeouts and shuts down with a 10s grace
  period.
