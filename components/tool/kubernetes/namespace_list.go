package kubernetes

import (
	"context"

	"github.com/cloudwego/eino/components/tool"
	"github.com/goccy/go-json"
	corev1 "k8s.io/api/core/v1"
)

const namespaceListDescription = `
** General Purpose **
It lists all the Namespaces in a specified Kubernetes cluster.

** Output **
It return a JSON array of objects, where each object represents a Namespace with the following fields:
- name: the name of the Namespace.
`

// NamespaceListOutput defines the structure of the output returned by the NamespaceList function. It represents a Namespace with its name.
type NamespaceListOutput struct {
	Name string `json:"name"`
}

// ToJson returns the JSON representation of the NamespaceListOutput struct. It marshals the struct into a JSON RawMessage and returns it. If there is an error during marshaling, it panics.
func (h *NamespaceListOutput) ToJson(o *corev1.Namespace) json.RawMessage {

	// Forge object
	output := CloneObject(h)
	output.Name = o.Name

	data, err := json.Marshal(output)
	if err != nil {
		panic(err)
	}
	return data
}

// NewNamespaceListTool creates a new instance of the NamespaceListTool. It takes a context and a Configs object as parameters, builds Kubernetes clients for the provided configurations, and infers the tool using the description and invoke function. It returns the invokable tool or an error if any step fails.
func NewNamespaceListTool(ctx context.Context, configs Configs) (tool.InvokableTool, error) {
	return NewListTool(ctx, configs, "kubernetes_list_namespaces", namespaceListDescription, &corev1.NamespaceList{}, &NamespaceListOutput{}, nil)
}
