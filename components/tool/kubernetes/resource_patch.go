package kubernetes

import (
	"context"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/goccy/go-json"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/confirm"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/safety"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

const resourcePatchDescription = `
** General Purpose **
It patches any Kubernetes resource using strategic merge, JSON merge, or JSON patch.
Works with core resources (Pods, ConfigMaps, Services, etc.) as well as CRDs.

** Patch Types **
- 'strategic': Strategic merge patch (default for most Kubernetes resources). Only specify the fields you want to change.
- 'merge': JSON merge patch (RFC 7386). Replace the entire value at the given paths.
- 'json': JSON patch (RFC 6902). Use operations like add, remove, replace, move, copy, test.

** Safety **
Always use dryRun=true first to validate the patch before applying.
After reviewing the dry-run result, set confirmed=true to actually apply the patch.

** Output **
It returns the patched resource as a JSON object.
`

// ResourcePatchParams defines the parameters for the ResourcePatch function.
type ResourcePatchParams struct {
	Cluster    string `json:"cluster" validate:"required" jsonschema:"(required) The cluster to connect to."`
	Namespace  string `json:"namespace,omitempty" jsonschema:"(optional) The namespace of the resource. Omit for cluster-scoped resources."`
	ApiGroup   string `json:"apiGroup" validate:"required" jsonschema:"(required) The API group of the resource."`
	ApiVersion string `json:"apiVersion" validate:"required" jsonschema:"(required) The API version."`
	Resource   string `json:"resource" validate:"required" jsonschema:"(required) The resource type in plural lowercase."`
	Name       string `json:"name" validate:"required" jsonschema:"(required) The name of the resource to patch."`
	PatchType  string `json:"patchType" validate:"required,oneof=strategic merge json" jsonschema:"(required) The patch type: 'strategic' (strategic merge patch, default for most resources), 'merge' (JSON merge patch), or 'json' (JSON patch with operations like add/remove/replace)."`
	Patch      string `json:"patch" validate:"required" jsonschema:"(required) The patch document as a JSON string. For strategic/merge: a partial resource spec. For json: an array of operations like [{\"op\":\"replace\",\"path\":\"/spec/replicas\",\"value\":3}]."`
	DryRun     bool   `json:"dryRun,omitempty" jsonschema:"(optional) If true, use server-side dry-run to validate without patching."`
	Confirmed  bool   `json:"confirmed,omitempty" jsonschema:"(optional) Must be true to actually execute."`
}

// ResourcePatchTool patches any Kubernetes resource.
type ResourcePatchTool struct {
	*baseToolWithDynamic
	tool.InvokableTool
}

// mapPatchType maps the string patch type to the Kubernetes PatchType constant.
func mapPatchType(pt string) types.PatchType {
	switch pt {
	case "strategic":
		return types.StrategicMergePatchType
	case "merge":
		return types.MergePatchType
	case "json":
		return types.JSONPatchType
	default:
		return types.StrategicMergePatchType
	}
}

// Invoke executes the ResourcePatchTool with the given parameters.
func (t *ResourcePatchTool) Invoke(ctx context.Context, params *ResourcePatchParams) (result string, err error) {

	if err := validate.Struct(params); err != nil {
		return "", err
	}

	// Enforce safety gate: require confirmation for non-dry-run operations.
	if err := confirm.RequireConfirmation(params.DryRun, params.Confirmed); err != nil {
		return "", err
	}

	// Check namespace is allowed.
	if err := t.checkNamespace(params.Cluster, params.Namespace); err != nil {
		return "", err
	}

	// Block patching of security-sensitive resources.
	if res, ok := blocklistedResources[params.ApiGroup]; ok && res[params.Resource] {
		return "", errors.Errorf("patching resources of type %q in API group %q is blocked for security reasons", params.Resource, params.ApiGroup)
	}

	c, err := t.dynamicClient(params.Cluster)
	if err != nil {
		return "", err
	}

	gvr := toGVR(params.ApiGroup, params.ApiVersion, params.Resource)

	patchType := mapPatchType(params.PatchType)

	ctx, cancel := withTimeout(ctx, t.getDefaultTimeout(params.Cluster))
	defer cancel()

	opts := metav1.PatchOptions{}
	if params.DryRun {
		opts.DryRun = []string{metav1.DryRunAll}
	}

	// Dry-run: fetch the existing resource for ownership check.
	if params.DryRun {
		existing, getErr := c.Resource(gvr).Namespace(params.Namespace).Get(ctx, params.Name, metav1.GetOptions{})
		if getErr == nil {
			ownership := safety.CheckOwnership(existing)
			// Pre-fetch and attach ownership to the dry-run result below.
			patched, patchErr := c.Resource(gvr).Namespace(params.Namespace).Patch(ctx, params.Name, patchType, []byte(params.Patch), opts)
			if patchErr != nil {
				return "", errors.Wrapf(patchErr, "failed to patch resource %s/%s of type %s.%s/%s (dry-run)", params.Namespace, params.Name, params.Resource, params.ApiGroup, params.ApiVersion)
			}
			unstructured.RemoveNestedField(patched.Object, "metadata", "managedFields")
			dryRunResult := map[string]any{
				"dryRun":       true,
				"wouldPatchTo": patched.Object,
			}
			if ownership.IsManaged {
				dryRunResult["ownership"] = ownership
			}
			data, err := json.Marshal(dryRunResult)
			if err != nil {
				return "", errors.Wrap(err, "failed to marshal dry-run result")
			}
			return string(data), nil
		}
	}

	patched, err := c.Resource(gvr).Namespace(params.Namespace).Patch(ctx, params.Name, patchType, []byte(params.Patch), opts)
	if err != nil {
		return "", errors.Wrapf(err, "failed to patch resource %s/%s of type %s.%s/%s", params.Namespace, params.Name, params.Resource, params.ApiGroup, params.ApiVersion)
	}

	// Remove managedFields to reduce noise in the output.
	unstructured.RemoveNestedField(patched.Object, "metadata", "managedFields")

	data, err := json.Marshal(patched.Object)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal patched resource")
	}

	return string(data), nil
}

// NewResourcePatchTool creates a new instance of the ResourcePatchTool.
func NewResourcePatchTool(ctx context.Context, configs Configs) (tool.InvokableTool, error) {

	base, err := newBaseToolWithDynamic(ctx, configs)
	if err != nil {
		return nil, err
	}

	patchTool := &ResourcePatchTool{
		baseToolWithDynamic: base,
	}

	// Infer tool
	t, err := utils.InferTool("kubernetes_resource_patch", resourcePatchDescription, patchTool.Invoke)
	if err != nil {
		return nil, err
	}
	patchTool.InvokableTool = t

	return patchTool, nil
}
