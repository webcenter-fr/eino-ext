# mcp — MCP servers per tool family

Project-specific extension: one MCP server per local eino tool family, built on
[`libs/mcp`](../../libs/mcp). Each package exposes the family's tools (read +
write) over MCP (stdio or streamable HTTP) with provider-based authentication,
RBAC, and elicitation-based human approval for write tools.

## Packages

| Package | Tool family | Instance argument | Write tools |
|---|---|---|---|
| [`kubernetes`](./kubernetes) | [`components/tool/kubernetes`](../tool/kubernetes) | `cluster` | 5 (`kubernetes_pod_exec`, `kubernetes_resource_create`, `kubernetes_resource_patch`, `kubernetes_resource_delete`, `kubernetes_resource_apply`) |
| [`argocd`](./argocd) | [`components/tool/argocd`](../tool/argocd) | `instance` | 3 (`argocd_application_create`, `argocd_application_delete`, `argocd_application_sync`) |
| [`prometheus`](./prometheus) | [`components/tool/prometheus`](../tool/prometheus) | `instance` | 0 (read-only) |
| [`grafana`](./grafana) | [`components/tool/grafana`](../tool/grafana) | `instance` | 1 (`grafana_dashboard_write`) |

## Deployable binary

For production, use [`cmd/mcp-server`](../../cmd/mcp-server) instead of writing
a `main` per family. It selects the family with `--server`, loads a YAML config
through [`libs/mcp/serverconfig`](../../libs/mcp/serverconfig), and serves stdio
or streamable HTTP with an unauthenticated operations port (`/healthz`,
`/readyz`, `/metrics`). The [`helm/eino-ext`](../../helm/eino-ext) chart deploys
one MCP Deployment per enabled family.

## Running a server

```go
// Kubernetes example; the other families have the same shape.
srv, err := kubernetes.NewServer(ctx, k8stool.Configs{
    "prod": {Config: prodRestConfig},
}, runtime.NewScheme(), &mcpserver.Config{
    Auth:          authCfg,  // optional: local/OIDC bearer-token providers
    Authorization: authzCfg, // optional: RBAC rules
})
if err != nil {
    return err
}

// stdio (trusted local process)
go srv.ServeStdio(ctx)

// or streamable HTTP (bearer-token auth when Auth is configured)
go srv.ListenAndServeHTTP(ctx, ":8080")
```

See [`libs/mcp`](../../libs/mcp) for the full configuration reference (auth
providers, RBAC rules, approval, audit) and the approval flow.

## Approval

By default, write tools require human approval through MCP **elicitation** in
the client UI. For hard, out-of-band approval, point every server at the
standalone [approval service](../../server/approval) via
`Config.Approval.Service`:

```go
Approval: &mcpserver.ApprovalConfig{
    Timeout: "5m",
    Service: &mcpserver.ApprovalServiceConfig{
        URL:              "https://approve.example.com",
        ServerName:       "kubernetes", // must match the service credential binding
        ServerToken:      "s3cr3t",
        ReceiptSecret:    "receipt-secret",
        ReceiptAlgorithm: "HS256",
    },
},
```

With the service configured, the default authorizer becomes
`ApprovalURLAuthorizer`: the human approves in a browser, the MCP server
long-polls the decision and verifies a signed receipt bound to the canonical
`argsHash` before executing. Clients without URL-mode elicitation get a tool
error carrying the approval URL and `approvalId`; the LLM re-calls the same
write tool with `approvalId` (the adapter strips it before `InvokableRun`).

The service path **fails closed**: an unreachable service, bad receipt, hash
mismatch, decline, expiry or policy denial denies execution and never downgrades
to elicitation. There is no argument-size rejection limit on either path; the
elicitation prompt discloses truncation (`showing N of M bytes — remainder NOT
displayed`) and shows the full `argsHash`.
