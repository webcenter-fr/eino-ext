# Kubernetes Tools Consolidation Plan

## Goal

Reduce from ~57 separate tool schemas to ≈7. This shrinks the model-context
footprint of tool descriptions on every turn and improves tool-selection accuracy,
while keeping the curated per-type list output that keeps tool responses small.

Resource identity moves from "one tool per kind" to a `kind` parameter resolved
via a cached RESTMapper. Write tools adopt the same `kind` resolution.

## Tool surface (after)

Read: `kubernetes_list`, `kubernetes_describe`, `kubernetes_cluster_list`,
`kubernetes_pod_log` — **4 instead of ~51**.

Write: `kubernetes_resource_create`, `kubernetes_resource_apply`,
`kubernetes_resource_patch`, `kubernetes_resource_delete`,
`kubernetes_pod_exec` — **5 (same count, changed params)**.

---

## 1. EXACT FORMER TOOL INVENTORY

This is the complete set of dedicated list tools (24) whose ToJSON logic must be
migrated into the formatter registry. Each has a unique Kind, GVK, Go type,
cluster-scope status, and import path.

### 1A. Core resources (in `k8s.io/client-go/kubernetes/scheme` = default scheme)

| # | file                    | Kind                    | Go type                         | apiVersion                        | Scope       |
|---|-------------------------|------------------------|---------------------------------|-----------------------------------|-------------|
| 1 | pod_list.go             | Pod                    | \*corev1.Pod                    | v1                                | namespaced  |
| 2 | deployment_list.go      | Deployment             | \*appsv1.Deployment             | apps/v1                           | namespaced  |
| 3 | statefulset_list.go     | StatefulSet            | \*appsv1.StatefulSet            | apps/v1                           | namespaced  |
| 4 | daemonset_list.go       | DaemonSet              | \*appsv1.DaemonSet              | apps/v1                           | namespaced  |
| 5 | configmap_list.go       | ConfigMap              | \*corev1.ConfigMap              | v1                                | namespaced  |
| 6 | secret_list.go          | Secret                 | \*corev1.Secret                 | v1                                | namespaced  |
| 7 | service_list.go         | Service                | \*corev1.Service                | v1                                | namespaced  |
| 8 | ingress_list.go         | Ingress                | \*networkingv1.Ingress          | networking.k8s.io/v1              | namespaced  |
| 9 | pvc_list.go             | PersistentVolumeClaim  | \*corev1.PersistentVolumeClaim  | v1                                | namespaced  |
|10 | node_list.go            | Node                   | \*corev1.Node                   | v1                                | **cluster** |
|11 | namespace_list.go       | Namespace              | \*corev1.Namespace              | v1                                | **cluster** |
|12 | event_list.go           | Event                  | \*corev1.Event                  | v1                                | namespaced  |
|13 | service_account_list.go | ServiceAccount         | \*corev1.ServiceAccount         | v1                                | namespaced  |
|14 | storageclass_list.go    | StorageClass           | \*storagev1.StorageClass        | storage.k8s.io/v1                 | **cluster** |
|15 | crd_list.go             | CustomResourceDefinition | \*apiextensionsv1.CustomResourceDefinition | apiextensions.k8s.io/v1 | **cluster** |

### 1B. CRD resources (require explicit AddToScheme)

| # | file                     | Kind                    | Go type                        | GV (for RESTMapper)         | Scope       | AddToScheme call              |
|---|-------------------------|-------------------------|-------------------------------|-----------------------------|-------------|-------------------------------|
|16 | kafka_cluster_list.go   | Kafka                   | \*strimzi.Kafka               | kafka.strimzi.io/v1beta2    | namespaced  | strimzi.AddToScheme(s)        |
|17 | kafka_topic_list.go     | KafkaTopic              | \*strimzi.KafkaTopic          | kafka.strimzi.io/v1beta2    | namespaced  | strimzi.AddToScheme(s)        |
|18 | kafka_node_pool_list.go | KafkaNodePool           | \*strimzi.KafkaNodePool       | kafka.strimzi.io/v1beta2    | namespaced  | strimzi.AddToScheme(s)        |
|19 | kafka_user_list.go      | KafkaUser               | \*strimzi.KafkaUser           | kafka.strimzi.io/v1beta2    | namespaced  | strimzi.AddToScheme(s)        |
|20 | olm_csv_list.go         | ClusterServiceVersion   | \*olmv1alpha1.ClusterServiceVersion | operators.coreos.com/v1alpha1 | namespaced  | olmv1alpha1.AddToScheme(s)    |
|21 | olm_subscription_list.go| Subscription            | \*olmv1alpha1.Subscription    | operators.coreos.com/v1alpha1 | namespaced  | olmv1alpha1.AddToScheme(s)    |
|22 | olm_install_plan_list.go| InstallPlan             | \*olmv1alpha1.InstallPlan     | operators.coreos.com/v1alpha1 | namespaced  | olmv1alpha1.AddToScheme(s)    |
|23 | ocp_route_list.go       | Route                   | \*routev1.Route               | route.openshift.io/v1       | namespaced  | routev1.AddToScheme(s)        |
|24 | spark_application_list.go| SparkApplication       | \*spark.SparkApplication      | sparkoperator.k8s.io/v1beta2| namespaced  | spark.AddToScheme(s)          |

