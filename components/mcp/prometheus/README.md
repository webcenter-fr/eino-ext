# prometheus — MCP server for the Prometheus eino tools

Exposes the [Prometheus eino tools](../../tool/prometheus) as an MCP server
(built on [`libs/mcp`](../../../libs/mcp)): 3 read-only tools over stdio or
streamable HTTP, with provider-based auth and RBAC. Prometheus has no write
tools, so the approval gate is a no-op but the wiring is uniform.

## Tools

All read-only:

- `prometheus_instance_list` — list configured instances (no `instance`
  argument).
- `prometheus_metric` — run an instant PromQL query.
- `prometheus_target_list` — list scrape targets.

## Usage

```go
srv, err := prometheus.NewServer(ctx, promtool.Configs{
    "prod": {Address: "http://prometheus.example.com:9090"},
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
- **Approval:** no write tools; every tool is read-only.
