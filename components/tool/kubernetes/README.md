# Kubernetes Tools

eino tools for interacting with Kubernetes clusters via controller-runtime and
dynamic clients.

## Design

- **Consolidated** — reduced from ~57 separate tool schemas to 9. Uses a
  `kind` parameter resolved via the discovery API, with support for kubectl
  shortnames and CRDs.
- **Deterministic kind resolution** — an optional `apiVersion` (or a
  `resource.group` kind) disambiguates kinds that exist in several API groups;
  a bare ambiguous kind returns an error listing the candidates.
- **Compact dry-run diffs** — patch/apply dry-run returns a unified `diff`
  (3 lines context, 64 KiB cap) alongside the full `wouldPatchTo`/`wouldApplyTo`.
- **Field projection** — `describe` and `list` accept `fields` (dot paths) to
  return only the requested parts of an object.
- **Multi-cluster** — configured via a `Configs` map (`map[string]*ClusterConfig`)
  of named clusters.
- **Curated output** — a formatter registry provides type-specific list output
  for 28 resource types. Unknown types fall back to a generic name/namespace/status
  formatter.
- **Full-content describe** — the `describe` tool returns the complete resource
  JSON (all top-level fields, not just metadata/spec/status/data).
  `excludeFieldsOutput` (`metadata`/`spec`/`status`/`data`) still applies.
  `metadata.managedFields` is always omitted (large, server-managed, and rarely
  useful to an agent).
- **Dynamic CRUD** — create, apply, patch, and delete tools use the dynamic client
  with kind-based resolution.
- **Safety** — write tools enforce a dry-run/confirmed gate internally. Pod exec
  has a destructive command blocklist. Factory functions for combined safety
  middleware configuration are provided.

### Curated views for monitoring.coreos.com alerting CRDs

`list` emits curated, alert-relevant summaries for:

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

## Filtering

`list` accepts a `filter` parameter with two forms, auto-detected from the
trimmed string:

- **JSON object selector** (starts with `{`) — e.g.
  `{"status.conditions[].reason":"NotSupported"}`. All keys must match (AND);
  `[]` matches any array element; an array value means IN; matching is
  type-coerced and case-insensitive (`500` matches `"500"`, `true` matches
  `"true"`); a bare key (no dots) matches any field with that name at any depth.
- **Go RE2 regex** (any other non-empty string) — applied to the raw resource
  JSON. RE2 does not support lookahead/lookbehind/backreferences.

Filtering runs on the **full raw object**, before `fields` projection and
curated formatting, so nested fields such as `status.conditions[].reason` are
matchable even when the curated view omits them. When a filter is present, the
tool iterates all server pages, accumulates only matches, and paginates the
filtered set in-memory with an opaque `paginateToken`; without a filter, the
existing server-side pagination is unchanged. A filtered follow-up page
re-iterates the server pages (bounded by the configured timeout).

Note: because regexes now scan the raw object rather than the curated output, a
regex that relied on a curated-only field name (e.g. `"status":"Running"` for
Pods, whose raw JSON uses `"phase":"Running"`) will no longer match — prefer the
selector for field-value filters.

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
| Read | `kubernetes_list` | List any K8s resource by kind/shortname + GVR fallback, with label selector, filter, `fields` projection, and pagination |
| Read | `kubernetes_describe` | Describe any K8s resource by kind/shortname + name; returns the full resource JSON with `excludeFieldsOutput` and `fields` projection support |
| Read | `kubernetes_cluster_list` | List configured clusters |
| Read | `kubernetes_pod_log` | Get pod logs (invokable + streamable) |
| Write | `kubernetes_pod_exec` | Exec commands in pods (invokable + streamable) |
| Write | `kubernetes_resource_create` | Create a NEW resource from a full manifest |
| Write | `kubernetes_resource_apply` | Apply a FULL manifest (create or replace); use `patch` for small edits |
| Write | `kubernetes_resource_patch` | Preferred tool to change annotations, labels, resources, replicas or any spec field |
| Write | `kubernetes_resource_delete` | Delete resources with cascade options |

The `kind` parameter accepts:

- A Kubernetes Kind e.g. `Pod`, `Deployment`, `ConfigMap`
- A kubectl shortname e.g. `po`, `deploy`, `svc`
- A `resource.group` form e.g. `deployments.apps`

When a kind exists in several API groups (e.g. two CRDs both named `Kafka`),
pass `apiVersion` (e.g. `kafka.strimzi.io/v1beta2`, or `v1` for the core group)
or use the `resource.group` form (e.g. `kafkas.kafka.strimzi.io`). A bare
ambiguous kind returns an error listing the candidate groups and versions
instead of silently picking one. Apply/create resolve the manifest's
`apiVersion` automatically.

Resolution queries the discovery API directly (preferred resources, or the
exact group/version when `apiVersion` is set), so newly installed CRDs are
picked up immediately. All operations are wrapped in `kretry` for transient
API server errors.

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
