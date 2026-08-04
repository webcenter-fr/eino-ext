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
)

type DescribeParams struct {
	Cluster             string   `json:"cluster" validate:"required" jsonschema:"(required) The cluster to connect to."`
	Kind                string   `json:"kind" validate:"required" jsonschema:"(required) The resource kind in PascalCase singular (e.g. 'Pod', 'Deployment', 'ConfigMap'). Also accepts kubectl shortnames ('po', 'deploy'), and 'resource.group' form ('deployments.apps'). Plural resource names ('pods') are accepted but PascalCase is preferred."`
	Name                string   `json:"name" validate:"required" jsonschema:"(required) The resource name."`
	Namespace           string   `json:"namespace,omitempty" jsonschema:"(optional) The namespace of the resource. Ignored for cluster-scoped kinds."`
	ExcludeFieldsOutput []string `json:"excludeFieldsOutput,omitempty" validate:"omitempty,dive,oneof=metadata spec status data" jsonschema:"(optional) The fields to exclude from the output. Default to no exclusion. You can set 'metadata', 'spec', 'status', and 'data'."`
}

type describeOutput struct {
	metav1.TypeMeta `json:",inline"`
	Metadata        *metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec            any                `json:"spec,omitempty"`
	Status          any                `json:"status,omitempty"`
	Data            any                `json:"data,omitempty"`
}

func (o *describeOutput) applyFieldExclusions(excludeFields []string) error {
	for _, excludeField := range excludeFields {
		switch excludeField {
		case "metadata":
			o.Metadata = nil
		case "spec":
			o.Spec = nil
		case "status":
			o.Status = nil
		case "data":
			o.Data = nil
		default:
			return errors.Errorf("invalid exclude field: %s", excludeField)
		}
	}
	return nil
}

const describeDescription = `
** General Purpose **
It describes any Kubernetes resource. The 'kind' parameter accepts a PascalCase singular kind (e.g. 'Pod', 'Deployment', 'ConfigMap'), a kubectl shortname ('po', 'deploy'), or a 'resource.group' form ('deployments.apps'). Plural resource names ('pods') are also accepted. Supports core types and CRDs.

** Output **
Returns a JSON object with metadata, spec, status, and data fields.
`

type DescribeTool struct {
	*baseTool
	tool.InvokableTool
}

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

	output := describeOutput{
		TypeMeta: metav1.TypeMeta{
			Kind:       o.GetKind(),
			APIVersion: o.GetAPIVersion(),
		},
		Metadata: &metav1.ObjectMeta{
			Name:              o.GetName(),
			Namespace:         o.GetNamespace(),
			Labels:            o.GetLabels(),
			Annotations:       o.GetAnnotations(),
			OwnerReferences:   o.GetOwnerReferences(),
			ResourceVersion:   o.GetResourceVersion(),
			CreationTimestamp: o.GetCreationTimestamp(),
			DeletionTimestamp: o.GetDeletionTimestamp(),
		},
		Spec:   o.Object["spec"],
		Status: o.Object["status"],
		Data:   o.Object["data"],
	}

	if err := output.applyFieldExclusions(params.ExcludeFieldsOutput); err != nil {
		return "", err
	}

	data, err := json.Marshal(output)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal output")
	}

	return string(data), nil
}

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
