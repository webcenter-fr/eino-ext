package kubernetes

import (
	"context"
	"fmt"
	"reflect"
	"regexp"
	"strings"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/goccy/go-json"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/filter"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/marshal"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type paginateToken struct {
	PaginateToken string `json:"paginateToken"`
}

// OutputObject is the interface that list output types must implement to convert
// a Kubernetes resource into its JSON representation.
type OutputObject[resource client.Object] interface {
	ToJSON(resource) json.RawMessage
}

// ListParams defines the parameters for the List function, which lists all the resources in a specified Kubernetes cluster.
type ListParams struct {
	Cluster        string              `json:"cluster" validate:"required" jsonschema:"(required) The cluster to connect to."`
	Namespace      string              `json:"namespace,omitempty" jsonschema:"(optional) The namespace to list resources from. If not provided, it will list resources from all namespaces."`
	LabelsSelector string              `json:"labelsSelector,omitempty" jsonschema:"(optional) The labels selector on string format, separated by comma. For example: 'app=nginx,env=prod'."`
	Filter         string              `json:"filter,omitempty" jsonschema:"(optional) A Go RE2 regex applied on each resource JSON output. Keep only the resources that match the pattern. Example: 'app-.*|web-.*'. Invalid regex returns an error."`
	Paginate       *ListParamsPaginate `json:"paginate,omitempty" jsonschema:"(optional) Pagination parameters."`
}

type ListParamsPaginate struct {
	PageSize      int    `json:"pageSize,omitempty" validate:"omitempty,min=1,max=500" jsonschema:"(optional) The number of resources to return per page. Default is 50."`
	PaginateToken string `json:"paginateToken,omitempty" jsonschema:"(optional) The token to retrieve the next page of results. This token is returned in the response when there are more results available than can fit in a single page."`
}

// ListTool is a tool that lists all the resources in a specified Kubernetes cluster.
type ListTool[resourceList client.ObjectList, resource client.Object, outputObject OutputObject[resource]] struct {
	clients       map[string]client.Client
	tool.InvokableTool
	output        outputObject
	r             resourceList
	knownClusters []string
}

// IsMatch returns true if the JSON data matches the compiled regex filter. A nil filter matches everything.
func (t *ListTool[resourceList, resource, outputObject]) IsMatch(o json.RawMessage, re *regexp.Regexp) bool {
	if re == nil {
		return true
	}
	return re.Match(o)
}

// Invoke executes the ListTool with the given parameters.
func (t *ListTool[resourceList, resource, outputObject]) Invoke(ctx context.Context, params *ListParams) (result string, err error) {

	if params.Paginate != nil && params.Paginate.PageSize == 0 {
		params.Paginate.PageSize = 50
	}

	if err := validate.Struct(params); err != nil {
		return "", err
	}

	re, err := filter.Compile(params.Filter)
	if err != nil {
		return "", errors.Wrap(err, "error when compile regex")
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

	oList := reflect.New(reflect.TypeOf(t.r).Elem()).Interface().(resourceList)
	listOpts := &client.ListOptions{
		Namespace:     params.Namespace,
		LabelSelector: ls,
	}
	if params.Paginate != nil {
		listOpts.Limit = int64(params.Paginate.PageSize)
		listOpts.Continue = params.Paginate.PaginateToken
	}

	if err = c.List(ctx, oList, listOpts); err != nil {
		return "", errors.Wrap(err, "failed to list resources")
	}

	items, err := GetItems[resourceList, resource](oList)
	if err != nil {
		return "", errors.Wrap(err, "failed to get items from list")
	}
	outputs := make([]json.RawMessage, 0, len(items))
	for _, item := range items {
		output := t.output.ToJSON(item)
		if !t.IsMatch(output, re) {
			continue
		}
		outputs = append(outputs, output)
	}

	accessor, err := apimeta.ListAccessor(oList)
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

// NewListTool creates a new instance of the ListTool.
func NewListTool[resourceList client.ObjectList, resource client.Object, outputObject OutputObject[resource]](ctx context.Context, configs Configs, toolsName string, toolsDescription string, oList resourceList, output outputObject, s *runtime.Scheme) (tool.InvokableTool, error) {
	listTool := &ListTool[resourceList, resource, outputObject]{
		r:             oList,
		output:        output,
		knownClusters: configs.GetClusterNames(),
	}
	clients, err := BuildClients(configs, s)
	if err != nil {
		return nil, err
	}
	listTool.clients = clients

	// Infer tool
	t, err := utils.InferTool(toolsName, fmt.Sprintf("%s\n%s", toolsDescription, listOutputGuidance), listTool.Invoke)
	if err != nil {
		return nil, err
	}
	listTool.InvokableTool = t

	return listTool, nil
}

// GetItems extracts the Items slice from a Kubernetes ObjectList using reflection.
func GetItems[k8sObjectList client.ObjectList, k8sObject client.Object](o k8sObjectList) (items []k8sObject, err error) {
	if reflect.ValueOf(o).IsNil() {
		return nil, errors.New("resource list cannot be nil")
	}

	val := reflect.ValueOf(o).Elem()
	valueField := val.FieldByName("Items")
	if !valueField.IsValid() {
		return nil, errors.Errorf("resource list of type %T has no Items field", o)
	}

	items = make([]k8sObject, valueField.Len())
	for i := range items {
		items[i] = valueField.Index(i).Addr().Interface().(k8sObject)
	}

	return items, nil
}

// CloneObject creates an empty clone of the given object type using reflection.
func CloneObject[objectType comparable](o objectType) objectType {
	if reflect.TypeOf(o).Kind() != reflect.Pointer {
		panic("CloneObject works only with pointer types")
	}

	if reflect.ValueOf(o).IsNil() {
		panic("CloneObject: object cannot be nil")
	}

	return reflect.New(reflect.TypeOf(o).Elem()).Interface().(objectType)
}
