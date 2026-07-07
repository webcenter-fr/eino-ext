# ArgoCD Tools

eino tools for interacting with ArgoCD servers via their REST API (grpc-gateway) using the `goargocdclient` library.

## Design

- **goargocdclient** — uses the `github.com/disaster37/goargocdclient` library for type-safe ArgoCD API interactions.
- **Authentication** — token or username/password auth via `goargocdclient` options. Tokens can be created externally via `argocd account generate-token`.
- **Multi-instance** — supports a map of named ArgoCD instances, matching the kubernetes tool pattern.
- **TLS** — configurable via `goargocdclient.WithInsecure()` option; secure by default.

## Configuration

```go
import (
    "github.com/webcenter-fr/eino-ext/components/tool/argocd"
    "github.com/disaster37/goargocdclient"
)

configs := argocd.Configs{
    "prod": argocd.Config{
        URL:    "https://argocd.example.com",
        Option: goargocdclient.WithToken("eyJhbGciOiJIUzI1NiIs..."),
    },
    "staging": argocd.Config{
        URL:    "https://argocd-staging.example.com",
        Option: goargocdclient.WithInsecure(),
        // Option: goargocdclient.WithToken("eyJhbGciOiJIUzI1NiIs..."),
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
| `argocd_cluster_list` | List clusters with optional filter |
| `argocd_cluster_describe` | Get cluster details with optional field exclusion |
| `argocd_repository_list` | List repositories with optional filter |
| `argocd_repository_describe` | Get repository details with optional field exclusion |
| `argocd_certificate_list` | List certificates with optional filter |

## Usage Example

```go
ctx := context.Background()

appListTool, err := argocd.NewApplicationListTool(ctx, configs)

result, err := appListTool.InvokableRun(ctx, `{
    "instance": "prod",
    "project": "default"
}`)
```
