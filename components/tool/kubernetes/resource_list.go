package kubernetes

import (
	"context"
	"regexp"
	"fmt"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/goccy/go-json"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/filter"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/marshal"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
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

// ResourceListOutput defines the structure of the output returned by the ResourceList function.
type ResourceListOutput struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Status    string `json:"status,omitempty"`
}

// ResourceListParams defines the parameters for the ResourceList function.
type ResourceListParams struct {
	Cluster        string              `json:"cluster" validate:"required" jsonschema:"(required) The cluster to connect to."`
	Namespace      string              `json:"namespace,omitempty" jsonschema:"(optional) The namespace to list resources from. If not provided, it will list resources from all namespaces."`
	LabelsSelector string              `json:"labelsSelector,omitempty" jsonschema:"(optional) The labels selector on string format, separated by comma. For example: 'app=nginx,env=prod'."`
	ApiVersion     string              `json:"apiVersion" validate:"required" jsonschema:"(required) The API version. For example, 'v1' or 'v1beta1'."`
	ApiGroup       string              `json:"apiGroup" validate:"required" jsonschema:"(required) The API group of the resource. For example, 'apps'."`
	Resource       string              `json:"resource" validate:"required" jsonschema:"(required) The resource type in plural lowercase. For example, 'deployments', 'pods'."`
	Filter         string              `json:"filter,omitempty" jsonschema:"(optional) A Go RE2 regex applied on each resource JSON output. Keep only the resources that match the pattern. Example: 'app-.*|web-.*'. Invalid regex returns an error."`
	Paginate       *ListParamsPaginate `json:"paginate,omitempty" jsonschema:"(optional) Pagination parameters."`
}

// ResourceListTool is a tool that lists all the resources in a specified Kubernetes cluster.
type ResourceListTool struct {
	*baseToolWithDynamic
	tool.InvokableTool
	output *ResourceListOutput
}

// IsMatch returns true if the JSON data matches the compiled regex filter. A nil filter matches everything.
func (t *ResourceListTool) IsMatch(o json.RawMessage, re *regexp.Regexp) bool {
	return filter.Match(o, re)
}

// ToJSON converts the given unstructured.Unstructured object to a JSON representation of ResourceListOutput.
func (h *ResourceListOutput) ToJSON(o *unstructured.Unstructured) json.RawMessage {
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

	return marshal.MustMarshal(output)
}

// Invoke executes the ResourceListTool with the given parameters.
func (t *ResourceListTool) Invoke(ctx context.Context, params *ResourceListParams) (result string, err error) {

	if params.Paginate != nil && params.Paginate.PageSize == 0 {
		params.Paginate.PageSize = 50
	}

	if err := validate.Struct(params); err != nil {
		return "", err
	}

	re, err := filter.Compile(params.Filter)
	if err != nil {
		return "", err
	}

	c, err := t.dynamicClient(params.Cluster)
	if err != nil {
		return "", err
	}

	var ls labels.Selector
	if len(params.LabelsSelector) > 0 {
		ls, err = labels.Parse(params.LabelsSelector)
		if err != nil {
			return "", errors.Wrap(err, "invalid labels selector")
		}
	}

	namespaceResource := toGVR(params.ApiGroup, params.ApiVersion, params.Resource)

	listOpts := v1.ListOptions{}
	if ls != nil {
		listOpts.LabelSelector = ls.String()
	}
	if params.Paginate != nil {
		listOpts.Limit = int64(params.Paginate.PageSize)
		listOpts.Continue = params.Paginate.PaginateToken
	}
	o, err := c.Resource(namespaceResource).Namespace(params.Namespace).List(ctx, listOpts)
	if err != nil {
		return "", errors.Wrapf(err, "failed to list resources on namespace %s of type %s.%s/%s", params.Namespace, params.Resource, params.ApiGroup, params.ApiVersion)
	}

	outputs := make([]json.RawMessage, 0, len(o.Items))
	for _, item := range o.Items {
		output := t.output.ToJSON(&item)
		if !t.IsMatch(output, re) {
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
		tokenData := marshal.MustMarshal(paginateToken{PaginateToken: continueToken})
		outputs = append(outputs, json.RawMessage(tokenData))
	}

	data, err := json.Marshal(outputs)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal output")
	}

	return string(data), nil
}

// NewResourceListTool creates a new instance of the ResourceListTool.
func NewResourceListTool(ctx context.Context, configs Configs) (tool.InvokableTool, error) {
	base, err := newBaseToolWithDynamic(ctx, configs)
	if err != nil {
		return nil, err
	}

	listTool := &ResourceListTool{
		baseToolWithDynamic: base,
		output:              &ResourceListOutput{},
	}

	// Infer tool
	t, err := utils.InferTool("kubernetes_resources_list", fmt.Sprintf("%s\n%s", resourceListDescription, listOutputGuidance), listTool.Invoke)
	if err != nil {
		return nil, err
	}
	listTool.InvokableTool = t

	return listTool, nil
}
