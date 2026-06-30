# ArgoCD Tools

Thin REST client and eino tools for interacting with ArgoCD servers via their REST API (grpc-gateway).

## Design

- **Zero ArgoCD Go dependencies** — uses only `net/http` and `encoding/json` to avoid pulling in ArgoCD's 200+ transitive dependencies and k8s version conflicts.
- **Authentication** — pre-existing JWT token only, created externally via `argocd account generate-token`.
- **Multi-instance** — supports a map of named ArgoCD instances, matching the kubernetes tool pattern.
- **TLS** — configurable `insecure` boolean per instance; default is secure.

## Configuration

```go
import "github.com/webcenter-fr/eino-ext/components/tool/argocd"

configs := argocd.Configs{
    "prod": &argocd.Config{
        ServerURL: "https://argocd.example.com",
        Token:     "eyJhbGciOiJIUzI1NiIs...",
        Insecure:  false,
    },
    "staging": &argocd.Config{
        ServerURL: "https://argocd-staging.example.com",
        Token:     "eyJhbGciOiJIUzI1NiIs...",
        Insecure:  true,
    },
}
```

## Available Tools

| Tool Name | Description |
|---|---|
| `argocd_instance_list` | List all configured ArgoCD instances |
| `argocd_application_list` | List applications with optional project, selector, and filter |
| `argocd_application_describe` | Get application details with optional field exclusion |
| `argocd_application_sync` | Trigger application sync with optional revision, dry-run, prune |
| `argocd_application_create` | Create a new application |
| `argocd_application_delete` | Delete an application with optional cascade |
| `argocd_project_list` | List projects with optional filter |
| `argocd_project_describe` | Get project details with optional field exclusion |

## Usage Example

```go
ctx := context.Background()

appListTool, err := argocd.NewApplicationListTool(ctx, configs)

result, err := appListTool.InvokableRun(ctx, `{
    "instance": "prod",
    "project": "default"
}`)
```