### 1C. Describe tools

All 25 describe tools (**including CRD describes**) delegate to the generic
`DescribeTool[resource]` from `generic_describe.go`. They contain zero per-type
logic beyond passing the Go type. The describe implementation is already unified
and remains so — no per-type formatters needed.

### 1D. Files that exist

List files (will be DELETED):
`pod_list.go`, `deployment_list.go`, `statefulset_list.go`, `daemonset_list.go`,
`configmap_list.go`, `secret_list.go`, `service_list.go`, `ingress_list.go`,
`pvc_list.go`, `node_list.go`, `namespace_list.go`, `event_list.go`,
`service_account_list.go`, `storageclass_list.go`, `crd_list.go`,
`kafka_cluster_list.go`, `kafka_topic_list.go`, `kafka_node_pool_list.go`,
`kafka_user_list.go`, `olm_csv_list.go`, `olm_subscription_list.go`,
`olm_install_plan_list.go`, `ocp_route_list.go`, `spark_application_list.go`.

Describe files (will be DELETED — all 25):
`pod_describe.go`, `deployment_describe.go`, `statefulset_describe.go`,
`daemonset_describe.go`, `configmap_describe.go`, `secret_describe.go`,
`service_describe.go`, `ingress_describe.go`, `pvc_describe.go`,
`node_describe.go`, `namespace_describe.go`, `event_describe.go`,
`service_account_describe.go`, `storageclass_describe.go`,
`crd_describe.go`, `kafka_cluster_describe.go`, `kafka_topic_describe.go`,
`kafka_node_pool_describe.go`, `kafka_user_describe.go`,
`olm_csv_describe.go`, `olm_subscription_describe.go`,
`olm_install_plan_describe.go`, `ocp_route_describe.go`,
`spark_application_describe.go`.

Generic engine files (DELETED): `generic_list.go`, `generic_describe.go`.

Resource files (DELETED): `resource_list.go`, `resource_describe.go`.

**Files that stay (unchanged except for possibly adapting test helpers):**
`base.go`, `client.go`, `config.go`, `cluster_list.go`, `pod_log.go`,
`pod_exec.go`, `resource_create.go`, `resource_apply.go`,
`resource_patch.go`, `resource_delete.go`, `validate_manifest.go`,
`validate_manifest_test.go`, `helper.go`, `prompts/list_output_guidance.md`,
`prompts/describe_output_guidance.md`, `suite_test.go`.

**New files created**: `list.go`, `describe.go`, `formatters.go`,
`resolver.go`, `kretry.go` (in libs/toolkit/kretry).

**Files ADAPTED**: `base.go` (add mapper field), `registry.go` (replace
constructors), `check.go` (new component names), `check_test.go` (count
assertions), `README.md` (new tool table), `configmap_test.go` (use new tool
constructors), `resource_create.go`/`resource_apply.go`/`resource_patch.go`/
`resource_delete.go` (GVR triple → kind param, use resolver+retry).

---

## 2. NEW COMPONENTS (ordered by dependency)

### 2A. `libs/toolkit/kretry` — shared retry helper

File: `libs/toolkit/kretry/kretry.go` + `libs/toolkit/kretry/kretry_test.go`

```go
package kretry

import (
    "context"
    "net"
    "time"
    "k8s.io/apimachinery/pkg/api/errors"
    "k8s.io/client-go/util/retry"
)

// DefaultBackoff is the default retry backoff: 2s, 4s, 8s (capped at 15s).
var DefaultBackoff = wait.Backoff{
    Steps:    3,
    Duration: 2 * time.Second,
    Factor:   2.0,
    Cap:      15 * time.Second,
}

// Do retries fn on transient errors with the given backoff.
// Returns fn's error if all attempts fail or ctx is cancelled.
func Do(ctx context.Context, backoff wait.Backoff, fn func(context.Context) error) error

// IsTransient returns true if err is a retryable API-server / network error.
func IsTransient(err error) bool

// DefaultBackoff wrapped around Do:
func Retry(ctx context.Context, fn func(context.Context) error) error
```

IsTransient classifier — retry these and ONLY these:
- `errors.IsServerTimeout(err)`  — 504
- `errors.IsTooManyRequests(err)` — 429
- `errors.IsInternalError(err)` — 500
- `errors.IsServiceUnavailable(err)` — 503
- `errors.IsTimeout(err)` — client-side timeout
- net.Error timeout / temporary
- context.DeadlineExceeded? NO — that means the caller's overall deadline hit.

