# Grafana Tools

eino tools for interacting with Grafana instances via the HTTP API (v9+).

## Design

- **HTTP API** — uses `net/http` with Bearer token auth (no dedicated Go client library).
- **Multi-instance** — `Configs map[string]Config`, matching the argocd/kubernetes pattern.
- **Dashboard protection** — per-instance blocklist (UID, title prefix, folder, tag) prevents modification of protected dashboards.
- **Safety** — the build tool enforces a dry-run/confirmed gate via `confirm.RequireConfirmation`.

## Configuration

```go
import "github.com/webcenter-fr/eino-ext/components/tool/grafana"

configs := grafana.Configs{
    "prod": {
        URL:   "https://grafana.prod.example.com",
        Token: "glsa_...", // service account token
        ProtectedDashboards: grafana.ProtectedDashboardsConfig{
            UIDs:          []string{"k8s-monitoring", "infra-overview"},
            TitlePrefixes: []string{"Kubernetes ", "Infra: "},
            Tags:          []string{"protected", "infrastructure"},
        },
        DefaultTimeout: "10s",
    },
    "staging": {
        URL:   "https://grafana.staging.example.com",
        Token: "glsa_...",
    },
}
```

## Available Tools

| Tool | Type | Description |
|---|---|---|
| `grafana_instance_list` | Read | List configured Grafana instances |
| `grafana_dashboard_search` | Read | Search dashboards by title, tags, folder, type |
| `grafana_dashboard_describe` | Read | Get full dashboard details by UID |
| `grafana_dashboard_build` | Write | Create or update a dashboard (blocklist-enforced) |

## Factory Functions

```go
// All tools (read + write)
tools, err := grafana.NewAllTools(ctx, configs)

// Read-only tools (no write operations)
readOnlyTools, err := grafana.NewReadOnlyTools(ctx, configs)

// All tools with safety middleware
tools, mw, err := grafana.NewAllToolsWithSafety(ctx, configs, &safety.Config{
    Policy: myCELPolicy,
})

// Write tool names for safety middleware
names := grafana.WriteToolNames() // ["grafana_dashboard_build"]
```

## Dashboard Protection

The dashboard build tool enforces a per-instance blocklist. A dashboard is protected if it matches **any** of the following criteria:

- **UID** — exact match in the UIDs list
- **Title prefix** — title starts with any string in TitlePrefixes
- **Folder** — resides in a folder listed in Folders
- **Tag** — carries a tag listed in Tags

All four criteria are optional. If none are configured, no dashboards are protected.

Example: with the config above, the `build` tool will reject any attempt to modify:
- Dashboard with UID `k8s-monitoring` or `infra-overview`
- Any dashboard whose title starts with "Kubernetes " or "Infra: "
- Any dashboard carrying the tag "protected" or "infrastructure"

## Usage Example

```go
// Search dashboards
search := grafana.NewDashboardSearchTool(ctx, configs) // returns (*DashboardSearchTool, error)
result, _ := search.InvokableRun(ctx, `{
    "instance": "prod",
    "query": "production",
    "type": "dash-db"
}`)

// Build (create/update) a dashboard
build := grafana.NewDashboardBuildTool(ctx, configs) // returns (*DashboardBuildTool, error)
result, _ = build.InvokableRun(ctx, `{
    "instance": "prod",
    "dashboard": "{\"title\": \"My Dashboard\", \"panels\": []}",
    "folderUID": "general",
    "confirmed": true
}`)
```
