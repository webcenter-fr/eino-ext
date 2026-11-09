# kubernetes — MCP server for the Kubernetes eino tools

Exposes the [Kubernetes eino tools](../../tool/kubernetes) as an MCP server
(built on [`libs/mcp`](../../../libs/mcp)): 9 tools (read + write) over stdio or
streamable HTTP, with provider-based auth, RBAC, and elicitation-based human
approval for write tools.

## Tools

Read (read-only):

- `kubernetes_cluster_list` — list configured clusters (no `cluster` argument).
- `kubernetes_list` — list resources.
- `kubernetes_describe` — describe a resource.
- `kubernetes_pod_logs` — pod logs.

Write (destructive; gated by dry-run/confirmed + human approval):

- `kubernetes_pod_exec`
- `kubernetes_resource_create`
- `kubernetes_resource_patch`
- `kubernetes_resource_delete`
- `kubernetes_resource_apply`

## Usage

```go
srv, err := kubernetes.NewServer(ctx, k8stool.Configs{
    "prod": {Config: prodRestConfig},
}, runtime.NewScheme(), &mcpserver.Config{
    Auth:          authCfg,  // optional: local/OIDC bearer-token providers
    Authorization: authzCfg, // optional: RBAC rules
})
if err != nil {
    return err
}
go srv.ServeStdio(ctx) // or srv.ListenAndServeHTTP(ctx, ":8080")
```

- **Instance argument:** `cluster` — RBAC rules match it per call.
- **Approval:** write tools require `dryRun=true` first, then `confirmed=true`
  with a human accept via MCP elicitation (see
  [`libs/mcp`](../../../libs/mcp#approval-write-tools)). Without an
  elicitation-capable client, write tools are dry-run only.