Tests: table-driven; mock errors for each transient type; verify retried and
succeeds on 3rd attempt; verify permanent error (NotFound, Forbidden) returns
immediately; verify ctx cancellation stops retries.

### 2B. Resolver — cached discovery RESTMapper

File: `components/tool/kubernetes/resolver.go` (no separate test file; tested via
list/describe integration tests).

```go
package kubernetes

import (
    "context"
    "sync"
    "k8s.io/apimachinery/pkg/api/meta"
    "k8s.io/apimachinery/pkg/runtime/schema"
    "k8s.io/client-go/discovery"
    "k8s.io/client-go/discovery/cached/memory"
    "k8s.io/client-go/rest"
    "k8s.io/client-go/restmapper"
)

// cachedMapper wraps a per-cluster cached discovery RESTMapper.
type cachedMapper struct {
    restMapper meta.RESTMapper
    mu         sync.Mutex    // guards Reset
}

type resolveResult struct {
    GVR      schema.GroupVersionResource
    GVK      schema.GroupVersionKind
    Scoped   bool   // true = namespaced, false = cluster-scoped
}

// newCachedMapper builds a memcache-backed DeferredDiscoveryRESTMapper
// wrapped in ShortcutExpander.
func newCachedMapper(config *rest.Config) (*cachedMapper, error) {
    dc, err := discovery.NewDiscoveryClientForConfig(config)
    if err != nil { return nil, err }
    mem := memory.NewMemCacheClient(dc)
    restMapper := restmapper.NewDeferredDiscoveryRESTMapper(mem)
    // ShortcutExpander enables kubectl-style shortnames (po, deploy, etc.)
    expander := restmapper.NewShortcutExpander(restMapper, mem, func(err error) bool { return false })
    return &cachedMapper{restMapper: expander}, nil
}

// Resolve resolves a kind/user input to GVR, GVK, and scope.
// On meta.NoResourceMatchError it calls Reset() once and retries (picks up
// newly installed CRDs). The entire resolve is wrapped in kretry for
// transient errors.
func (cm *cachedMapper) Resolve(ctx context.Context, kind string) (resolveResult, error)

// Reset invalidates the memcache so the next Resolve re-fetches discovery.
func (cm *cachedMapper) Reset() {
    cm.mu.Lock()
    defer cm.mu.Unlock()
    cm.restMapper.Reset()
}
```

**How Resolve works**:
1. Accept `kind`. The ShortcutExpander already handles shortnames like `po`,
   `deploy`, `svc` (built-in) and CRD-declared shortNames.
2. First try `RESTMapping(schema.GroupKind{Group: "", Kind: kind})`. If the
   kind is a shortname or includes a group (e.g. `deployments.apps`), the
   ShortcutExpander's aliases already handle it; if not, fall through.
3. On `meta.NoResourceMatchError`: call `cm.Reset()`, then retry once.
4. Wrap the entire resolve in `kretry.Retry(ctx, fn)`.
5. From the returned `meta.RESTMapping` extract:
   - `mapping.Resource` = GVR
   - `mapping.GroupVersionKind` = GVK (only valid after client-go v0.26+;
     GVK may be empty in older versions — construct from RESTMapping's
     GroupVersion+Kind as fallback)
   - `mapping.Scope.Name()` == `meta.RESTScopeNameNamespace` → `Scoped: true`
6. Return the resolveResult.

**Storage**: Add `mappers map[string]*cachedMapper` to `baseTool`.
Build them in `newBaseTool` by iterating over `configs`:

```go
func newBaseTool(ctx context.Context, configs Configs) (*baseTool, error) {
    // existing BuildClients call ...
    mappers := make(map[string]*cachedMapper, len(configs))
    for name, cc := range configs {
        m, err := newCachedMapper(cc.Config)
        if err != nil { return nil, err }
        mappers[name] = m
    }
    return &baseTool{
        clients:    clients,
        mappers:    mappers,      // NEW
        configs:    configs,
        knownClusters: configs.GetClusterNames(),
        disallowedNamespaces: buildDisallowedNamespaces(configs),
    }, nil
}
```

The `baseToolWithDynamic` embeds `*baseTool` so mappers are accessible to both
read tools and write tools transparently.

### 2C. Combined scheme + formatter registry

File: `components/tool/kubernetes/formatters.go`

Purpose: one `*runtime.Scheme` registering ALL types that have curated list
formatters, plus a GVK-keyed map of formatter functions.

