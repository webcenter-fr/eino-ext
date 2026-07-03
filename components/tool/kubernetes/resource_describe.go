package kubernetes

import (
	"context"
	"strings"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/goccy/go-json"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const resourceDescribeDescription = `
** General Purpose **
It gets the details of a specific resource in a specified Kubernetes cluster.
You can use it when there a are no dedicated tool for describing a specific Kubernetes resource type, or when you want to describe a custom resource.

** Output **
It return a JSON object representing the kubernetes resource.
`

// ResourceDescribeParams defines the parameters for the ResourceDescribe function.
type ResourceDescribeParams struct {
	Cluster             string   `json:"cluster" validate:"required" jsonschema:"(required) The cluster to connect to."`
	Namespace           string   `json:"namespace" validate:"required" jsonschema:"(required) The namespace of the resource."`
	Name                string   `json:"name" validate:"required" jsonschema:"(required) The resource name."`
	ApiVersion          string   `json:"apiVersion" validate:"required" jsonschema:"(required) The API version. For example, 'v1' or 'v1beta1'."`
	ApiGroup            string   `json:"apiGroup" validate:"required" jsonschema:"(required) The API group of the resource. For example, 'apps'."`
	Resource            string   `json:"resource" validate:"required" jsonschema:"(required) The resource type in plural lowercase. For example, 'deployments', 'pods'."`
	ExcludeFieldsOutput []string `json:"excludeFieldsOutput,omitempty" validate:"omitempty,dive,oneof=metadata spec status data" jsonschema:"(optional) The fields to exclude from the output. Default to no exclusion. You can set 'metadata', 'spec', 'status', and 'data'."`
}

// ResourceDescribeOutput defines the structure of the output returned by the ResourceDescribe function.
type ResourceDescribeOutput struct {
	metav1.TypeMeta `json:",inline"`
	Metadata        *metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec            any                `json:"spec,omitempty"`
	Status          any                `json:"status,omitempty"`
	Data            any                `json:"data,omitempty"`
}

// ResourceDescribeTool is a tool that gets the details of a specific resource in a specified Kubernetes cluster.
type ResourceDescribeTool struct {
	*baseToolWithDynamic
	tool.InvokableTool
}

// redactSecretData replaces all values in the given data map with "REDACTED".
func redactSecretData(data any) {
	m, ok := data.(map[string]any)
	if !ok {
		return
	}
	for k := range m {
		m[k] = "REDACTED"
	}
}

// Invoke executes the ResourceDescribeTool with the given parameters.
func (t *ResourceDescribeTool) Invoke(ctx context.Context, params *ResourceDescribeParams) (result string, err error) {

	if err := validate.Struct(params); err != nil {
		return "", err
	}

	c, err := t.dynamicClient(params.Cluster)
	if err != nil {
		return "", err
	}

	namespaceResource := schema.GroupVersionResource{
		Group:    params.ApiGroup,
		Version:  params.ApiVersion,
		Resource: params.Resource,
	}

	o, err := c.Resource(namespaceResource).Namespace(params.Namespace).Get(ctx, params.Name, metav1.GetOptions{})
	if err != nil {
		return "", errors.Wrapf(err, "failed to get resource %s/%s of type %s.%s/%s", params.Namespace, params.Name, params.Resource, params.ApiGroup, params.ApiVersion)
	}

	// Redact secret data to avoid leaking sensitive information.
	if strings.ToLower(params.Resource) == "secrets" {
		if data, ok := o.Object["data"]; ok {
			redactSecretData(data)
		}
		if stringData, ok := o.Object["stringData"]; ok {
			redactSecretData(stringData)
		}
	}

	output, err := objectToDescribeOutput(o)
	if err != nil {
		return "", errors.Wrap(err, "failed to convert object to describe output")
	}
	output.Spec = o.Object["spec"]
	output.Status = o.Object["status"]
	output.Data = o.Object["data"]

	for _, excludeField := range params.ExcludeFieldsOutput {
		switch excludeField {
		case "metadata":
			output.Metadata = nil
		case "spec":
			output.Spec = nil
		case "status":
			output.Status = nil
		case "data":
			output.Data = nil
		default:
			return "", errors.Errorf("invalid exclude field: %s", excludeField)
		}
	}

	data, err := json.Marshal(output)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal output")
	}

	return string(data), nil

}

// NewResourceDescribeTool creates a new instance of the ResourceDescribeTool.
func NewResourceDescribeTool(ctx context.Context, configs Configs) (tool.InvokableTool, error) {

	clients, err := BuildClientDynamics(configs, nil)
	if err != nil {
		return nil, err
	}
	base, err := newBaseTool(configs)
	if err != nil {
		return nil, err
	}

	describeTool := &ResourceDescribeTool{
		baseToolWithDynamic: &baseToolWithDynamic{
			baseTool: base,
			dynamics: clients,
		},
	}

	// Infer tool
	t, err := utils.InferTool("kubernetes_describe_resource", resourceDescribeDescription, describeTool.Invoke)
	if err != nil {
		return nil, err
	}
	describeTool.InvokableTool = t

	return describeTool, nil
}
