package kubernetes

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/goccy/go-json"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// PodDescribeParams defines the parameters for the PodDescribe function, which gets the details of a specific pod in a specified Kubernetes cluster. It includes the cluster name, namespace, and pod name.
type DescribeParams struct {
	Cluster             string   `json:"cluster" validate:"required" jsonschema:"(required) The cluster to connect to."`
	Namespace           string   `json:"namespace" validate:"required" jsonschema:"(required) The namespace of the resource."`
	Name                string   `json:"name" validate:"required" jsonschema:"(required) The resource name."`
	ExcludeFieldsOutput []string `json:"excludeFieldsOutput,omitempty" validate:"omitempty,dive,oneof=metadata spec status data" jsonschema:"(optional) The fields to exclude from the output. Default to no exclusion. You can set 'metadata', 'spec', 'status', and 'data'."`
}

// DescribeOutput defines the structure of the output returned by the Describe function. It represents a resource with its metadata, spec, and status.
type DescribeOutput struct {
	metav1.TypeMeta `json:",inline"`
	Metadata        *metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec            any                `json:"spec,omitempty"`
	Status          any                `json:"status,omitempty"`
	Data            any                `json:"data,omitempty"`
}

// DescribeTool is a tool that gets the details of a specific resource in a specified Kubernetes cluster. It contains a map of Kubernetes clients for different clusters and implements the InvokableTool interface.
type DescribeTool[resource client.Object] struct {
	clients map[string]client.Client
	tool.InvokableTool
	r             resource
	knownClusters []string
}

