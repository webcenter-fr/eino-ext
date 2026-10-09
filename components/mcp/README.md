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
