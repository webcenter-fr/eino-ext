package kubernetes

import (
	"context"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/goccy/go-json"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/confirm"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const resourceDeleteDescription = `
** General Purpose **
It deletes any Kubernetes resource by GVR and name.
Works with core resources (Pods, ConfigMaps, Services, etc.) as well as CRDs.

** Deletion Propagation **
- 'background' (default): Delete the resource and its dependents in the background.
- 'foreground': Block until all dependents are deleted before deleting the resource.
- 'orphan': Delete the resource but leave its dependents (e.g., Pods from a ReplicaSet).

** Safety **
Always use dryRun=true first to preview what would be deleted.
After reviewing the dry-run result, set confirmed=true to actually delete.

** Output **
On success, returns a confirmation message. On dry-run, returns the resource that would be deleted.
`

// ResourceDeleteParams defines the parameters for the ResourceDelete function.
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

// ResourceDeleteTool deletes any Kubernetes resource.
type ResourceDeleteTool struct {
	*baseToolWithDynamic
	tool.InvokableTool
}

// mapCascadePolicy maps the string cascade option to a metav1.DeletionPropagation.
func mapCascadePolicy(cascade string) *metav1.DeletionPropagation {
	var p metav1.DeletionPropagation
	switch cascade {
	case "background":
		p = metav1.DeletePropagationBackground
	case "foreground":
		p = metav1.DeletePropagationForeground
	case "orphan":
		p = metav1.DeletePropagationOrphan
	default:
		return nil
	}
	return &p
}

// Invoke executes the ResourceDeleteTool with the given parameters.
func (t *ResourceDeleteTool) Invoke(ctx context.Context, params *ResourceDeleteParams) (result string, err error) {

	if err := validate.Struct(params); err != nil {
		return "", err
	}

	// Enforce safety gate: require confirmation for non-dry-run operations.
	if err := confirm.RequireConfirmation(params.DryRun, params.Confirmed); err != nil {
		return "", err
	}

	c, err := t.dynamicClient(params.Cluster)
	if err != nil {
		return "", err
	}

	gvr := toGVR(params.ApiGroup, params.ApiVersion, params.Resource)

	// Dry-run: fetch the resource and return what would be deleted.
	if params.DryRun {
		existing, getErr := c.Resource(gvr).Namespace(params.Namespace).Get(ctx, params.Name, metav1.GetOptions{})
		if getErr != nil {
			return "", errors.Wrapf(getErr, "failed to fetch resource for dry-run %s/%s of type %s.%s/%s", params.Namespace, params.Name, params.Resource, params.ApiGroup, params.ApiVersion)
		}
		unstructured.RemoveNestedField(existing.Object, "metadata", "managedFields")

		dryRunResult := map[string]any{
			"dryRun":      true,
			"wouldDelete": existing.Object,
		}
		data, err := json.Marshal(dryRunResult)
		if err != nil {
			return "", errors.Wrap(err, "failed to marshal dry-run result")
		}
		return string(data), nil
	}

	opts := metav1.DeleteOptions{}
	if cp := mapCascadePolicy(params.Cascade); cp != nil {
		opts.PropagationPolicy = cp
	}
	if params.GracePeriodSeconds != nil {
		opts.GracePeriodSeconds = params.GracePeriodSeconds
	}

	if err := c.Resource(gvr).Namespace(params.Namespace).Delete(ctx, params.Name, opts); err != nil {
		return "", errors.Wrapf(err, "failed to delete resource %s/%s of type %s.%s/%s", params.Namespace, params.Name, params.Resource, params.ApiGroup, params.ApiVersion)
	}

	data, err := json.Marshal(map[string]any{
		"deleted":      true,
		"resourceType": gvr.Resource,
		"name":         params.Name,
		"namespace":    params.Namespace,
	})
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal delete result")
	}

	return string(data), nil
}

// NewResourceDeleteTool creates a new instance of the ResourceDeleteTool.
func NewResourceDeleteTool(ctx context.Context, configs Configs) (tool.InvokableTool, error) {

	base, err := newBaseToolWithDynamic(ctx, configs)
	if err != nil {
		return nil, err
	}

	deleteTool := &ResourceDeleteTool{
		baseToolWithDynamic: base,
	}

	// Infer tool
	t, err := utils.InferTool("kubernetes_resource_delete", resourceDeleteDescription, deleteTool.Invoke)
	if err != nil {
		return nil, err
	}
	deleteTool.InvokableTool = t

	return deleteTool, nil
}