// Invoke executes the DescribeTool with the given parameters. It validates the parameters, retrieves the appropriate Kubernetes client for the specified cluster, and lists the resources based on the provided namespace and label selector. The output is filtered using a regex pattern if provided, and the final result is returned as a JSON string.
func (t *DescribeTool[resource]) Invoke(ctx context.Context, params *DescribeParams) (result string, err error) {

	if err := validate.Struct(params); err != nil {
		return "", err
	}

	c, ok := t.clients[params.Cluster]
	if !ok {
		return "", errors.Errorf("Kubernetes cluster not found: %s. Cluster must be one of: %s", params.Cluster, strings.Join(t.knownClusters, ", "))
	}

	o := reflect.New(reflect.TypeOf(t.r).Elem()).Interface().(client.Object)
	if err = c.Get(ctx, client.ObjectKey{Namespace: params.Namespace, Name: params.Name}, o); err != nil {
		return "", errors.Wrap(err, "failed to get resource")
	}
	kind, err := c.GroupVersionKindFor(o)
	if err != nil {
		return "", errors.Wrap(err, "failed to get GroupVersionKind for resource")
	}
	if err := SetObjectTypeMeta(o, metav1.TypeMeta{
		Kind:       kind.Kind,
		APIVersion: kind.Version,
	}); err != nil {
		return "", errors.Wrap(err, "failed to set TypeMeta")
	}

	// Special filter for secret content.
	if resource, ok := any(o).(*corev1.Secret); ok {
		for k := range resource.Data {
			resource.Data[k] = []byte("REDACTED")
		}
	}

	output, err := objectToDescribeOutput(o)
	if err != nil {
		return "", errors.Wrap(err, "failed to convert object to describe output")
	}

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

// GetObjectStatus retrieves the status field from a Kubernetes resource object using reflection.
// It returns (nil, nil) when the resource has no Status field.
func GetObjectStatus(r client.Object) (any, error) {
	rt := reflect.TypeOf(r)
	if rt.Kind() != reflect.Ptr {
		return nil, errors.Errorf("resource must be pointer, got %T", rt)
	}
	rv := reflect.ValueOf(r).Elem()
	om := rv.FieldByName("Status")
	if !om.IsValid() {
		return nil, nil
	}
	return om.Interface(), nil
}

// GetObjectSpec retrieves the Spec field from a Kubernetes resource object.
// It returns (nil, nil) when the resource has no Spec field.
func GetObjectSpec(r client.Object) (any, error) {
	rt := reflect.TypeOf(r)
	if rt.Kind() != reflect.Ptr {
		return nil, errors.Errorf("resource must be pointer, got %T", rt)
	}
	rv := reflect.ValueOf(r).Elem()
	om := rv.FieldByName("Spec")
	if !om.IsValid() {
		return nil, nil
	}
	return om.Interface(), nil
}

// GetDataSpec retrieves the Data field from a Kubernetes resource object.
// It returns (nil, nil) when the resource has no Data field.
func GetDataSpec(r client.Object) (any, error) {
	rt := reflect.TypeOf(r)
	if rt.Kind() != reflect.Ptr {
		return nil, errors.Errorf("resource must be pointer, got %T", rt)
	}
	rv := reflect.ValueOf(r).Elem()
	om := rv.FieldByName("Data")
	if !om.IsValid() {
		return nil, nil
	}
	return om.Interface(), nil
}

func SetObjectTypeMeta(o client.Object, typeMeta metav1.TypeMeta) error {
	rt := reflect.TypeOf(o)
	if rt.Kind() != reflect.Ptr {
		return errors.Errorf("resource must be pointer, got %T", rt)
	}
	rv := reflect.ValueOf(o).Elem()
	om := rv.FieldByName("TypeMeta")
	if !om.IsValid() {
		return errors.Errorf("resource of type %T has no TypeMeta field", o)
	}
	om.Set(reflect.ValueOf(typeMeta))
	return nil
}

// NewDescribePodTool creates a new instance of the DescribePodTool. It takes a context and a Configs object as parameters, builds Kubernetes clients for the provided configurations, and infers the tool using the description and invoke function. It returns the invokable tool or an error if any step fails.
func NewDescribeTool[resource client.Object](ctx context.Context, configs Configs, toolsName string, toolsDescription string, o resource, s *runtime.Scheme) (tool.InvokableTool, error) {

	describeTool := &DescribeTool[resource]{
		r:             o,
		knownClusters: configs.GetClusterNames(),
	}
	clients, err := BuildClients(configs, s)
	if err != nil {
		return nil, err
	}
	describeTool.clients = clients

	// Infer tool
	t, err := utils.InferTool(toolsName, fmt.Sprintf("%s\n%s", toolsDescription, describeOutputGuidance), describeTool.Invoke)
	if err != nil {
		return nil, err
	}
	describeTool.InvokableTool = t

	return describeTool, nil
}

// objectToDescribeOutput converts a Kubernetes resource object to a DescribeOutput struct. It extracts the TypeMeta, Metadata, Spec, and Status from the resource object and populates the DescribeOutput struct accordingly. This function is used to format the output of the DescribeTool in a consistent way.
func objectToDescribeOutput[resource client.Object](r resource) (DescribeOutput, error) {
	if reflect.ValueOf(r).IsNil() {
		return reflect.Zero(reflect.TypeOf(DescribeOutput{})).Interface().(DescribeOutput), nil
	}
	spec, err := GetObjectSpec(r)
	if err != nil {
		return DescribeOutput{}, err
	}
	status, err := GetObjectStatus(r)
	if err != nil {
		return DescribeOutput{}, err
	}
	data, err := GetDataSpec(r)
	if err != nil {
		return DescribeOutput{}, err
	}
	return DescribeOutput{
		TypeMeta: metav1.TypeMeta{
			Kind:       r.GetObjectKind().GroupVersionKind().Kind,
			APIVersion: r.GetObjectKind().GroupVersionKind().Version,
		},
		Metadata: &metav1.ObjectMeta{
			Name:              r.GetName(),
			Namespace:         r.GetNamespace(),
			Labels:            r.GetLabels(),
			Annotations:       r.GetAnnotations(),
			OwnerReferences:   r.GetOwnerReferences(),
			ResourceVersion:   r.GetResourceVersion(),
			CreationTimestamp: r.GetCreationTimestamp(),
			DeletionTimestamp: r.GetDeletionTimestamp(),
		},
		Spec:   spec,
		Status: status,
		Data:   data,
	}, nil
}
