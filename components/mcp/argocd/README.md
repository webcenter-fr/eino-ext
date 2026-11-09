# argocd — MCP server for the ArgoCD eino tools

Exposes the [ArgoCD eino tools](../../tool/argocd) as an MCP server (built on
[`libs/mcp`](../../../libs/mcp)): 13 tools (read + write) over stdio or
streamable HTTP, with provider-based auth, RBAC, and elicitation-based human
approval for write tools.

## Tools

Read (read-only):

- `argocd_application_list`, `argocd_application_describe`
- `argocd_certificate_list`
- `argocd_cluster_list`, `argocd_cluster_describe`
- `argocd_instance_list`
- `argocd_project_list`, `argocd_project_describe`
- `argocd_repository_list`, `argocd_repository_describe`

Write (destructive; gated by dry-run/confirmed + human approval):

- `argocd_application_create`
- `argocd_application_delete`
- `argocd_application_sync`

## Usage

```go
srv, err := argocd.NewServer(ctx, argocdtool.Configs{
    "prod": {URL: "https://argocd.example.com", Token: token},
}, &mcpserver.Config{
    Auth:          authCfg,  // optional: local/OIDC bearer-token providers
    Authorization: authzCfg, // optional: RBAC rules
})
if err != nil {
    return err
}
go srv.ServeStdio(ctx) // or srv.ListenAndServeHTTP(ctx, ":8080")
```

- **Instance argument:** `instance` — RBAC rules match it per call.
- **Approval:** write tools require `dryRun=true` first, then `confirmed=true`
  with a human accept via MCP elicitation (see
  [`libs/mcp`](../../../libs/mcp#approval-write-tools)). Without an
  elicitation-capable client, write tools are dry-run only.
