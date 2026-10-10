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
      3. Gate (write tools): safety.ShouldGateWithAuthorization + ElicitationAuthorizer
      4. t.InvokableRun(execCtx, argsJSON)
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
| `approval.go` | `ElicitationAuthorizer` (implements `safety.ExecutionAuthorizer` via `session.Elicit`), `ErrApprovalDenied` |
| `adapter.go` | eino tool → `mcpsdk.Tool` adapter, handler (authz → policy → gate → execute → audit) |
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
    Approval:  &mcpserver.ApprovalConfig{Timeout: "5m"},
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
  fallback `sub`) → `Identity.User`; `GroupsClaim` (default `groups`) →
  `Identity.Groups`; the `scope` claim → the token's scopes.

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

Real execution of a write tool requires a human decision delivered through the
MCP client (**elicitation**). The model-supplied `confirmed=true` argument is
**never** an authorization source — it only triggers the authorizer.

| Channel | Controlled by | Trusted as authorization? |
|---|---|---|
| Tool arguments (incl. `confirmed=true`, `dryRun`) | LLM | **No** — `confirmed=true` only triggers the authorizer |
| Conversation text ("the user already said yes") | LLM | **No** — approval is never read from chat text |
| Bearer token | Verified server-side | Yes — but the LLM cannot forge it |
| Elicitation response (accept/decline) | **Human**, via the MCP client UI | **Yes — the only approval source** |
| `context.Context` grant (`safety.WithExecutionAuthorized`) | Server only | Yes — the model cannot write to ctx |

Approval flow:

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

Fail closed: with no elicitation-capable client, write tools are dry-run only.
The approval prompt shows the complete arguments of the execution call — a
write whose arguments exceed the maximum approvable size (4000 bytes) is
rejected without eliciting, because the human could not review the full
payload that would execute. The approval timeout must be positive (a
non-positive `ApprovalConfig.Timeout` is rejected at construction).
The safety middleware's `AllowModelConfirmation` escape hatch (trusts
model-supplied `confirmed=true`) is **not** wired into the MCP server. Approval
is always elicitation, always human. The authorizer runs on **every**
`confirmed=true` call (no replay); the ctx grant is per-call and per-tool-name.

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
+ identity metadata) via `Config.AuditSink` (default `safety.LogSink`).

## Links

- [`libs/toolkit/safety`](../toolkit/safety) — gate, policy, audit,
  authorization primitives.
- [`libs/toolkit/confirm`](../toolkit/confirm) — per-tool second layer
  (`RequireConfirmationCtx`).
- [`components/mcp`](../../components/mcp) — per-family MCP servers
  (kubernetes, argocd, prometheus, grafana).
- [MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk) — the underlying
  SDK.
