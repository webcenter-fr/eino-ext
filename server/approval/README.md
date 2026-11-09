# approval — standalone out-of-band approval service

`server/approval` is the standalone human-approval service for mutative MCP tool
calls. MCP servers register an approval over REST, a human decides it in a
browser (OIDC login + HttpOnly cookie session), and the MCP server verifies a
signed receipt before executing. The service owns all approval state; the MCP
servers hold none.

It is deliberately separate from the four MCP servers: one URL, one OIDC client,
one place to audit. MCP binaries import only
[`libs/mcp/approval`](../../../libs/mcp/approval), so OIDC, cookie, SPA and store
dependencies never leak into them.

## Architecture

```
MCP server ──REST (per-server bearer token)──▶ approval service
                                                   │ store: memory | file | redis | postgresql | opensearch
human browser ──OIDC + HttpOnly cookie────────────▶│ Nuxt SPA (embedded ui/dist)
```

## Configuration

```go
srv, err := approval.New(ctx, &approval.Config{
    BaseURL: "https://approve.example.com", // required; used for approval links + OIDC redirect
    Store:   store.NewMemory(),             // default: in-memory
    TTL:     "15m",                         // approval record lifetime (default 15m)
    ServerCredentials: []approval.ServerCredential{
        {
            ServerName:       "grafana",
            TokenHash:        approval.HashServerToken("s3cr3t"), // SHA-256 hex, never plaintext
            AllowedInstances: []string{"prod"},
        },
    },
    Receipt: approval.ReceiptConfig{Secret: "receipt-secret"}, // HS256; or PrivateKeyPEM + RS256/EdDSA
    OIDC: &approval.OIDCConfig{
        Issuer:       "https://idp.example.com",
        ClientID:     "approval",
        ClientSecret: "…",
        RedirectURL:  "https://approve.example.com/auth/callback",
    },
    Policy: &approval.PolicyConfig{Rules: []approval.PolicyRule{
        {Name: "four-eyes", Tools: []string{"kubernetes_resource_delete"}, RequireFourEyes: true},
    }},
})
```

Defaults: `ListenAddr=":8080"`, `TTL="15m"`, `SessionTTL="8h"`,
`CookieName="approval_session"`, `CSRFHeader="X-CSRF-Token"`,
`Receipt.Algorithm="HS256"`, `Receipt.TTL="2m"`, self-approve policy, no-op
`Notifier`, structured-log `AuditSink`. `New` applies defaults, validates the
config with `libs/toolkit/validate`, builds the receipt signer and (when OIDC is
configured) fetches the discovery document.

`Server.Handler()` returns the HTTP handler with security headers. The service
is normally run through [`cmd`](./cmd).

## REST contract

All under `/api/v1`. MCP-facing endpoints require the per-server bearer token;
browser-facing endpoints require the cookie session.

| Method | Path | Auth | Purpose |
|---|---|---|---|
| `POST` | `/approvals` | server token | Register an approval; returns `{approvalId, url, expiresAt}` |
| `GET` | `/approvals/{id}/decision?wait=30s` | server token | Long-poll; returns `{state}` and, when approved, `{receipt}` |
| `GET` | `/approvals` | cookie | Inbox of pending records this approver may decide |
| `GET` | `/approvals/{id}` | cookie | Detail: args + preview + requester |
| `POST` | `/approvals/{id}/decide` | cookie + CSRF | Body `{decision: approve\|reject}` |
| `GET` | `/auth/login`, `/auth/callback`, `/auth/logout` | — | OIDC auth-code + PKCE |
| `GET` | `/healthz` | — | Liveness |
| `GET` | `/*` | — | SPA shell from the embedded `ui/dist` |

Rules:

- A register call whose asserted `serverName` does not match the credential's
  bound `ServerName` → **403**; likewise for an instance outside
  `AllowedInstances`.
- The service recomputes `argsHash` from the submitted `args` and rejects a
  mismatch with the client-supplied hash → **400**.
- Long-poll holds at most 30s, then returns `pending` (clients re-issue).

## Receipt

Claims: `approvalId`, `serverName`, `toolName`, `instance`, `requesterSub`,
`approverSub`, `argsHash`, `decision`, `policy`, `iat`, `exp` (2m), `jti`.
Signed HS256 from the per-server shared secret by default, or RS256/EdDSA with a
configured key pair. Receipts are reusable within `exp`; every use is audited
with the `jti`.

## Security notes

- **Cookie:** `HttpOnly`, `Secure`, `SameSite=Lax`, host-only, random 256-bit
  id, server-side session record, rotation on login, explicit logout.
- **CSRF:** double-submit token on `POST /decide` and logout; mismatch → 403.
- **CSP:** strict `default-src 'self'`, no `unsafe-inline`/`unsafe-eval`, nonce
  for the SPA bootstrap; plus `X-Content-Type-Options`, `Referrer-Policy`,
  `X-Frame-Options: DENY`.
- **Anti-phishing:** the default self-approve policy only lets the requester
  decide, so a different user opening another user's approval URL gets 403.
  `approvalId` is an opaque random lookup key carrying no authority. The
  comparison is `session.Sub` (the browser's OIDC `sub`) against the record's
  `requesterSub`; the MCP server must therefore send its OIDC `sub` as
  `requester.sub` (its `Identity.Sub`, default `SubClaim: "sub"`), not its
  display name. If the two identity systems use different claims, self-approve
  fails closed — align the MCP `SubClaim` with this service's OIDC `sub`.
- **Server tokens:** stored as SHA-256 hex digests and compared in constant time.
- **Fail closed:** no session, unreachable service, bad signature, hash
  mismatch, expiry, decline or policy denial → deny + audit.
- **No token passthrough:** the end user's MCP bearer token is never forwarded.

## Tests

`server_test.go` covers server-name/instance binding, hash mismatch, the
register→approve→receipt flow, CSRF, anti-phishing, four-eyes and group policy,
inbox filtering, expiry, security headers and SPA fallback. Run with
`make test`.
