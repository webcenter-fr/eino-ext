package kubernetes

import (
	"context"
	"fmt"
	"strings"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/goccy/go-json"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// DescribeParams defines the parameters for describing a Kubernetes resource.
type DescribeParams struct {
	Cluster             string   `json:"cluster" validate:"required" jsonschema:"(required) The cluster to connect to."`
	Kind                string   `json:"kind" validate:"required" jsonschema:"(required) The resource kind in PascalCase singular (e.g. 'Pod', 'Deployment', 'ConfigMap'). Also accepts kubectl shortnames ('po', 'deploy'), and 'resource.group' form ('deployments.apps'). Plural resource names ('pods') are accepted but PascalCase is preferred."`
	Name                string   `json:"name" validate:"required" jsonschema:"(required) The resource name."`
	Namespace           string   `json:"namespace,omitempty" jsonschema:"(optional) The namespace of the resource. Ignored for cluster-scoped kinds."`
	ExcludeFieldsOutput []string `json:"excludeFieldsOutput,omitempty" validate:"omitempty,dive,oneof=metadata spec status data" jsonschema:"(optional) The fields to exclude from the output. Default to no exclusion. You can set 'metadata', 'spec', 'status', and 'data'."`
}

// describeExcludableFields are the top-level fields a caller may ask the
// describe tool to omit from the output.
var describeExcludableFields = []string{"metadata", "spec", "status", "data"}

// validateExcludeFields validates every requested exclusion against the
// top-level fields the describe tool can omit.
func validateExcludeFields(excludeFields []string) error {
	for _, excludeField := range excludeFields {
		switch excludeField {
		case "metadata", "spec", "status", "data":
			continue
		default:
			return errors.Errorf("parameter 'excludeFieldsOutput' has invalid value %q; allowed values are: %s. Remove or fix it and retry",
				excludeField, strings.Join(describeExcludableFields, ", "))
		}
	}
	return nil
}

// marshalRawDescribeOutput marshals the full unstructured resource content as
// JSON, deleting only the top-level fields requested via excludeFields. Unlike
// a fixed metadata/spec/status/data struct, this preserves every field the API
// server returns — including non-standard top-level fields such as webhooks
// (ValidatingWebhookConfiguration / MutatingWebhookConfiguration), rules
// (ClusterRole / Role), roleRef and subjects (RoleBinding /
// ClusterRoleBinding), or value/globalDefault (PriorityClass).
func marshalRawDescribeOutput(o *unstructured.Unstructured, excludeFields []string) (string, error) {
	if err := validateExcludeFields(excludeFields); err != nil {
		return "", err
	}

	obj := o.DeepCopy().Object
	for _, field := range excludeFields {
		delete(obj, field)
	}

	data, err := json.Marshal(obj)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal output")
	}
	return string(data), nil
}

const describeDescription = `
** General Purpose **
It describes any Kubernetes resource and returns its full JSON content as stored in the cluster. The 'kind' parameter accepts a PascalCase singular kind (e.g. 'Pod', 'Deployment', 'ConfigMap'), a kubectl shortname ('po', 'deploy'), or a 'resource.group' form ('deployments.apps'). Plural resource names ('pods') are also accepted. Supports core types and CRDs.

** Output **
Returns the full JSON object for the resource: apiVersion, kind, metadata, and every remaining top-level field (spec, status, data, webhooks, rules, roleRef, subjects, ...).
`

// DescribeTool is an eino tool for describing Kubernetes resources.
type DescribeTool struct {
	*baseTool
	tool.InvokableTool
}

// Invoke returns the details of a Kubernetes resource as JSON.
func (t *DescribeTool) Invoke(ctx context.Context, params *DescribeParams) (string, error) {
	if err := validate.Struct(params); err != nil {
		return "", err
	}

	resolved, err := t.resolveKind(ctx, params.Cluster, params.Kind)
	if err != nil {
		return "", err
	}

	c, err := t.dynamicClient(params.Cluster)
	if err != nil {
		return "", err
	}

	ctx, cancel := withTimeout(ctx, t.getDefaultTimeout(params.Cluster))
	defer cancel()

	var namespace string
	if resolved.Scoped {
		namespace = params.Namespace
	}

	o, err := c.Resource(resolved.GVR).Namespace(namespace).Get(ctx, params.Name, metav1.GetOptions{})
	if err != nil {
		return "", errors.Wrapf(err, "failed to get %s/%s", resolved.GVK.Kind, params.Name)
	}

	if strings.ToLower(resolved.GVK.Kind) == "secret" {
		if data, ok := o.Object["data"]; ok {
			redactSecretData(data)
		}
		if stringData, ok := o.Object["stringData"]; ok {
			redactSecretData(stringData)
		}
	}

	return marshalRawDescribeOutput(o, params.ExcludeFieldsOutput)
}

// NewDescribeTool creates a new DescribeTool.
func NewDescribeTool(ctx context.Context, configs Configs) (tool.InvokableTool, error) {
	base, err := newBaseTool(ctx, configs)
	if err != nil {
		return nil, err
	}
	t := &DescribeTool{baseTool: base}
	inv, err := utils.InferTool("kubernetes_describe",
		fmt.Sprintf("%s\n%s", describeDescription, describeOutputGuidance),
		t.Invoke)
	if err != nil {
		return nil, err
	}
	t.InvokableTool = inv
	return t, nil
}
