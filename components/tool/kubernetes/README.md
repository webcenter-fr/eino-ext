# Kubernetes Tools

eino tools for interacting with Kubernetes clusters via controller-runtime and
dynamic clients.

## Design

- **Consolidated** — reduced from ~57 separate tool schemas to 9. Uses a
  `kind` parameter resolved via a cached RESTMapper that supports kubectl
  shortnames and CRDs.
- **Multi-cluster** — configured via a `Configs` map (`map[string]*ClusterConfig`)
  of named clusters.
- **Curated output** — a formatter registry provides type-specific list output
  for 28 resource types. Unknown types fall back to a generic name/namespace/status
  formatter.
- **Curated describe views** — the `describe` tool emits a curated,
  ALERT-relevant summary for the prometheus-operator CRDs (`Alertmanager`,
  `AlertmanagerConfig`, `PrometheusRule`, `Silence` from
  `monitoring.coreos.com`). Other kinds fall back to the raw
  `metadata/spec/status/data` dump. `excludeFieldsOutput` still applies to
  the curated sections (`metadata`/`spec`/`status`).
- **Dynamic CRUD** — create, apply, patch, and delete tools use the dynamic client
  with kind-based resolution.
- **Safety** — write tools enforce a dry-run/confirmed gate internally. Pod exec
  has a destructive command blocklist. Factory functions for combined safety
  middleware configuration are provided.

### Curated views for monitoring.coreos.com alerting CRDs

`list` and `describe` emit curated, alert-relevant summaries for:

- `Alertmanager` (`v1`) — replicas, version, paused, and derived status
  (`Paused`/`Available`/`Degraded`).
- `AlertmanagerConfig` (`v1alpha1`) — route receiver, receiver names and
  config-type tags (e.g. `slack`, `webhook`), and route tree.
- `PrometheusRule` (`v1`) — group/rule counts, alert names, and severities
  (hoisted from `labels.severity`).
- `Silence` (`v1alpha1`) — state, matchers, `startsAt`/`endsAt`, `createdBy`.

`AlertmanagerConfig` receivers reference Kubernetes Secrets by name only (e.g.
`slackConfigs[].apiURL` as a `secretKeyRef`); the CRD never holds secret data,
so no redaction is needed.

## Configuration

```go
import (
    "k8s.io/client-go/rest"

    "github.com/webcenter-fr/eino-ext/components/tool/kubernetes"
)

configs := kubernetes.Configs{
    "prod": &kubernetes.ClusterConfig{
        Config: &rest.Config{Host: "https://prod.example.com", ...},
    },
}
```

## Available Tools

| Category | Tool Name | Description |
|---|---|---|
| Read | `kubernetes_list` | List any K8s resource by kind/shortname + GVR fallback, with label selector, filter, and pagination |
| Read | `kubernetes_describe` | Describe any K8s resource by kind/shortname + name, with field exclusion; curated views for monitoring.coreos.com alerting CRDs |
| Read | `kubernetes_cluster_list` | List configured clusters |
| Read | `kubernetes_pod_log` | Get pod logs (invokable + streamable) |
| Write | `kubernetes_pod_exec` | Exec commands in pods (invokable + streamable) |
| Write | `kubernetes_resource_create` | Create resources via dynamic client |
| Write | `kubernetes_resource_apply` | Server-side apply via dynamic client |
| Write | `kubernetes_resource_patch` | Patch resources with type selection |
| Write | `kubernetes_resource_delete` | Delete resources with cascade options |

The `kind` parameter accepts:

- A Kubernetes Kind e.g. `Pod`, `Deployment`, `ConfigMap`
- A kubectl shortname e.g. `po`, `deploy`, `svc`
- A `resource.group` form e.g. `deployments.apps`

Resolution uses a cached RESTMapper (backed by discovery cache) that resets
on cache misses to pick up newly installed CRDs. All operations are wrapped
in `kretry` for transient API server errors.

## Factory Functions

```go
// All tools
tools, err := kubernetes.NewAllTools(ctx, configs, scheme)

// Read-only tools
readTools, err := kubernetes.NewReadOnlyTools(ctx, configs, scheme)

// All tools with pre-configured safety middleware
allTools, mw, err := kubernetes.NewAllToolsWithSafety(ctx, configs, scheme, safetyCfg)

// Write tool names for external safety middleware
writeNames := kubernetes.WriteToolNames()
```

## Security

Pod exec has a destructive command blocklist covering `rm`, `kill`, `dd`,
`mkfs`, `chroot`, `iptables`, and similar commands. Write tools require a
dry-run step before execution. Blocklisted kinds (ClusterRole, Namespace,
NetworkPolicy, etc.) cannot be created, applied, patched, or deleted.
