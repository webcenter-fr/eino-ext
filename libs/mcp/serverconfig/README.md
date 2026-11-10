# serverconfig — YAML configuration for the deployable MCP server

`libs/mcp/serverconfig` loads the YAML configuration used by
[`cmd/mcp-server`](../../../cmd/mcp-server), resolves file/environment secret
references, and wires the selected tool family into a
[`libs/mcp`](../README.md) server.

## Configuration

```yaml
server: kubernetes          # kubernetes | argocd | prometheus | grafana
transport: http             # stdio | http (default stdio)
addr: ":8080"               # MCP HTTP listen address
opsAddr: ":9090"            # /healthz, /readyz, /metrics (never in a Service)
log:
  level: info               # trace|debug|info|warn|error|fatal|panic
  format: json              # text|json

kubernetes:
  clusters:
    in-cluster:
      inCluster: true
      disallowedNamespaces: [kube-system, kube-public, kube-node-lease]
      defaultTimeout: 30s
    prod:
      kubeconfigFile: /etc/eino-ext/kubeconfig/prod
      context: prod
      defaultTimeout: 30s

# URL/token families (argocd, prometheus, grafana) share the same shape.
argocd:
  instances:
    prod:
      url: https://argocd.example.com
      tokenFile: /etc/eino-ext/secrets/argocd-token
      # tokenEnv: ARGOCD_TOKEN
      tlsSkipVerify: false
      defaultTimeout: 30s

auth:
  providers:
    - type: local
      local:
        tokens:
          - tokenFile: /etc/eino-ext/secrets/alice-token
            user: alice
            groups: [devs]
            scopes: [mcp]
    - type: oidc
      oidc:
        issuer: https://idp.example.com
        audience: mcp-server
  requiredScopes: [mcp]

authz:
  rules:
    - users: [alice]
      operations: [read, write]
      instances: [prod]

approval:
  timeout: 5m
  service:
    url: http://eino-ext-approval:8080
    serverName: kubernetes
    serverTokenFile: /etc/eino-ext/secrets/approval-token
    receiptSecretFile: /etc/eino-ext/secrets/receipt-secret
    receiptAlgorithm: HS256
```

## Secret references

Secrets are **references only**; a literal secret is never accepted from YAML.

- `*File` reads a file, trims trailing `\n`/`\r\n`, rejects an empty value and
  rejects files larger than 64 KiB.
- `*Env` reads an environment variable and rejects an unset or empty value.
- When both are set, `*File` wins. The chosen source is logged at debug level;
  the value is never logged.
- Secrets are resolved **after** viper unmarshalling, so they never enter
  viper's key space or `AllSettings()`.

## Precedence

Flags > environment (`EINO_MCP_*`, `.` replaced by `_`) > YAML file > defaults.
Unknown YAML keys are rejected (`mapstructure` `ErrorUnused`), so a typo in a
security setting fails loudly.

## API

```go
cfg, err := serverconfig.Load("/etc/eino-ext/config.yaml", map[string]any{
    "addr": ":8080",
})
if err != nil {
    return err
}
srv, err := serverconfig.Build(ctx, cfg)
```

`Build` is lazy: it constructs the family tools and the MCP server without
contacting any external service. `OpsHandler(ready)` returns the unauthenticated
operations handler (`/healthz`, `/readyz`, `/metrics`).
