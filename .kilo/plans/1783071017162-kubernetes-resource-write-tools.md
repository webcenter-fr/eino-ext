# Kubernetes Resource Write Tools + Parameter Cleanup

## Goal

1. Add 4 generic write tools to `components/tool/kubernetes/` that can create, patch, delete, and apply **any** Kubernetes resource (core + CRDs) using the dynamic client.
2. Rename existing `resource_list.go` and `resource_describe.go` parameters to use cleaner names.
3. Add secret redaction to `resource_describe.go` (currently missing — only `generic_describe.go` has it).
4. Keep per-type list/describe tools as-is (they provide curated output and are not replaced by generic tools).

## Design Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Tool granularity | One tool per operation (not per resource type) | LLMs work better with focused, well-described tools |
| Client type | `dynamic.Interface` | Works with any resource type including CRDs |
| Manifest input | Raw JSON string field | Most flexible for the LLM to generate any resource |
| DryRun strategy | Kubernetes server-side dry-run | Catches server-side admission controllers |
| Patch strategies | Strategic merge + JSON merge + JSON patch | Covers all common use cases |
| GVR parameter names | Cleaner names: `apiGroup`, `apiVersion`, `resource` | More accurate than existing `resourceGroup/resourceVersion/resourceKind` |
| Safety | DryRun/Confirmed gate on all tools | Consistent with existing safety middleware |
| Per-type list/describe | **Keep them** | Curated output (pod images, deployment replicas), secret redaction, better LLM tool selection |
| Write tools on secrets/nodes | **Allowed** | Dynamic client works on any resource; safety gate + RBAC provide protection |

### Why Keep Per-Type List/Describe Tools?

Per-type tools and generic tools serve different purposes:

| Aspect | Per-type (e.g. `kubernetes_list_pods`) | Generic (`kubernetes_resources_list`) |
|--------|----------------------------------------|---------------------------------------|
| Output | Curated: pods → status/node/images/IP, deployments → replicas/images, secrets → type only | Minimal: name, namespace, status |
| Describe redaction | `generic_describe.go` redacts `Secret.Data` → `"REDACTED"` | **Missing** — needs to be added |
| LLM guidance | Specific description per resource type | Generic fallback description |
| Use case | Common resources queried frequently | CRDs and rare resource types |

---

## Part A: Rename Existing Generic Tool Parameters

### `resource_list.go` — Rename Parameters

**Current** → **New**:
- `resourceGroup` → `apiGroup`
- `resourceVersion` → `apiVersion`
- `resourceKind` → `resource`

Update `ResourceListParams` struct tags and all references in `Invoke()`:

```go
type ResourceListParams struct {
    Cluster         string              `json:"cluster" validate:"required" jsonschema:"(required) The cluster to connect to."`
    Namespace       string              `json:"namespace,omitempty" jsonschema:"(optional) The namespace to list resources from."`
    LabelsSelector  string              `json:"labelsSelector,omitempty" jsonschema:"(optional) The labels selector..."`
    ApiVersion      string              `json:"apiVersion" validate:"required" jsonschema:"(required) The API version. For example, 'v1' or 'v1beta1'."`
    ApiGroup        string              `json:"apiGroup" validate:"required" jsonschema:"(required) The API group of the resource. For example, 'apps'."`
    Resource        string              `json:"resource" validate:"required" jsonschema:"(required) The resource type in plural lowercase. For example, 'deployments', 'pods'."`
    Filter          string              `json:"filter,omitempty" jsonschema:"(optional) A Go RE2 regex..."`
    Paginate        *ListParamsPaginate `json:"paginate,omitempty" jsonschema:"(optional) Pagination parameters."`
}
```

Update the GVR construction in `Invoke()`:
```go
namespaceResource := schema.GroupVersionResource{
    Group:    params.ApiGroup,
    Version:  params.ApiVersion,
    Resource: params.Resource,
}
```

### `resource_describe.go` — Rename Parameters + Add Secret Redaction

**Current** → **New**:
- `resourceGroup` → `apiGroup`
- `resourceVersion` → `apiVersion`
- `resourceKind` → `resource`

Update `ResourceDescribeParams` struct tags and all references in `Invoke()`.

