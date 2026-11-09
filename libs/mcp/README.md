# mcp — Expose eino tools as an MCP server

`libs/mcp` exposes [eino](https://github.com/cloudwego/eino) tools as an
[MCP](https://modelcontextprotocol.io) server, consumable from any MCP client
(e.g. opencode) over stdio or streamable HTTP. It adds provider-based
authentication (local static tokens + OIDC), granular RBAC (user/group →
operations + instances + tools), and elicitation-based human approval for write
tools, reusing the shared safety machinery from
[`libs/toolkit/safety`](../toolkit/safety).

## Architecture

```
MCP client (opencode)
  → transport (stdio | streamable HTTP + Authorization: Bearer …)
  → auth.RequireBearerToken (TokenVerifier: local | oidc)   [HTTP only]
  → identity (auth.TokenInfo → Identity{User, Groups})
  → per-identity *mcp.Server (ServerFor, cached; tools/list filtered by category)
  → tool handler:
      1. RBAC: Authorize(identity, toolName, instance, isWrite)
      2. CEL policy: Policy.Evaluate(ctx, toolName, params)
      3. Gate (write tools): safety.ShouldGateWithAuthorization +
         ElicitationAuthorizer (default) or ApprovalURLAuthorizer (when
         Config.Approval.Service is set)
      4. t.InvokableRun(execCtx, argsJSON)  [approvalId stripped first]
      5. audit(phase, result, err) + identity metadata
```

| File | Contents |
|---|---|
| `config.go` | `Config`, `AuthConfig`, `ProviderConfig`, `LocalProviderConfig`, `LocalToken`, `OIDCProviderConfig`, `AuthzConfig`, `AccessRule`, `ApprovalConfig` |
| `identity.go` | `Identity{User, Groups}`, token-info mapping, per-identity cache key, audit metadata |
| `authz.go` | `Authorizer`, `Authorize`, `CanUse`, `ErrAccessDenied`, rule matching |
| `auth.go` | `Provider` interface, `tokenVerifier` adapter (multi-provider chain), `newProviders` |
| `auth_local.go` | `LocalProvider` (constant-time token compare) |
| `auth_oidc.go` | `OIDCProvider` (go-oidc/v3 JWT verification, claims → identity) |
| `approval.go` | `ElicitationAuthorizer` (implements `safety.ExecutionAuthorizer` via `session.Elicit`), `ApprovalDisplayBudget`, `ErrApprovalDenied` |
| `approval_url.go` | `ApprovalURLAuthorizer` (out-of-band browser approval via `libs/mcp/approval`), `PreviewFunc` |
| `approval/` | lightweight approval-service client: canonical `argsHash`, receipt verify, REST register + long-poll |
| `adapter.go` | eino tool → `mcpsdk.Tool` adapter, handler (authz → policy → gate → execute → audit), `approvalId` inject/strip |
| `server.go` | `Server`, `NewServer`, `ServerFor` (per-identity cache) |
| `transport.go` | `ServeStdio`, `HTTPHandler`, `ListenAndServeHTTP` |

## Quick start

```go
srv, err := mcpserver.NewServer(ctx, &mcpserver.Config{
    Name:          "my-tools",
    Version:       "1.0.0",
    Tools:         tools, // []tool.InvokableTool
    WriteTools:    k8stool.WriteToolNames(),
    InstanceParam: "cluster", // RBAC instance argument
})
if err != nil {
    return err
}

// stdio (trusted local process; identity = Config.LocalIdentity)
go srv.ServeStdio(ctx)

// or streamable HTTP (bearer-token auth when Config.Auth is set)
go srv.ListenAndServeHTTP(ctx, ":8080")
```

Per-family ready-made servers live in [`components/mcp`](../../components/mcp).

## Configuration

Full example (local + OIDC providers, RBAC rules, approval timeout):

```go
cfg := &mcpserver.Config{
    Name:          "kubernetes",
    Version:       "1.0.0",
    Tools:         tools,
    WriteTools:    k8stool.WriteToolNames(),
    InstanceParam: "cluster",
    Auth: &mcpserver.AuthConfig{
        Providers: []mcpserver.ProviderConfig{
            {Type: "local", Local: &mcpserver.LocalProviderConfig{
                Tokens: []mcpserver.LocalToken{
                    {Token: "s3cr3t", User: "alice", Groups: []string{"devs"}, Scopes: []string{"mcp"}},
                },
            }},
            {Type: "oidc", OIDC: &mcpserver.OIDCProviderConfig{
                Issuer:   "https://idp.example.com",
                Audience: "mcp-server",
                // UserClaim: "preferred_username" (default; falls back to "sub")
                // SubClaim: "sub" (default; the stable subject used for approval
                //           binding — must match the approval service's OIDC sub)
                // GroupsClaim: "groups" (default)
            }},
        },
        ResourceMetadataURL: "https://mcp.example.com/.well-known/oauth-protected-resource",
        RequiredScopes:      []string{"mcp"},
    },
    Authorization: &mcpserver.AuthzConfig{
        Rules: []mcpserver.AccessRule{
            {Users: []string{"alice"}, Operations: []string{"read", "write"}, Instances: []string{"prod"}},
            {Groups: []string{"devs"}, Operations: []string{"read"}},
        },
    },
    Approval: &mcpserver.ApprovalConfig{
        Timeout: "5m",
        // Optional: out-of-band browser approval. When set, the default
        // authorizer becomes ApprovalURLAuthorizer; when nil, elicitation
        // stays the default.
        Service: &mcpserver.ApprovalServiceConfig{
            URL:                  "https://approve.example.com",
            ServerName:           "kubernetes",
            ServerToken:          "s3cr3t",
            ReceiptSecret:        "receipt-secret", // or ReceiptPrivateKeyPEM
            ReceiptAlgorithm:     "HS256",
            PollInterval:         "250ms",
            RequestTimeout:       "35s",
            TTL:                  "15m",
        },
    },
    AuditSink: mySink,   // optional; defaults to safety.LogSink
    Policy:    myPolicy, // optional; evaluated on every call
}
```

### Authentication

The HTTP transport is wrapped with the go-sdk's `auth.RequireBearerToken`
middleware. Providers implement the SDK's `auth.TokenVerifier` and are tried in
order; first success wins. All failures → 401 + `WWW-Authenticate` (RFC 9728
`resource_metadata` when configured).

- **local** — static bearer tokens mapped to identities. Verification iterates
  all tokens, comparing SHA-256 digests with `crypto/subtle.ConstantTimeCompare`
  (fixed-length digests → constant time regardless of token length; all tokens
  are always compared → no timing leak of which tokens exist).
- **oidc** — verifies OIDC-issued JWT access tokens with
  `github.com/coreos/go-oidc/v3` (signature via JWKS, issuer, audience,
  expiry). Claims map to identity: `UserClaim` (default `preferred_username`,
  fallback `sub`) → `Identity.User`; `SubClaim` (default `sub`) →
  `Identity.Sub`; `GroupsClaim` (default `groups`) → `Identity.Groups`; the
  `scope` claim → the token's scopes.

`Identity.Sub` is the **stable subject** used for approval/anti-phishing
binding. When the approval service is configured, `SubClaim` must resolve to the
same value as the approval service's OIDC `sub` (its `Session.Sub`), otherwise
the default self-approve policy will deny the requester's own approvals
(fail-closed). The default `SubClaim: "sub"` already matches; only change it if
both sides map the same claim.

`AuthConfig.RequiredScopes` is enforced by the SDK's `RequireBearerToken`
middleware: a request whose token does not carry all the required scopes is
rejected (403) before any handler runs. Scopes come from `LocalToken.Scopes`
(local provider) or the token's `scope` claim (OIDC provider).

For **stdio** (trusted local process) there is no bearer token: the identity is
`Config.LocalIdentity` (still subject to RBAC).

### Authorization (RBAC)

Rules map users/groups to operations, instances, and tools. Within a rule, the
`Users` and `Groups` conditions are AND-ed; across rules, the union of matching
rules grants access. **Fail closed: no matching rule → deny.**

```go
type AccessRule struct {
    Users      []string // match Identity.User (exact); empty = any
    Groups     []string // match any of Identity.Groups; empty = any
    Operations []string // "read", "write"; empty = all
    Instances  []string // cluster/instance names; empty or "*" = all
    Tools      []string // exact names or "prefix_*" glob; empty = all
}
```

Enforcement at two points:

- **`tools/list`** — a per-identity `*mcp.Server` (built via `ServerFor`, cached
  by identity key) registers only the tools whose category the identity may use.
- **`tools/call`** — the handler extracts the instance argument
  (`Config.InstanceParam`: `cluster` for kubernetes, `instance` for the others)
  and checks category + instance + tool pattern. Tools without an instance
  argument (e.g. `kubernetes_cluster_list`) are checked at category level only.

### Approval (write tools)

Real execution of a write tool requires a human decision. The model-supplied
`confirmed=true` argument is **never** an authorization source — it only
triggers the authorizer. Two authorizers are available:

- **`ElicitationAuthorizer`** (default) — the human decides in the MCP client UI
  via `session.Elicit(...)`.
- **`ApprovalURLAuthorizer`** — opt-in via `Config.Approval.Service`; the human
  decides out-of-band in a browser, independent of the agent process, the LLM
  and the MCP client UI.

| Channel | Controlled by | Trusted as authorization? |
|---|---|---|
| Tool arguments (incl. `confirmed=true`, `dryRun`, `approvalId`) | LLM | **No** — `confirmed=true` only triggers the authorizer; `approvalId` is only a lookup key |
| Conversation text ("the user already said yes") | LLM | **No** — approval is never read from chat text |
| Bearer token | Verified server-side | Yes — but the LLM cannot forge it |
| Elicitation response (accept/decline) | **Human**, via the MCP client UI | **Yes** (elicitation path) |
| Signed approval receipt | **Human**, via the approval service | **Yes** (service path) |
| `context.Context` grant (`safety.WithExecutionAuthorized`) | Server only | Yes — the model cannot write to ctx |

#### Elicitation path (default)

1. LLM calls a write tool with `dryRun=true` → gate allows → tool returns the
   preview → handler appends `safety.DryRunGuidance`. Audited `PhaseDryRun`.
2. LLM re-calls with `confirmed=true` → `safety.ShouldGateWithAuthorization`
   requires `ExecutionAuthorizer.AuthorizeExecute` → the `ElicitationAuthorizer`
   calls `session.Elicit(...)` → the client renders an approval prompt to the
   human showing the tool name, the target instance, the user, and the **actual
   arguments of this execution call**.
3. Human accepts → gate marks `safety.WithExecutionAuthorized(ctx, toolName)` →
   the tool's `confirm.RequireConfirmationCtx` sees the grant → executes.
   Audited `PhaseExecute`.
4. Human declines / cancels / timeout / client lacks elicitation → gate rejects →
   tool error result, nothing executes. Audited `PhaseRejected`.

**Disclosed truncation.** There is no argument-size rejection limit. The prompt
renders up to `ApprovalDisplayBudget` (4000) bytes; when the payload is larger
it states `showing N of M bytes — remainder NOT displayed` and includes the
full `SHA-256 of full arguments` (`argsHash`). This keeps consent informed and
auditable without blocking legitimate large payloads (e.g. a 6 KB Grafana
dashboard or a Deployment carrying
`kubectl.kubernetes.io/last-applied-configuration`). The audit event records
`argsHash` and `argsTruncated`.

#### Service path (opt-in)

When `Config.Approval.Service` is set, the default authorizer becomes
`ApprovalURLAuthorizer`:

1. If the call carries `approvalId` (published as an optional property in the
   write tool's MCP input schema and **stripped before `InvokableRun`**), fetch
   the decision, verify the signed receipt, done.
2. Otherwise run the tool's own `dryRun=true` preview (best-effort; a failure is
   surfaced to the approver as a warning, **never** an auto-approval), compute
   `argsHash`, and register the approval with the service.
3. If the client advertises URL-mode elicitation, call
   `session.Elicit(mode:"url", url, elicitationId)`; otherwise return a tool
   error carrying the URL and `approvalId` so the LLM can re-call the **same**
   write tool with `approvalId`.
4. Long-poll the decision until the MCP timeout, then verify the receipt:
   signature → `exp`/`iat` → `decision == approve` → server/tool/instance match
   → recomputed `argsHash` matches the args about to execute.

**Fail closed.** An unreachable service, a 5xx, a bad signature, a hash
mismatch, a decline, an expiry or a policy denial all deny execution. The
service path **never** downgrades to elicitation. The approval timeout must be
positive (a non-positive `ApprovalConfig.Timeout` is rejected at construction).

The safety middleware's `AllowModelConfirmation` escape hatch (trusts
model-supplied `confirmed=true`) is **not** wired into the MCP server. Approval
is always human. The authorizer runs on **every** `confirmed=true` call (no
replay); the ctx grant is per-call and per-tool-name.

A custom `safety.ExecutionAuthorizer` can be injected via
`Config.ExecutionAuthorizer` (it MUST derive the decision from server-side
state, never from tool arguments).

### Transports

- **stdio** — `Server.ServeStdio(ctx)`: single session, local identity.
- **HTTP (streamable)** — `Server.HTTPHandler()` /
  `Server.ListenAndServeHTTP(ctx, addr)`: stateful mode only (stateless rejects
  server→client requests, which would break elicitation-based approval).

### Audit

Every tool call emits a `safety.AuditEvent` (phase read/dry-run/execute/rejected
+ identity metadata) via `Config.AuditSink` (default `safety.LogSink`). Write
calls also record `argsHash` and `argsTruncated`; the service path emits
separate `approval-requested` / `approval-granted` / `approval-denied` /
`approval-expired` events carrying `approvalId`, `approverSub`, `jti`,
`decision` and `policy`.

## Links

- [`libs/toolkit/safety`](../toolkit/safety) — gate, policy, audit,
  authorization primitives.
- [`libs/toolkit/confirm`](../toolkit/confirm) — per-tool second layer
  (`RequireConfirmationCtx`).
- [`components/mcp`](../../components/mcp) — per-family MCP servers
  (kubernetes, argocd, prometheus, grafana).
- [MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk) — the underlying
  SDK.