```go
package kubernetes

import (
    "context"
    "github.com/goccy/go-json"
    "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
    "k8s.io/apimachinery/pkg/runtime"
    "k8s.io/apimachinery/pkg/runtime/schema"
    utilruntime "k8s.io/apimachinery/pkg/util/runtime"
    "k8s.io/client-go/kubernetes/scheme"

    appsv1 "k8s.io/api/apps/v1"
    corev1 "k8s.io/api/core/v1"
    networkingv1 "k8s.io/api/networking/v1"
    storagev1 "k8s.io/api/storage/v1"
    apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
    strimzi "github.com/RedHatInsights/strimzi-client-go/apis/kafka.strimzi.io/v1beta2"
    olmv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
    routev1 "github.com/openshift/api/route/v1"
    spark "github.com/kubeflow/spark-operator/api/v1beta2"
)

// combinedScheme registers all resource types that have curated list formatters.
// Used by the formatter registry to convert unstructured -> typed objects.
var combinedScheme = func() *runtime.Scheme {
    s := scheme.Scheme  // start with the default scheme (has core types)
    utilruntime.Must(appsv1.AddToScheme(s))
    utilruntime.Must(networkingv1.AddToScheme(s))
    utilruntime.Must(storagev1.AddToScheme(s))
    utilruntime.Must(apiextensionsv1.AddToScheme(s))
    utilruntime.Must(strimzi.AddToScheme(s))
    utilruntime.Must(olmv1alpha1.AddToScheme(s))
    utilruntime.Must(routev1.AddToScheme(s))
    utilruntime.Must(spark.AddToScheme(s))
    return s
}()
```

**Formatter type**:

```go
// listFormatter takes a typed K8s object (converted from unstructured via the
// combinedScheme) and returns its curated JSON representation.
type listFormatter func(runtime.Object) json.RawMessage
```

**Registry**:

```go
// formatterRegistry maps (apiVersion, kind) → listFormatter.
// Keyed on apiVersion+kind (i.e. schema.GroupVersionKind) for exact match,
// falling back to GroupKind (version-agnostic) if no exact match is found.
var formatterRegistry = initFormatterRegistry()

func initFormatterRegistry() map[schema.GroupVersionKind]listFormatter {
    reg := make(map[schema.GroupVersionKind]listFormatter)
    // For each type, construct a formatter that:
    //   1. Casts the runtime.Object to the concrete type
    //   2. Runs the exact same field extraction as the deleted ToJSON body
    //   3. Returns the marshal.MustMarshal'd output
    reg[schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"}] =
        func(o runtime.Object) json.RawMessage {
            pod := o.(*corev1.Pod)
            // copy the body of PodListOutput.ToJSON verbatim here
            output := &PodListOutput{...} // keep exported types or make unexported
            ...
            return marshal.MustMarshal(output)
        }
    // ... repeat for all 24 types ...
    return reg
}
```

**Exported vs unexported output types**: The `XxxListOutput` structs (e.g.
`PodListOutput`, `DeploymentListOutput`) are currently exported (capital letter)
and referenced in `configmap_test.go`. They must remain exported for back compat,
OR be made unexported with `configmap_test.go` updated. This plan makes them
**unexported** (lowercase first letter is fine since they're migration-internal
and the test is rewritten — see step 2H). However, the test currently asserts
against `ConfigMapListOutput{...}` literal structs. Since the test is rewritten,
the structs can be unexported. **Decision**: unexport the output structs
(`podListOutput`, `deploymentListOutput`, etc.) to signal they are internal.

**Default/formatter fallback**: When no formatter is registered for a GVK, the
`listFormatter` used is the generic name/namespace/status extraction from the
current `ResourceListOutput.ToJSON`. This must include Secret redaction:

```go
func defaultListFormatter(o runtime.Object) json.RawMessage {
    // ... generic name/namespace/status, redact data for Secrets ...
}
```

**Conversion helper**: The list tool will call this after fetching from the
dynamic client:

```go
type unstructuredList = *unstructured.UnstructuredList
type unstructuredObj = *unstructured.Unstructured

// formatListItem converts an unstructured item to a typed object, then
// runs it through the formatter registry.
func (f *formatterHelper) Format(o unstructuredObj) json.RawMessage {
    // 1. Try to convert to a typed object via combinedScheme
    typed, err := combinedScheme.ConvertToVersion(o, ...) // or use
        // runtime.DefaultUnstructuredConverter.FromUnstructured(o.Object, target)
    // 2. If conversion succeeds and a formatter exists for the GVK → use it
    // 3. If conversion fails or no formatter → use defaultListFormatter
}
```

**IMPORTANT**: `FromUnstructured` requires a destination object, and we don't
know the destination type without a lookup. Better approach: store a separate
`typeConstructor map[GVK]func() runtime.Object` alongside the formatters.

```go
type formatterEntry struct {
    newObj    func() runtime.Object
    format    listFormatter
}
var registry map[schema.GroupVersionKind]formatterEntry
```

The list tool:
1. Gets the GVK from the unstructured item (via `obj.GetObjectKind()` or
   parsing `apiVersion`/`kind` fields).
