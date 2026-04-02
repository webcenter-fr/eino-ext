package kubernetes

import (
	"context"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/goccy/go-json"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
)

const customResourceDefinitionListDescription = `
** General Purpose **
It lists all the CustomResourceDefinitions (CRDs) in a specified Kubernetes cluster.

** Output **
It return a JSON array of objects, where each object represents a CustomResourceDefinition with the following fields:
- name: the name of the CustomResourceDefinition.
- namespace: the namespace of the CustomResourceDefinition.
- group: the API group of the CustomResourceDefinition.
- kind: the kind plural of the CustomResourceDefinition.
- versions: the list of versions of the CustomResourceDefinition.
`

// CustomResourceDefinitionListOutput defines the structure of the output returned by the CustomResourceDefinitionList function. It represents a CustomResourceDefinition with its name, namespace, group, and versions.
type CustomResourceDefinitionListOutput struct {
	Name      string   `json:"name"`
	Namespace string   `json:"namespace"`
	Group     string   `json:"group"`
	Kind      string   `json:"kind"`
	Versions  []string `json:"versions"`
}

// ToJson returns the JSON representation of the CustomResourceDefinitionListOutput struct. It marshals the struct into a JSON RawMessage and returns it. If there is an error during marshaling, it panics.
func (h *CustomResourceDefinitionListOutput) ToJson(o *apiextensionsv1.CustomResourceDefinition) json.RawMessage {

	// Forge object
	output := CloneObject(h)
	output.Name = o.Name
	output.Namespace = o.Namespace
	output.Group = o.Spec.Group
	output.Kind = strings.ToLower(o.Spec.Names.Plural)
	for _, version := range o.Spec.Versions {
		output.Versions = append(output.Versions, version.Name)
	}

	data, err := json.Marshal(output)
	if err != nil {
		panic(err)
	}
	return data
}

// NewCustomResourceDefinitionListTool creates a new instance of the CustomResourceDefinitionListTool. It takes a context and a Configs object as parameters, builds Kubernetes clients for the provided configurations, and infers the tool using the description and invoke function. It returns the invokable tool or an error if any step fails.
func NewCustomResourceDefinitionListTool(ctx context.Context, configs Configs) (tool.InvokableTool, error) {
	s := scheme.Scheme
	utilruntime.Must(apiextensionsv1.AddToScheme(s))

	return NewListTool(ctx, configs, "kubernetes_list_customresourcedefinitions", customResourceDefinitionListDescription, &apiextensionsv1.CustomResourceDefinitionList{}, &CustomResourceDefinitionListOutput{}, s)
}
