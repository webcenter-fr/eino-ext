# Kubernetes Tools

eino tools for interacting with Kubernetes clusters via controller-runtime and
typed/dynamic clients.

## Design

- **Multi-cluster** — configured via a `Configs` map (`map[string]*rest.Config`)
  of named clusters, matching the argocd tool pattern.
- **Generic list/describe** — uses Go generics and reflection for type-agnostic
  list and describe tools over typed K8s objects.
- **Dynamic CRUD** — create, apply, patch, and delete tools use the dynamic
  client for generic resource operations.
- **Safety** — write tools enforce a dry-run/confirmed gate internally. Pod exec
  has a destructive command blocklist. Factory functions for combined safety
  middleware configuration are provided.

## Configuration

```go
import (
    "k8s.io/client-go/rest"
    "k8s.io/apimachinery/pkg/runtime"

    "github.com/webcenter-fr/eino-ext/components/tool/kubernetes"
)

configs := kubernetes.Configs{
    "prod": &rest.Config{Host: "https://prod.example.com", ...},
    "staging": &rest.Config{Host: "https://staging.example.com", ...},
}
scheme := runtime.NewScheme()
// register types on scheme...
```

## Available Tools

| Category | Tool Name | Description |
|---|---|---|
| Read | `kubernetes_pod_list` | List pods with labels selector and filter |
| Read | `kubernetes_pod_describe` | Describe pod details |
| Read | `kubernetes_deployment_list` | List deployments |
| Read | `kubernetes_deployment_describe` | Describe deployment details |
| Read | `kubernetes_statefulset_list` | List statefulsets |
| Read | `kubernetes_statefulset_describe` | Describe statefulset details |
| Read | `kubernetes_daemonset_list` | List daemonsets |
| Read | `kubernetes_daemonset_describe` | Describe daemonset details |
| Read | `kubernetes_configmap_list` | List configmaps |
| Read | `kubernetes_configmap_describe` | Describe configmap details |
| Read | `kubernetes_secret_list` | List secrets (values redacted) |
| Read | `kubernetes_secret_describe` | Describe secret details (values redacted) |
| Read | `kubernetes_service_list` | List services |
| Read | `kubernetes_service_describe` | Describe service details |
| Read | `kubernetes_ingress_list` | List ingresses |
| Read | `kubernetes_ingress_describe` | Describe ingress details |
| Read | `kubernetes_pvc_list` | List persistent volume claims |
| Read | `kubernetes_pvc_describe` | Describe PVC details |
| Read | `kubernetes_node_list` | List nodes |
| Read | `kubernetes_node_describe` | Describe node details |
| Read | `kubernetes_namespace_list` | List namespaces |
| Read | `kubernetes_namespace_describe` | Describe namespace details |
| Read | `kubernetes_event_list` | List events |
| Read | `kubernetes_serviceaccount_list` | List service accounts |
| Read | `kubernetes_storageclass_list` | List storage classes |
| Read | `kubernetes_crd_list` | List custom resource definitions |
| Read | `kubernetes_pod_log` | Get pod logs (invokable + streamable) |
| Write | `kubernetes_pod_exec` | Exec commands in pods (invokable + streamable) |
| Write | `kubernetes_resource_create` | Create resources via dynamic client |
| Write | `kubernetes_resource_apply` | Server-side apply via dynamic client |
| Write | `kubernetes_resource_patch` | Patch resources (strategic/merge/json) |
| Write | `kubernetes_resource_delete` | Delete resources with cascade options |

Additional tools for Kafka, OLM, OpenShift, and Spark are available.

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
dry-run step before execution.
