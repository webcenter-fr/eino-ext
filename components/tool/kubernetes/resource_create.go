package kubernetes

import (
	"context"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/goccy/go-json"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/confirm"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// blocklistedKinds contains Kubernetes resource kinds that must not be created
// via the dynamic resource tools due to their security impact.
var blocklistedKinds = map[string]bool{
	"ClusterRole":        true,
	"ClusterRoleBinding": true,
	"Namespace":          true,
	"NetworkPolicy":      true,
	"PodSecurityPolicy":  true,
	"PriorityClass":      true,
	"ValidatingWebhookConfiguration": true,
	"MutatingWebhookConfiguration":   true,
	"RuntimeClass":                   true,
}

// checkBlocklist returns an error if the given GVK or GVR is security-blocklisted
// for the specified operation (e.g. "creating", "patching").
func checkBlocklist(gvk schema.GroupVersionKind, gvr schema.GroupVersionResource, op string) error {
	if blocklistedKinds[gvk.Kind] {
		return errors.Errorf("%s resources of kind %q is blocked for security reasons", op, gvk.Kind)
	}
	if res, ok := blocklistedResources[gvr.Group]; ok && res[gvr.Resource] {
		return errors.Errorf("%s resources of type %q in API group %q is blocked for security reasons", op, gvr.Resource, gvr.Group)
	}
	return nil
}

const resourceCreateDescription = `
** General Purpose **
It creates any Kubernetes resource from a JSON manifest using the dynamic client.
Works with core resources (Pods, ConfigMaps, Services, etc.) as well as CRDs.
The 'kind' parameter accepts a PascalCase singular kind (e.g. 'Pod', 'Deployment', 'ConfigMap'), a kubectl shortname ('po', 'deploy'), or a 'resource.group' form ('deployments.apps'). Plural resource names ('pods') are also accepted.

** Safety **
Always use dryRun=true first to validate the resource before creating.
After reviewing the dry-run result, set confirmed=true to actually create the resource.

** Output **
It returns the created resource as a JSON object.
`

// ResourceCreateParams defines the parameters for the ResourceCreate function.
type ResourceCreateParams struct {
	Cluster   string `json:"cluster" validate:"required" jsonschema:"(required) The cluster to connect to."`
	Namespace string `json:"namespace,omitempty" jsonschema:"(optional) The namespace for the resource. Omit for cluster-scoped resources."`
	Kind      string `json:"kind" validate:"required" jsonschema:"(required) The resource kind in PascalCase singular (e.g. 'Pod', 'Deployment', 'ConfigMap'). Also accepts kubectl shortnames ('po', 'deploy'), and 'resource.group' form ('deployments.apps'). Plural resource names ('pods') are accepted but PascalCase is preferred."`
	Manifest  string `json:"manifest" validate:"required" jsonschema:"(required) The full resource manifest as a JSON string. Must include apiVersion, kind, and metadata."`
	DryRun    bool   `json:"dryRun,omitempty" jsonschema:"(optional) If true, use server-side dry-run to validate without creating. Show the result to the user and ask for confirmation."`
	Confirmed bool   `json:"confirmed,omitempty" jsonschema:"(optional) Must be true to actually execute. Set this after the user has approved the dry-run result."`
}

// ResourceCreateTool creates any Kubernetes resource from a JSON manifest.
type ResourceCreateTool struct {
	*baseTool
	tool.InvokableTool
}

// Invoke executes the ResourceCreateTool with the given parameters.
func (t *ResourceCreateTool) Invoke(ctx context.Context, params *ResourceCreateParams) (result string, err error) {

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

	// Block creation of security-sensitive resource kinds.
	if err := checkBlocklist(resolved.GVK, resolved.GVR, "creating"); err != nil {
		return "", err
	}

	// Validate pod spec security for Pod/Job/CronJob kinds.
	if err := validateManifestSecurity(obj); err != nil {
		return "", errors.Wrap(err, "manifest security validation failed")
	}

	// Override namespace if provided (allows the LLM to specify it separately).
	if params.Namespace != "" {
		obj.SetNamespace(params.Namespace)
	}

	gvr := resolved.GVR

	ctx, cancel := withTimeout(ctx, t.getDefaultTimeout(params.Cluster))
	defer cancel()

	opts := metav1.CreateOptions{}
	if params.DryRun {
		opts.DryRun = []string{metav1.DryRunAll}
	}

	created, err := c.Resource(gvr).Namespace(params.Namespace).Create(ctx, obj, opts)
	if err != nil {
		return "", errors.Wrapf(err, "failed to create resource %s (GVR: %s)", resolved.GVK.Kind, gvr)
	}

	// Remove managedFields to reduce noise in the output.
	unstructured.RemoveNestedField(created.Object, "metadata", "managedFields")

	data, err := json.Marshal(created.Object)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal created resource")
	}

	return string(data), nil
}

// NewResourceCreateTool creates a new instance of the ResourceCreateTool.
func NewResourceCreateTool(ctx context.Context, configs Configs) (tool.InvokableTool, error) {

	base, err := newBaseTool(ctx, configs)
	if err != nil {
		return nil, err
	}

	createTool := &ResourceCreateTool{
		baseTool: base,
	}

	// Infer tool
	t, err := utils.InferTool("kubernetes_resource_create", resourceCreateDescription, createTool.Invoke)
	if err != nil {
		return nil, err
	}
	createTool.InvokableTool = t

	return createTool, nil
}
