package kubernetes

import (
	"context"
	"strings"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/go-playground/validator/v10"
	"github.com/goccy/go-json"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

const resourceDescribeDescription = `
** General Purpose **
It gets the details of a specific resource in a specified Kubernetes cluster.
You can use it when there a are no dedicated tool for describing a specific Kubernetes resource type, or when you want to describe a custom resource.

** Output **
It return a JSON object representing the kubernetes resource.
`

// ResourceDescribeParams defines the parameters for the ResourceDescribe function, which gets the details of a specific resource in a specified Kubernetes cluster. It includes the cluster name, namespace, and resource name.
type ResourceDescribeParams struct {
	Cluster             string   `json:"cluster" validate:"required" jsonschema:"(required) The cluster to connect to."`
	Namespace           string   `json:"namespace" validate:"required" jsonschema:"(required) The namespace of the resource."`
	Name                string   `json:"name" validate:"required" jsonschema:"(required) The resource name."`
	ResourceVersion     string   `json:"resourceVersion" validate:"required" jsonschema:"(required) The group and version of the resource, in the format of 'group/version'. For example, 'apps/v1'."`
	ResourceGroup       string   `json:"resourceGroup" validate:"required" jsonschema:"(required) The API group of the resource. For example, 'apps'."`
	ResourceKind        string   `json:"resourceKind" validate:"required" jsonschema:"(required) The kind of the resource. For example, 'Deployment'."`
	ExcludeFieldsOutput []string `json:"excludeFieldsOutput,omitempty" validate:"omitempty,dive,oneof=metadata spec status data" jsonschema:"(optional) The fields to exclude from the output. Default to no exclusion. You can set 'metadata', 'spec', 'status', and 'data'."`
}

// ResourceDescribeOutput defines the structure of the output returned by the ResourceDescribe function. It represents a resource with its metadata, spec, and status.
type ResourceDescribeOutput struct {
	metav1.TypeMeta `json:",inline"`
	Metadata        *metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec            any                `json:"spec,omitempty"`
	Status          any                `json:"status,omitempty"`
	Data            any                `json:"data,omitempty"`
}

// ResourceDescribeTool is a tool that gets the details of a specific resource in a specified Kubernetes cluster. It contains a map of Kubernetes clients for different clusters and implements the InvokableTool interface.
type ResourceDescribeTool struct {
	clients map[string]dynamic.Interface
	tool.InvokableTool
	knownClusters []string
}

// Invoke executes the DescribeTool with the given parameters. It validates the parameters, retrieves the appropriate Kubernetes client for the specified cluster, and lists the resources based on the provided namespace and label selector. The output is filtered using a regex pattern if provided, and the final result is returned as a JSON string.
func (t *ResourceDescribeTool) Invoke(ctx context.Context, params *ResourceDescribeParams) (result string, err error) {

	validator := validator.New()
	if err := validator.Struct(params); err != nil {
		return "", errors.Wrap(err, "invalid parameters for ResourceDescribeTool")
	}

	c, ok := t.clients[params.Cluster]
	if !ok {
		return "", errors.Errorf("Kubernetes cluster not found: %s. Cluster must be one of: %s", params.Cluster, strings.Join(t.knownClusters, ", "))
	}

	namespaceResource := schema.GroupVersionResource{
		Group:    params.ResourceGroup,
		Version:  params.ResourceVersion,
		Resource: strings.ToLower(params.ResourceKind),
	}

	o, err := c.Resource(namespaceResource).Namespace(params.Namespace).Get(ctx, params.Name, metav1.GetOptions{})
	if err != nil {
		return "", errors.Wrapf(err, "failed to get resource %s/%s of type %s.%s/%s", params.Namespace, params.Name, params.ResourceKind, params.ResourceGroup, params.ResourceVersion)
	}

	output := objectToDescribeOutput(o)
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

// NewResourceDescribeTool creates a new instance of the ResourceDescribeTool. It takes a context and a Configs object as parameters, builds Kubernetes clients for the provided configurations, and infers the tool using the description and invoke function. It returns the invokable tool or an error if any step fails.
func NewResourceDescribeTool(ctx context.Context, configs Configs) (tool.InvokableTool, error) {

	describeTool := &ResourceDescribeTool{
		knownClusters: configs.GetClusterNames(),
	}
	clients, err := BuildClientDynamics(configs, nil)
	if err != nil {
		return nil, err
	}
	describeTool.clients = clients

	// Infer tool
	t, err := utils.InferTool("kubernetes_describe_resource", resourceDescribeDescription, describeTool.Invoke)
	if err != nil {
		return nil, err
	}
	describeTool.InvokableTool = t

	return describeTool, nil
}
