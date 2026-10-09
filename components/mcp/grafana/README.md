# grafana — MCP server for the Grafana eino tools

Exposes the [Grafana eino tools](../../tool/grafana) as an MCP server (built on
[`libs/mcp`](../../../libs/mcp)): 6 tools (read + write) over stdio or
streamable HTTP, with provider-based auth, RBAC, and elicitation-based human
approval for write tools.

## Tools

Read (read-only):

- `grafana_instance_list` — list configured instances (no `instance` argument).
- `grafana_dashboard` — search/get dashboards.
- `grafana_datasource` — search/get data sources.
- `grafana_query` — run a data source query.
- `grafana_dashboard_validate` — validate a dashboard JSON.

Write (destructive; gated by dry-run/confirmed + human approval):

- `grafana_dashboard_write`

## Usage

```go
srv, err := grafana.NewServer(ctx, grafanatool.Configs{
    "prod": {URL: "https://grafana.example.com", Token: token},
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