**Add secret redaction** — when the GVR targets secrets (`resource == "secrets"`), redact `data` and `stringData` fields in the output:

```go
// After fetching the unstructured object, redact secret data:
if strings.ToLower(params.Resource) == "secrets" {
    if data, ok := o.Object["data"]; ok {
        if m, ok := data.(map[string]any); ok {
            for k := range m {
                m[k] = "REDACTED"
            }
        }
    }
    if stringData, ok := o.Object["stringData"]; ok {
        if m, ok := stringData.(map[string]any); ok {
            for k := range m {
                m[k] = "REDACTED"
            }
        }
    }
}
```

---

## Part B: New Write Tools

### 1. `kubernetes_resource_create` (`resource_create.go`)

**Purpose**: Create any Kubernetes resource from a JSON manifest.

**Parameters**:
```go
type ResourceCreateParams struct {
    Cluster     string `json:"cluster" validate:"required" jsonschema:"(required) The cluster to connect to."`
    Namespace   string `json:"namespace,omitempty" jsonschema:"(optional) The namespace for the resource. Omit for cluster-scoped resources."`
    ApiGroup    string `json:"apiGroup" validate:"required" jsonschema:"(required) The API group of the resource. For example, 'apps' for Deployments, or empty string for core resources like Pods."`
    ApiVersion  string `json:"apiVersion" validate:"required" jsonschema:"(required) The API version. For example, 'v1' or 'v1beta1'."`
    Resource    string `json:"resource" validate:"required" jsonschema:"(required) The resource type in plural lowercase. For example, 'deployments', 'pods', 'configmaps'."`
    Manifest    string `json:"manifest" validate:"required" jsonschema:"(required) The full resource manifest as a JSON string. Must include apiVersion, kind, and metadata."`
    DryRun      bool   `json:"dryRun,omitempty" jsonschema:"(optional) If true, use server-side dry-run to validate without creating. Show the result to the user and ask for confirmation."`
    Confirmed   bool   `json:"confirmed,omitempty" jsonschema:"(optional) Must be true to actually execute. Set this after the user has approved the dry-run result."`
}
```

**Implementation**:
- Parse manifest JSON into `unstructured.Unstructured`
- Set namespace on the object if provided
- Build GVR from `apiGroup`, `apiVersion`, `resource`
- If `DryRun`: use `metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}}` for server-side dry-run
- Call `dynamicClient.Resource(gvr).Namespace(ns).Create(ctx, obj, opts)`
- Return the created resource as JSON

### 2. `kubernetes_resource_patch` (`resource_patch.go`)

**Purpose**: Patch any Kubernetes resource using strategic merge, JSON merge, or JSON patch.

**Parameters**:
```go
type ResourcePatchParams struct {
    Cluster     string `json:"cluster" validate:"required" jsonschema:"(required) The cluster to connect to."`
    Namespace   string `json:"namespace,omitempty" jsonschema:"(optional) The namespace of the resource. Omit for cluster-scoped resources."`
    ApiGroup    string `json:"apiGroup" validate:"required" jsonschema:"(required) The API group of the resource."`
    ApiVersion  string `json:"apiVersion" validate:"required" jsonschema:"(required) The API version."`
    Resource    string `json:"resource" validate:"required" jsonschema:"(required) The resource type in plural lowercase."`
    Name        string `json:"name" validate:"required" jsonschema:"(required) The name of the resource to patch."`
    PatchType   string `json:"patchType" validate:"required,oneof=strategic merge json" jsonschema:"(required) The patch type: 'strategic' (strategic merge patch, default for most resources), 'merge' (JSON merge patch), or 'json' (JSON patch with operations like add/remove/replace)."`
    Patch       string `json:"patch" validate:"required" jsonschema:"(required) The patch document as a JSON string. For strategic/merge: a partial resource spec. For json: an array of operations like [{\"op\":\"replace\",\"path\":\"/spec/replicas\",\"value\":3}]."`
    DryRun      bool   `json:"dryRun,omitempty" jsonschema:"(optional) If true, use server-side dry-run to validate without patching."`
    Confirmed   bool   `json:"confirmed,omitempty" jsonschema:"(optional) Must be true to actually execute."`
}
```