2. Looks up `registry[gvk]`.
3. If found: `dst := entry.newObj(); err := runtime.DefaultUnstructuredConverter.FromUnstructured(o.Object, dst)`;
   then returns `entry.format(dst)`.
4. If not found: returns the generic default.

### 2D. `kubernetes_list` tool

File: `components/tool/kubernetes/list.go`

```go
type ListParams struct {
    Cluster        string              `json:"cluster" validate:"required" ...`
    Kind           string              `json:"kind" validate:"required" ...`
    // Namespace is optional. For cluster-scoped kinds (Node, Namespace, CRD,
    // StorageClass), the tool omits it from the dynamic client call.
    Namespace      string              `json:"namespace,omitempty" ...`
    LabelsSelector string              `json:"labelsSelector,omitempty" ...`
    Filter         string              `json:"filter,omitempty" ...`
    Paginate       *ListParamsPaginate `json:"paginate,omitempty" ...`
}

type ListTool struct {
    *baseTool
    tool.InvokableTool
}
```

The Invoke function:
1. Validate params.
2. Call `t.mappers[params.Cluster].Resolve(ctx, params.Kind)` to get
   GVR + scoped + GVK (wrapped in kretry).
3. If `!scoped`, ignore `params.Namespace` even if provided.
4. Dynamic-client list with pagination (same as current `ResourceListTool`).
5. For each item: call formatListItem (from 2C) to get JSON.
6. Filter via regex (same as current `generic_list.go`'s `IsMatch`).
7. Append continue token if paginated.
8. Append `listOutputGuidance` to the tool description via `InferTool`.
9. Wrap the entire List call in `kretry.Retry(ctx, fn)`.

**Constructor**:
```go
func NewListTool(ctx context.Context, configs Configs) (tool.InvokableTool, error) {
    base, err := newBaseTool(ctx, configs)
    if err != nil { return nil, err }
    t := &ListTool{baseTool: base}
    inv, err := utils.InferTool("kubernetes_list",
        fmt.Sprintf("%s\n%s", listDescription, listOutputGuidance),
        t.Invoke)
    if err != nil { return nil, err }
    t.InvokableTool = inv
    return t, nil
}
```

Tool description `listDescription` documents the `kind` parameter: "A
Kubernetes resource Kind (e.g. 'Pod', 'Deployment', 'ConfigMap'), a kubectl
shortname ('po', 'deploy'), or a 'resource.group' form ('deployments.apps').
Uses server-side discovery so CRDs are supported automatically."

### 2E. `kubernetes_describe` tool

File: `components/tool/kubernetes/describe.go`

```go
type DescribeParams struct {
    Cluster             string   `json:"cluster" validate:"required" ...`
    Kind                string   `json:"kind" validate:"required" ...`
    Name                string   `json:"name" validate:"required" ...`
    Namespace           string   `json:"namespace,omitempty" ...`
    ExcludeFieldsOutput []string `json:"excludeFieldsOutput,omitempty" ...`
}

type DescribeTool struct {
    *baseTool
    tool.InvokableTool
}
```

Invoke:
1. Validate.
2. Resolve kind → GVR + scoped via mapper (kretry wrapped).
3. Dynamic-client Get (kretry wrapped).
4. Reuse `objectToDescribeOutput` (moved from generic_describe.go into
   `formatters.go`) which already works on `client.Object` and uses
   reflection to extract spec/status/data fields. For unstructured objects,
   directly read `o.Object["spec"]`, `o.Object["status"]`, `o.Object["data"]`.
5. Secret redaction: if GVK.Kind == "Secret", redact `data` and `stringData`.
6. Apply `excludeFieldsOutput`.
7. Append `describeOutputGuidance` to tool description.
8. Return JSON.

**Constructor**:
```go
func NewDescribeTool(ctx context.Context, configs Configs) (tool.InvokableTool, error) {
    base, err := newBaseTool(ctx, configs)
    ...
    inv, err := utils.InferTool("kubernetes_describe", ...)
}
```

### 2F. Refactored write tools

For each of `resource_create.go`, `resource_apply.go`, `resource_patch.go`,
`resource_delete.go`:

**Param struct change**: Delete `ApiGroup string`, `ApiVersion string`,
`Resource string`. Add `Kind string` (required). Keep everything else
(`Cluster`, `Namespace`, `Name`, `Manifest`, etc.) unchanged.

**Invoke change**:
1. Call `t.mappers[params.Cluster].Resolve(ctx, params.Kind)` (kretry wrapped)
   → get GVR + GVK + scoped.
2. Run blocklist checks against the **resolved** GVK/GVR:
   - `blocklistedKinds` (used in create.go line 95, apply.go line 90):
     check `resolvedGVK.Kind`, not the user input string.
   - `blocklistedResources` (used in delete.go line 117, patch.go line 88):
     check `resolvedGVR.Group` and `resolvedGVR.Resource`, not the user input.
3. Patch: also add the `blocklistedKinds` check (currently patch.go line 88
   only checks `blocklistedResources` but not `blocklistedKinds` — this is a
   gap, fix it).
4. For create/apply: the manifest already contains `apiVersion`/`kind`. After
   changing to `kind`-based resolution, the resolved GVK should match the
   manifest's GVK. If they mismatch, return an error.
5. Wrap the dynamic client call in `kretry.Retry(ctx, fn)`.
6. Keep `confirm.RequireConfirmation`, dry-run, namespace checks, ownership,
   `validateManifestSecurity` unchanged.

**Constructor change**: Switch from `newBaseToolWithDynamic` to
`newBaseTool(ctx, configs)` (which now includes mappers). The dynamics map
is still needed — add `dynamics map[string]dynamic.Interface` to `baseTool`
and build them in `newBaseTool`, OR keep `baseToolWithDynamic` embedded. The
cleanest: move `dynamics` from `baseToolWithDynamic` into `baseTool` and
build them in `newBaseTool`, then delete `baseToolWithDynamic`. All tools
that need dynamics already have access to `baseTool`. But this is a broad
change. **Simpler**: keep `baseToolWithDynamic` and add mappers to it too,
or better: add mappers to `baseTool` and have `baseToolWithDynamic` access
them via its embedded `*baseTool`. Since write tools already use
`baseToolWithDynamic`, they get mappers automatically.

### 2G. `registry.go` update

Replace the long `readOnlyConstructors` slice with:

```go
var readOnlyConstructors = []toolConstructor{
    func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewClusterListTool(ctx, c) },
    func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewListTool(ctx, c) },
    func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewDescribeTool(ctx, c) },
    func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewPodLogTool(ctx, c) },
}
```

Write constructors stay the same (same function names, changed param structs
internally).

Update `var _ tool.InvokableTool` checks to reference the new types:

```go
var (
    _ tool.InvokableTool = (*ClusterListTool)(nil)
    _ tool.InvokableTool = (*ListTool)(nil)
    _ tool.InvokableTool = (*DescribeTool)(nil)
    _ tool.InvokableTool = (*PodLogTool)(nil)
    _ tool.InvokableTool = (*PodExecTool)(nil)
    _ tool.InvokableTool = (*ResourceCreateTool)(nil)
    _ tool.InvokableTool = (*ResourceApplyTool)(nil)
    _ tool.InvokableTool = (*ResourceDeleteTool)(nil)
    _ tool.InvokableTool = (*ResourcePatchTool)(nil)
    _ tool.StreamableTool = (*PodExecTool)(nil)
    _ tool.StreamableTool = (*PodLogTool)(nil)
)
```

`WriteToolNames()` stays unchanged (same 5 tool name strings).

### 2H. `configmap_test.go` adaptation

Delete `configmap_test.go`. Its test logic (the `TestConfigMap` suite method)
tests dedicated tools `NewConfigMapListTool` and `NewConfigMapDescribeTool`
which no longer exist.

**Replace** with a new corresponding test in the suite test that tests the
consolidated `NewListTool` and `NewDescribeTool` with `kind: "configmaps"`:

```go
func (t *ToolTestSuite) TestConsolidatedListAndDescribe() {
    // Create configmap fixtures (same as initConfigMapTest)
    // Use NewListTool + NewDescribeTool with kind: "configmaps"
    // Test: list all, label selector, filter, get, excludeFieldsOutput,
    //       cluster-scoped (nodes), unknown kind, invalid cluster error
}
```

This is a single test covering both list and describe via the unified tools,
replacing the old per-resource test. It is written directly in
`components/tool/kubernetes/suite_test.go` (or a new `tool_test.go`).

### 2I. `check.go` update

Change `coreResources()` → `coreKinds()`: returns just the Kind strings for
the probe.

```go
func coreKinds() []string {
    return []string{"pods", "configmaps", "nodes", "namespaces"} // representative sample
}
```

`probeCluster`: instead of iterating over `kubeResourceDescriptor` and calling
c.List with typed objects, use the dynamic client + resolver:

```go
func probeCluster(ctx context.Context, c client.Client, cfg *rest.Config,
    cluster string) checkup.Results {
    var results checkup.Results
    results = append(results, checkup.Result{
        Component: "kubernetes_cluster_list", Instance: cluster, Status: checkup.StatusOK,
    })
    // Build a dynamic client for probing
    dc, _ := dynamic.NewForConfig(cfg)
    // Build a cachedMapper for resolution
    mapper, _ := newCachedMapper(cfg)
    for _, kind := range coreKinds() {
        r := probeKind(ctx, dc, mapper, cluster, kind)
        results = append(results, r...)
    }
    // Limited for CRD kinds (not installable in checkup)
    results = append(results, checkup.Result{
        Component: "kubernetes_list", Instance: cluster, Status: checkup.StatusLimited,
        Message: "CRD-only kinds (kafka, olm, ocp, spark) tested with dedicated env",
    }...)
    // etc.
}
```

Replace `allComponentNames()` to return the new shorter list:

```go
func allComponentNames() []string {
    names := []string{
        "kubernetes_cluster_list",
        "kubernetes_list",
        "kubernetes_describe",
        "kubernetes_pod_log",
        "kubernetes_resource_create",
        "kubernetes_resource_apply",
        "kubernetes_resource_patch",
        "kubernetes_resource_delete",
    }
    return names
}
```

`limitedResults` function is removed. `clientErrorResults` generates error
results for all new component names.

Remove `probeCoreResource`, `listItems`, `listItem`, `listItemAdapter`,
`itemNameAndNamespace`, `coreResources` — all unused.

### 2J. `check_test.go` update

`TestAllComponentNames`: change count assertion from `> 40` to `== 8`.
TestClientErrorResults: change count assertion from `> 40` to `== 8`.

### 2K. `README.md` update

Rewrite the tool table:

```
| Category | Tool Name | Description |
|---|---|---|
| Read | `kubernetes_list` | List any K8s resource by kind/shortname + GVR fallback, with label selector, filter, and pagination |
| Read | `kubernetes_describe` | Describe any K8s resource by kind/shortname + name, with field exclusion |
| Read | `kubernetes_cluster_list` | List configured clusters |
| Read | `kubernetes_pod_log` | Get pod logs (invokable + streamable) |
| Write | `kubernetes_pod_exec` | Exec commands in pods (invokable + streamable) |
| Write | `kubernetes_resource_create` | Create resources via dynamic client |
| Write | `kubernetes_resource_apply` | Server-side apply via dynamic client |
| Write | `kubernetes_resource_patch` | Patch resources with type selection |
| Write | `kubernetes_resource_delete` | Delete resources with cascade options |
```

Document: `kind` param accepts Kind, shortname, or `resource.group`; cached
RESTMapper with reset-on-miss; kretry on transient errors; formatter registry
for curated list output.

### 2L. Deleted file list (for go vet / build to pass)

After deleting the 24 list files, 25 describe files, `generic_list.go`,
`generic_describe.go`, `resource_list.go`, `resource_describe.go`, and
`configmap_test.go`, the following exported APIs are removed:

- `NewPodListTool`, `NewDeploymentListTool`, ... (all NewXxxListTool)
- `NewPodDescribeTool`, ... (all NewXxxDescribeTool)
- `PodListOutput`, `DeploymentListOutput`, ... (all XxxListOutput types)
- `DescribeTool[resource]` (generic struct)
- `ListTool[resourceList, resource, outputObject]` (generic struct)
- `DescribeParams` (old struct — replaced by new DescribeParams)
- `ListParams` (old struct — changed)
- `DescribeOutput` (moved to internal)
- `OutputObject[resource]` (interface)
- `ResourceListTool`, `ResourceListParams`, `ResourceListOutput`
- `ResourceDescribeTool`, `ResourceDescribeParams`
- `GetItems`, `CloneObject`, `GetObjectStatus`, `GetObjectSpec`, `GetDataSpec`,
  `SetObjectTypeMeta` (kept as internal helpers in formatters.go)

Exported APIs kept:
- `NewClusterListTool`, `ClusterListTool`, `ClusterListParams`
- `NewPodLogTool`, `PodLogTool`
- `NewPodExecTool`, `PodExecTool`
- `NewResourceCreateTool`, etc. (write tool constructors — keep, but param
  structs change shape)
- `Configs`, `ClusterConfig`, `GetConfig`, `GetClusterNames`
- `BuildClients`, `BuildClientsFromKubeconfig`, `BuildClientSets`,
  `BuildClientDynamics`
- `NewClient`, `NewClientSet`, `NewClientDynamic`
- `Check`
- `NewAllTools`, `NewReadOnlyTools`, `NewAllToolsWithSafety`,
  `WriteToolNames`, `ExtractWriteToolNames`
- `NewListTool`, `NewDescribeTool` (new signatures)

### 2M. `helper.go` preservation

Keep `helper.go` as-is. It embeds `listOutputGuidance` and
`describeOutputGuidance` from the prompts directory. Both guidance strings are
still used in the new list/describe tool descriptions.

### 2N. Prompts preservation

`prompts/list_output_guidance.md` and `prompts/describe_output_guidance.md` are
unchanged. They are still embedded via `helper.go` and appended to the new
tool descriptions.

---

## 3. EXECUTION ORDER & DEPENDENCIES

1. **Create** `libs/toolkit/kretry/kretry.go` + `kretry_test.go`.
   - No dependency on rest of the project. Can be done first and tested.
2. **Create** `formatters.go` (combined scheme + formatter registry).
   - Depends on existing imports (all are already in go.mod). Can be created
     before list.go, but will not compile until `base.go` is updated. Best to
     write it but add to build target only when all files exist.
3. **Adapt** `base.go`: add `mappers map[string]*cachedMapper` to `baseTool`,
   build in `newBaseTool`. Add the `dynamic.Interface` clients to `baseTool`
   (or keep on `baseToolWithDynamic`). Also add `resolver.go`.
   - Depends on `kretry`. Also adds `cachedMapper` to baseTool.
4. **Create** `list.go`, `describe.go`, `resolver.go`.
   - Depends on `formatters.go`, adapted `base.go`, `kretry`.
5. **Adapt** write tool files: `resource_create.go`, `resource_apply.go`,
   `resource_patch.go`, `resource_delete.go`.
   - Depends on `base.go` having mappers, `kretry`.
6. **Delete** the 51 dedicated tool files + 2 generic files + 2 resource files.
7. **Update** `registry.go`, `check.go`, `check_test.go`, `README.md`.
8. **Adapt/delete** `configmap_test.go`; update `suite_test.go` with a
   consolidated test.
9. **Run** `go build ./...`, `go vet ./...`, `go test ./...`.

---

## 4. VALIDATION CHECKLIST

- [ ] `go build ./...` passes (no missing symbols from deleted files).
- [ ] `go vet ./...` passes.
- [ ] `go test ./...` passes (suite_test + check_test + kretry_test + validate_manifest_test).
- [ ] `NewAllTools` returns 9 tools.
- [ ] `NewReadOnlyTools` returns 4 tools.
- [ ] `WriteToolNames()` returns the 5 unchanged write names.
- [ ] `NewListTool` with `kind: "pods"` lists pods from the envtest cluster.
- [ ] `NewListTool` with `kind: "nodes"` (cluster-scoped) ignores namespace.
- [ ] `NewListTool` with `kind: "deploy"` (shortname) resolves successfully.
- [ ] `NewListTool` with `kind: "UnknownKindXyz"` returns a clear error.
- [ ] `NewDescribeTool` with `kind: "secret"` redacts data values.
- [ ] `NewDescribeTool` with `excludeFieldsOutput: ["spec", "status"]` works.
- [ ] `blocklistedKinds` blocks create/apply of `ClusterRole` via shortname `clusterrole`.
- [ ] `blocklistedResources` blocks delete/patch of `namespaces` via shortname `ns`.
- [ ] Transient error on list/get → retried up to `kretry.DefaultBackoff.Steps`.

---

## 5. RISKS / CAVEATS

- **Version drift**: RESTMapper returns the preferred/served version, which may
  differ from today's pinned versions (strimzi v1beta2). Formatter registry
  keys on GVK; register all served versions of each type that the cluster
  may serve, or key on GroupKind (version-agnostic) with a list of registered
  versions tried in order.
- **GVK on unstructured items**: The dynamic client does NOT always set GVK
  on returned items (it sets apiVersion+kind in the JSON map, but
  `obj.GetObjectKind().GroupVersionKind()` may return empty). Extract from
  `obj.GetAPIVersion()` and `obj.GetKind()` and construct the GVK manually.
- **Strimzi types use pointer fields**: e.g. `o.Status.Replicas` is `*int32`,
  `con.Type` is `*string`. The formatters must handle nil pointers (existing
  code uses `ptr.To()` helpers; these must stay).
- **OLM types**: `olmv1alpha1` has both `operators.coreos.com/v1alpha1` and
  `operators.coreos.com/v1` packages. The existing OLM tools only use
  `operators/v1alpha1`. Register only that.
- **ShortcutExpander import path**: As of client-go v0.36,
  `restmapper.NewShortcutExpander` expects 3 args:
  `(restMapper meta.RESTMapper, discoveryClient discovery.CachedDiscoveryInterface, fallbackFunc func(error) bool)`.
  With `memCacheClient` (type `*memory.CachedDiscoveryClient`) which
  implements `CachedDiscoveryInterface`.
  If the signature differs in this project's client-go version, use
  `restmapper.NewShortcutExpander(mem)` (2-arg version from older client-go).
- **go.mod**: All required imports are already in the project's go.mod. No new
  dependencies. Only `libs/toolkit/kretry` needs `k8s.io/client-go` and
  `k8s.io/apimachinery` which are already transitive deps.

---

## 6. OUT OF SCOPE

- Merging write tools into fewer operations (e.g. single apply/delete).
- Changing `pod_exec` or `pod_log` behavior.
- Safety middleware contract beyond updated `WriteToolNames`.
- Adding new K8s resource support (e.g. HPAs, VPA) — the formatter registry
  can be extended later.
- Periodic cache refresh for the RESTMapper.
