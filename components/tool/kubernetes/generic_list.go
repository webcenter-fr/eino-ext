package kubernetes

import (
	"context"
	"reflect"
	"regexp"
	"strings"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/go-playground/validator/v10"
	"github.com/goccy/go-json"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type OutputObject[resource client.Object] interface {
	ToJson(resource) json.RawMessage
}

// ListParams defines the parameters for the List function, which lists all the resources in a specified Kubernetes cluster. It includes the cluster name, an optional namespace to filter resources, and an optional regex pattern to further filter the output.
type ListParams struct {
	Cluster        string `json:"cluster" validate:"required" jsonschema:"(required) The cluster to connect to."`
	Namespace      string `json:"namespace,omitempty" jsonschema:"(optional) The namespace to list resources from. If not provided, it will list resources from all namespaces."`
	LabelsSelector string `json:"labelsSelector,omitempty" jsonschema:"(optional) The labels selector on string format, sepaeted by comma. For example: 'app=nginx,env=prod'."`
	Filter         string `json:"filter,omitempty" jsonschema:"(optional) A regex pattern to filter output. Keep only the resources that match the pattern. The filter is applied on each resource JSON output."`
}

// ListTool is a tool that lists all the resources in a specified Kubernetes cluster. It contains a map of Kubernetes clients for different clusters and implements the InvokableTool interface.
type ListTool[resourceList client.ObjectList, resource client.Object, outputObject OutputObject[resource]] struct {
	clients map[string]client.Client
	tool.InvokableTool
	output        outputObject
	r             resourceList
	knownClusters []string
}

// IsMatch checks if the Object matches the provided regex filter. If the filter is empty, it returns true.
func (t *ListTool[resourceList, resource, outputObject]) IsMatch(o json.RawMessage, filter string) bool {
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

// Invoke executes the ListTool with the given parameters. It validates the parameters, retrieves the appropriate Kubernetes client for the specified cluster, and lists the resources based on the provided namespace and label selector. The output is filtered using a regex pattern if provided, and the final result is returned as a JSON string.
func (t *ListTool[resourceList, resource, outputObject]) Invoke(ctx context.Context, params *ListParams) (result string, err error) {

	validator := validator.New()
	if err := validator.Struct(params); err != nil {
		return "", errors.Wrap(err, "invalid parameters for PodListTool")
	}

	c, ok := t.clients[params.Cluster]
	if !ok {
		return "", errors.Errorf("Kubernetes cluster not found: %s. Cluster must be one of: %s", params.Cluster, strings.Join(t.knownClusters, ", "))
	}

	oList := reflect.New(reflect.TypeOf(t.r).Elem()).Interface().(resourceList)
	var ls labels.Selector
	if len(params.LabelsSelector) > 0 {
		ls, err = labels.Parse(params.LabelsSelector)
		if err != nil {
			return "", errors.Wrap(err, "invalid labels selector")
		}
	}
	if err = c.List(context.Background(), oList, &client.ListOptions{Namespace: params.Namespace, LabelSelector: ls}); err != nil {
		return "", errors.Wrap(err, "failed to list resources")
	}

	items := GetItems[resourceList, resource](oList)

	outputs := make([]json.RawMessage, 0, len(items))
	for _, item := range items {
		output := t.output.ToJson(item)
		if !t.IsMatch(output, params.Filter) {
			continue
		}
		outputs = append(outputs, output)
	}

	data, err := json.Marshal(outputs)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal output")
	}

	return string(data), nil
}

// NewListTool creates a new instance of the ListTool. It takes a context and a Configs object as parameters, builds Kubernetes clients for the provided configurations, and infers the tool using the description and invoke function. It returns the invokable tool or an error if any step fails.
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
	t, err := utils.InferTool(toolsName, toolsDescription, listTool.Invoke)
	if err != nil {
		return nil, err
	}
	listTool.InvokableTool = t

	return listTool, nil
}

// GetItems permit to get items contend from ObjectList interface
func GetItems[k8sObjectList client.ObjectList, k8sObject client.Object](o k8sObjectList) (items []k8sObject) {
	if reflect.ValueOf(o).IsNil() {
		panic("ressource can't be nil")
	}

	val := reflect.ValueOf(o).Elem()
	valueField := val.FieldByName("Items")

	items = make([]k8sObject, valueField.Len())
	for i := range items {
		items[i] = valueField.Index(i).Addr().Interface().(k8sObject)
	}

	return items
}

// CloneObject creates empty clone of type
func CloneObject[objectType comparable](o objectType) objectType {
	if reflect.TypeOf(o).Kind() != reflect.Pointer {
		panic("CloneObject work only with pointer")
	}

	if reflect.ValueOf(o).IsNil() {
		panic("Object can't be nill")
	}

	return reflect.New(reflect.TypeOf(o).Elem()).Interface().(objectType)
}
