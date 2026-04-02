package kubernetes

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/go-playground/validator/v10"
	"github.com/goccy/go-json"
	"github.com/sirupsen/logrus"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

const resourceListDescription = `
** General Purpose **
It lists all the resources in a specified Kubernetes cluster.
You can use it when there are no dedicated tools for listing a specific Kubernetes resource type, or when you want to describe a custom resource.

** Output **
It return a JSON array of objects, where each object represents a resource with the following fields:
- name: the name of the resource.
- namespace: the namespace of the resource.
- status: the status of the resource, if applicable.
`

// ResourceListOutput defines the structure of the output returned by the ResourceList function. It represents a resource with its name, namespace, and status.
type ResourceListOutput struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Status    string `json:"status,omitempty"`
}

// ResourceListParams defines the parameters for the List function, which lists all the resources in a specified Kubernetes cluster. It includes the cluster name, an optional namespace to filter resources, and an optional regex pattern to further filter the output.
type ResourceListParams struct {
	Cluster              string              `json:"cluster" validate:"required" jsonschema:"(required) The cluster to connect to."`
	Namespace            string              `json:"namespace,omitempty" jsonschema:"(optional) The namespace to list resources from. If not provided, it will list resources from all namespaces."`
	LabelsSelector       string              `json:"labelsSelector,omitempty" jsonschema:"(optional) The labels selector on string format, sepaeted by comma. For example: 'app=nginx,env=prod'."`
	ResourceGroupVersion string              `json:"resourceGroupVersion" validate:"required" jsonschema:"(required) The group and version of the resource, in the format of 'group/version'. For example, 'apps/v1'."`
	ResourceKind         string              `json:"resourceKind" validate:"required" jsonschema:"(required) The kind of the resource. For example, 'Deployment'."`
	Filter               string              `json:"filter,omitempty" jsonschema:"(optional) A regex pattern to filter output. Keep only the resources that match the pattern. The filter is applied on each resource JSON output."`
	Paginate             *ListParamsPaginate `json:"paginate,omitempty" jsonschema:"(optional) Pagination parameters."`
}

// ResourceListTool is a tool that lists all the resources in a specified Kubernetes cluster. It contains a map of Kubernetes clients for different clusters and implements the InvokableTool interface.
type ResourceListTool struct {
	clients map[string]dynamic.Interface
	tool.InvokableTool
	knownClusters []string
	output        *ResourceListOutput
}

// IsMatch checks if the Object matches the provided regex filter. If the filter is empty, it returns true.
func (t *ResourceListTool) IsMatch(o json.RawMessage, filter string) bool {
	if filter == "" {
		return true
	}

	r := regexp.MustCompile(filter)
	if r.Match(o) {
		logrus.Debugf("Output %s filtered by regex: %s", string(o), filter)
		return true
	}
	return false

}

// ToJson converts the given unstructured.Unstructured object to a JSON representation of ResourceListOutput, which includes the resource's name, namespace, and status (if applicable).
func (h *ResourceListOutput) ToJson(o *unstructured.Unstructured) json.RawMessage {
	if o == nil {
		return nil
	}

	output := CloneObject(h)
	output.Name = o.GetName()
	output.Namespace = o.GetNamespace()

	if o.Object["status"] != nil && o.Object["status"].(map[string]any)["conditions"] != nil {
		for _, cond := range o.Object["status"].(map[string]any)["conditions"].([]any) {
			if cond.(map[string]any)["type"] == "Ready" {
				if cond.(map[string]any)["status"] == "True" {
					output.Status = "Ready"
				} else {
					output.Status = "Not Ready"
				}
				break
			}
		}
	}

	data, err := json.Marshal(output)
	if err != nil {
		panic(err)
	}
	return data

}

// Invoke executes the ResourceListTool with the given parameters. It validates the parameters, retrieves the appropriate Kubernetes client for the specified cluster, and lists the resources based on the provided namespace and label selector. The output is filtered using a regex pattern if provided, and the final result is returned as a JSON string.
func (t *ResourceListTool) Invoke(ctx context.Context, params *ResourceListParams) (result string, err error) {

	if params.Paginate != nil && params.Paginate.PageSize == 0 {
		params.Paginate.PageSize = 500
	}

	validator := validator.New()
	if err := validator.Struct(params); err != nil {
		return "", errors.Wrap(err, "invalid parameters for PodListTool")
	}

	c, ok := t.clients[params.Cluster]
	if !ok {
		return "", errors.Errorf("Kubernetes cluster not found: %s. Cluster must be one of: %s", params.Cluster, strings.Join(t.knownClusters, ", "))
	}

	var ls labels.Selector
	if len(params.LabelsSelector) > 0 {
		ls, err = labels.Parse(params.LabelsSelector)
		if err != nil {
			return "", errors.Wrap(err, "invalid labels selector")
		}
	}

	gv := strings.Split(params.ResourceGroupVersion, "/")
	if len(gv) != 2 {
		return "", errors.Errorf("invalid resourceGroupVersion: %s. It should be in the format of 'group/version'. For example, 'apps/v1'.", params.ResourceGroupVersion)
	}
	namespaceResource := schema.GroupVersionResource{
		Group:    gv[0],
		Version:  gv[1],
		Resource: params.ResourceKind,
	}

	listOpts := v1.ListOptions{
		LabelSelector: ls.String(),
	}
	if params.Paginate != nil {
		listOpts.Limit = int64(params.Paginate.PageSize)
		listOpts.Continue = params.Paginate.PaginateToken
	}
	o, err := c.Resource(namespaceResource).Namespace(params.Namespace).List(ctx, listOpts)
	if err != nil {
		return "", errors.Wrapf(err, "failed to list resources on namespace %s of type %s", params.Namespace, params.ResourceGroupVersionKind)
	}

	outputs := make([]json.RawMessage, 0, len(o.Items))
	for _, item := range o.Items {
		output := t.output.ToJson(&item)
		if !t.IsMatch(output, params.Filter) {
			continue
		}
		outputs = append(outputs, output)
	}

	accessor, err := apimeta.ListAccessor(o)
	if err != nil {
		return "", errors.Wrap(err, "failed to get list accessor")
	}
	continueToken := accessor.GetContinue()
	if continueToken != "" {
		outputs = append(outputs, json.RawMessage(fmt.Sprintf(`{"paginateToken": "%s"}`, continueToken)))
	}

	data, err := json.Marshal(outputs)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal output")
	}

	return string(data), nil
}

// NewResourceListTool creates a new instance of the ResourceListTool. It takes a context and a Configs object as parameters, builds Kubernetes clients for the provided configurations, and infers the tool using the description and invoke function. It returns the invokable tool or an error if any step fails.
func NewResourceListTool(ctx context.Context, configs Configs) (tool.InvokableTool, error) {
	listTool := &ResourceListTool{
		knownClusters: configs.GetClusterNames(),
	}
	clients, err := BuildClientDynamics(configs, nil)
	if err != nil {
		return nil, err
	}
	listTool.clients = clients

	// Infer tool
	t, err := utils.InferTool("kubernetes_resources_list", resourceListDescription, listTool.Invoke)
	if err != nil {
		return nil, err
	}
	listTool.InvokableTool = t

	return listTool, nil
}