**Implementation**:
- Map `patchType` to `types.PatchType`: strategic → `types.StrategicMergePatchType`, merge → `types.MergePatchType`, json → `types.JSONPatchType`
- Build GVR
- If `DryRun`: use `metav1.PatchOptions{DryRun: []string{metav1.DryRunAll}}`
- Call `dynamicClient.Resource(gvr).Namespace(ns).Patch(ctx, name, patchType, []byte(patch), opts)`
- Return the patched resource as JSON

### 3. `kubernetes_resource_delete` (`resource_delete.go`)

**Purpose**: Delete any Kubernetes resource by GVR and name.

**Parameters**:
```go
type ResourceDeleteParams struct {
    Cluster            string `json:"cluster" validate:"required" jsonschema:"(required) The cluster to connect to."`
    Namespace          string `json:"namespace,omitempty" jsonschema:"(optional) The namespace of the resource. Omit for cluster-scoped resources."`
    ApiGroup           string `json:"apiGroup" validate:"required" jsonschema:"(required) The API group of the resource."`
    ApiVersion         string `json:"apiVersion" validate:"required" jsonschema:"(required) The API version."`
    Resource           string `json:"resource" validate:"required" jsonschema:"(required) The resource type in plural lowercase."`
    Name               string `json:"name" validate:"required" jsonschema:"(required) The name of the resource to delete."`
    Cascade            string `json:"cascade,omitempty" validate:"omitempty,oneof=background foreground orphan" jsonschema:"(optional) Deletion propagation: 'background' (default, delete dependents in background), 'foreground' (wait for dependents), 'orphan' (leave dependents)."`
    GracePeriodSeconds *int64 `json:"gracePeriodSeconds,omitempty" jsonschema:"(optional) Grace period in seconds before the resource is deleted. Use 0 for immediate deletion."`
    DryRun             bool   `json:"dryRun,omitempty" jsonschema:"(optional) If true, fetch the resource and return what would be deleted without actually deleting it."`
    Confirmed          bool   `json:"confirmed,omitempty" jsonschema:"(optional) Must be true to actually execute."`
}
```

**Implementation**:
- Build GVR
- If `DryRun`: fetch the resource with `Get()`, return `{"dryRun": true, "wouldDelete": <resource>}`
- Map `cascade` to `metav1.DeletionPropagation`
- Build `metav1.DeleteOptions` with propagation policy and grace period
- Call `dynamicClient.Resource(gvr).Namespace(ns).Delete(ctx, name, opts)`
- Return success message

### 4. `kubernetes_resource_apply` (`resource_apply.go`)

**Purpose**: Server-side apply (create-or-update) any Kubernetes resource.

**Parameters**:
```go
type ResourceApplyParams struct {
    Cluster       string `json:"cluster" validate:"required" jsonschema:"(required) The cluster to connect to."`
    Namespace     string `json:"namespace,omitempty" jsonschema:"(optional) The namespace for the resource. Omit for cluster-scoped resources."`
    ApiGroup      string `json:"apiGroup" validate:"required" jsonschema:"(required) The API group of the resource."`
    ApiVersion    string `json:"apiVersion" validate:"required" jsonschema:"(required) The API version."`
    Resource      string `json:"resource" validate:"required" jsonschema:"(required) The resource type in plural lowercase."`
    Manifest      string `json:"manifest" validate:"required" jsonschema:"(required) The resource manifest as a JSON string. Must include apiVersion, kind, and metadata."`
    FieldManager  string `json:"fieldManager,omitempty" jsonschema:"(optional) The field manager name for server-side apply. Defaults to 'eino-agent'."`
    Force         bool   `json:"force,omitempty" jsonschema:"(optional) If true, force apply even if another field manager owns conflicting fields."`
    DryRun        bool   `json:"dryRun,omitempty" jsonschema:"(optional) If true, use server-side dry-run to validate without applying."`
    Confirmed     bool   `json:"confirmed,omitempty" jsonschema:"(optional) Must be true to actually execute."`
}
```

