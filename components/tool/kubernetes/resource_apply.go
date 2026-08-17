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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

const resourceApplyDescription = `
** General Purpose **
It applies (creates or updates) any Kubernetes resource using server-side apply.
Works with core resources (Pods, ConfigMaps, Services, etc.) as well as CRDs.
The 'kind' parameter accepts a PascalCase singular kind (e.g. 'Pod', 'Deployment', 'ConfigMap'), a kubectl shortname ('po', 'deploy'), or a 'resource.group' form ('deployments.apps'). Plural resource names ('pods') are also accepted.

** Server-Side Apply **
Server-side apply tracks field ownership, allowing multiple controllers to manage
different fields of the same resource without conflicts. Use 'force=true' to take
ownership of fields that are managed by another field manager.

** Safety **
Always use dryRun=true first to validate the apply before committing.
After reviewing the dry-run result, set confirmed=true to actually apply.

** Output **
It returns the applied resource as a JSON object.
`

// ResourceApplyParams defines the parameters for the ResourceApply function.
type ResourceApplyParams struct {
	Cluster      string `json:"cluster" validate:"required" jsonschema:"(required) The cluster to connect to."`
	Namespace    string `json:"namespace,omitempty" jsonschema:"(optional) The namespace for the resource. Omit for cluster-scoped resources."`
	Kind         string `json:"kind" validate:"required" jsonschema:"(required) The resource kind in PascalCase singular (e.g. 'Pod', 'Deployment', 'ConfigMap'). Also accepts kubectl shortnames ('po', 'deploy'), and 'resource.group' form ('deployments.apps'). Plural resource names ('pods') are accepted but PascalCase is preferred."`
	Manifest     string `json:"manifest" validate:"required" jsonschema:"(required) The resource manifest as a JSON string. Must include apiVersion, kind, and metadata."`
	FieldManager string `json:"fieldManager,omitempty" jsonschema:"(optional) The field manager name for server-side apply. Defaults to 'eino-agent'."`
	Force        bool   `json:"force,omitempty" jsonschema:"(optional) If true, force apply even if another field manager owns conflicting fields."`
	DryRun       bool   `json:"dryRun,omitempty" jsonschema:"(optional) If true, use server-side dry-run to validate without applying."`
	Confirmed    bool   `json:"confirmed,omitempty" jsonschema:"(optional) Must be true to actually execute."`
}

// ResourceApplyTool applies (creates or updates) any Kubernetes resource using server-side apply.
type ResourceApplyTool struct {
	*baseTool
	tool.InvokableTool
}

// Invoke executes the ResourceApplyTool with the given parameters.
func (t *ResourceApplyTool) Invoke(ctx context.Context, params *ResourceApplyParams) (result string, err error) {

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

	c, err := t.dynamicClient(params.Cluster)
	if err != nil {
		return "", err
	}

	// Resolve kind to GVR via cached mapper.
	resolved, err := t.resolveKind(ctx, params.Cluster, params.Kind)
	if err != nil {
		return "", err
	}

	// Parse the manifest JSON into an unstructured object.
	obj := &unstructured.Unstructured{}
	if err := json.Unmarshal([]byte(params.Manifest), &obj.Object); err != nil {
		return "", errors.Wrap(err, "invalid manifest JSON")
	}

	// Validate required manifest fields.
	if obj.GetAPIVersion() == "" || obj.GetKind() == "" || obj.GetName() == "" {
		return "", errors.New("manifest must include apiVersion, kind, and metadata.name")
	}

	// Verify resolved GVK matches manifest GVK.
	if obj.GetKind() != resolved.GVK.Kind {
		return "", errors.Errorf("resolved kind %q does not match manifest kind %q", resolved.GVK.Kind, obj.GetKind())
	}

	// Block apply of security-sensitive resource kinds.
	if err := checkBlocklist(resolved.GVK, resolved.GVR, "applying"); err != nil {
		return "", err
	}

	// Validate pod spec security for Pod/Job/CronJob kinds.
	if err := validateManifestSecurity(obj); err != nil {
		return "", errors.Wrap(err, "manifest security validation failed")
	}

	// Override namespace if provided.
	if params.Namespace != "" {
		obj.SetNamespace(params.Namespace)
	}

	// Default field manager.
	fieldManager := params.FieldManager
	if fieldManager == "" {
		fieldManager = "eino-agent"
	}

	gvr := resolved.GVR

	ctx, cancel := withTimeout(ctx, t.getDefaultTimeout(params.Cluster))
	defer cancel()

	opts := metav1.PatchOptions{
		FieldManager: fieldManager,
		Force:        &params.Force,
	}
	if params.DryRun {
		opts.DryRun = []string{metav1.DryRunAll}
	}

	// Dry-run: fetch the existing resource for ownership check.
	if params.DryRun {
		existing, getErr := c.Resource(gvr).Namespace(params.Namespace).Get(ctx, obj.GetName(), metav1.GetOptions{})
		if getErr == nil {
			ownership := safety.CheckOwnership(existing)
			manifestData, marshalErr := json.Marshal(obj.Object)
			if marshalErr != nil {
				return "", errors.Wrap(marshalErr, "failed to marshal manifest")
			}
			applied, applyErr := c.Resource(gvr).Namespace(params.Namespace).Patch(
				ctx,
				obj.GetName(),
				types.ApplyPatchType,
				manifestData,
				opts,
			)
			if applyErr != nil {
				return "", errors.Wrapf(applyErr, "failed to apply resource %s/%s of type %s (dry-run)", params.Namespace, obj.GetName(), resolved.GVK.Kind)
			}
			unstructured.RemoveNestedField(applied.Object, "metadata", "managedFields")
			dryRunResult := map[string]any{
				"dryRun":       true,
				"wouldApplyTo": applied.Object,
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

	manifestData, err := json.Marshal(obj.Object)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal manifest")
	}

	applied, err := c.Resource(gvr).Namespace(params.Namespace).Patch(
		ctx,
		obj.GetName(),
		types.ApplyPatchType,
		manifestData,
		opts,
	)
	if err != nil {
		return "", errors.Wrapf(err, "failed to apply resource %s/%s of type %s", params.Namespace, obj.GetName(), resolved.GVK.Kind)
	}

	// Remove managedFields to reduce noise in the output.
	unstructured.RemoveNestedField(applied.Object, "metadata", "managedFields")

	data, err := json.Marshal(applied.Object)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal applied resource")
	}

	return string(data), nil
}

// NewResourceApplyTool creates a new instance of the ResourceApplyTool.
func NewResourceApplyTool(ctx context.Context, configs Configs) (tool.InvokableTool, error) {

	base, err := newBaseTool(ctx, configs)
	if err != nil {
		return nil, err
	}

	applyTool := &ResourceApplyTool{
		baseTool: base,
	}

	// Infer tool
	t, err := utils.InferTool("kubernetes_resource_apply", resourceApplyDescription, applyTool.Invoke)
	if err != nil {
		return nil, err
	}
	applyTool.InvokableTool = t

	return applyTool, nil
}
