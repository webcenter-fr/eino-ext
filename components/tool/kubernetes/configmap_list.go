package kubernetes

import (
	"context"

	"github.com/cloudwego/eino/components/tool"
	"github.com/goccy/go-json"
	corev1 "k8s.io/api/core/v1"
)

const configMapListDescription = `
** General Purpose **
It lists all the ConfigMaps in a specified Kubernetes cluster.

** Output **
It return a JSON array of objects, where each object represents a ConfigMap with the following fields:
- name: the name of the ConfigMap.
- namespace: the namespace of the ConfigMap.
`

// ConfigMapListOutput defines the structure of the output returned by the ConfigMapList function. It represents a ConfigMap with its name and namespace.
type ConfigMapListOutput struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
}

// ToJson returns the JSON representation of the ConfigMapListOutput struct. It marshals the struct into a JSON RawMessage and returns it. If there is an error during marshaling, it panics.
func (h *ConfigMapListOutput) ToJson(o *corev1.ConfigMap) json.RawMessage {

	// Forge object
	output := CloneObject(h)
	output.Name = o.Name
	output.Namespace = o.Namespace

	data, err := json.Marshal(output)
	if err != nil {
		panic(err)
	}
	return data
}

// NewConfigMapListTool creates a new instance of the ConfigMapListTool. It takes a context and a Configs object as parameters, builds Kubernetes clients for the provided configurations, and infers the tool using the description and invoke function. It returns the invokable tool or an error if any step fails.
func NewConfigMapListTool(ctx context.Context, configs Configs) (tool.InvokableTool, error) {
	return NewListTool(ctx, configs, "kubernetes_list_configmaps", configMapListDescription, &corev1.ConfigMapList{}, &ConfigMapListOutput{}, nil)
}