**Implementation**:
- Parse manifest JSON into `unstructured.Unstructured`
- Set namespace on the object if provided
- Build GVR
- Default `fieldManager` to `"eino-agent"`
- Server-side apply uses `Patch` with `types.ApplyPatchType`:
```go
dynamicClient.Resource(gvr).Namespace(ns).Patch(ctx, name, types.ApplyPatchType, data, metav1.PatchOptions{
    FieldManager: fieldManager,
    Force:        &force,
    DryRun:       dryRunSlice,
})
```
- Return the applied resource as JSON

---

## Files to Create/Modify

### New Files

| File | Purpose |
|------|---------|
| `components/tool/kubernetes/resource_create.go` | Create tool implementation |
| `components/tool/kubernetes/resource_patch.go` | Patch tool implementation |
| `components/tool/kubernetes/resource_delete.go` | Delete tool implementation |
| `components/tool/kubernetes/resource_apply.go` | Apply tool implementation |

### Modified Files

| File | Change |
|------|--------|
| `components/tool/kubernetes/resource_list.go` | Rename params: `resourceGroup`→`apiGroup`, `resourceVersion`→`apiVersion`, `resourceKind`→`resource` |
| `components/tool/kubernetes/resource_describe.go` | Rename params (same as above) + add secret data redaction |
| `components/tool/kubernetes/registry.go` | Add 4 new tools to `writeConstructors`, update `WriteToolNames()` |

## Implementation Pattern

Each new tool follows the established pattern from `resource_list.go` and `resource_describe.go`:

1. **Params struct** with `json`, `validate`, and `jsonschema` tags
2. **Tool struct** embedding `*baseToolWithDynamic` + `tool.InvokableTool`
3. **Invoke method** with typed params:
   - Validate params with `validate.Struct()`
   - Get dynamic client via `t.dynamicClient(params.Cluster)`
   - Build `schema.GroupVersionResource`
   - Execute the operation
   - Return JSON result
4. **Factory function** using `utils.InferTool()`

## Registry Changes

```go
// In registry.go, add to writeConstructors:
var writeConstructors = []toolConstructor{
    func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewPodExecTool(ctx, c) },
    func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewResourceCreateTool(ctx, c) },
    func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewResourcePatchTool(ctx, c) },
    func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewResourceDeleteTool(ctx, c) },
    func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewResourceApplyTool(ctx, c) },
}

// Update WriteToolNames():
func WriteToolNames() []string {
    return []string{
        "kubernetes_pod_exec",
        "kubernetes_resource_create",
        "kubernetes_resource_patch",
        "kubernetes_resource_delete",
        "kubernetes_resource_apply",
    }
}
```

## Security Considerations

1. **DryRun/Confirmed gate**: All 4 write tools include `dryRun` and `confirmed` fields, compatible with the existing safety middleware in `libs/toolkit/safety/gate.go`
2. **Server-side dry-run**: Uses Kubernetes native `DryRunAll` option for accurate validation
3. **No blocklist needed**: Unlike `pod_exec`, these tools operate on the K8s API (not shell commands), so command injection is not a concern
4. **Secret redaction**: `resource_describe.go` now redacts `data` and `stringData` when describing secrets, matching `generic_describe.go` behavior
5. **Ownership awareness**: The safety middleware's ownership checker (`libs/toolkit/safety/ownership.go`) can be used to warn about modifying controller-managed resources
6. **RBAC**: The Kubernetes service account's permissions naturally limit what can be done — no additional resource-type restrictions needed in the tools

## Validation Plan

1. **Unit tests**: Test each tool's Invoke method with mock dynamic clients
2. **Integration test**: Create a test that:
   - Creates a ConfigMap via `kubernetes_resource_create`
   - Patches it via `kubernetes_resource_patch`
   - Applies an update via `kubernetes_resource_apply`
   - Deletes it via `kubernetes_resource_delete`
3. **Dry-run test**: Verify each tool returns correct dry-run output without modifying the cluster
4. **Secret redaction test**: Verify `resource_describe.go` redacts secret data
5. **Parameter rename test**: Verify `resource_list.go` and `resource_describe.go` work with new parameter names
6. **Error cases**: Test with invalid manifests, non-existent resources, invalid GVR
