package kubernetes

import (
	"context"

	"github.com/cloudwego/eino/components/tool"
	"github.com/goccy/go-json"
	corev1 "k8s.io/api/core/v1"
)

const secretListDescription = `
** General Purpose **
It lists all the Secrets in a specified Kubernetes cluster.

** Output **
It return a JSON array of objects, where each object represents a Secret with the following fields:
- name: the name of the Secret.
- namespace: the namespace of the Secret.
- type: the type of the Secret.
`

// SecretListOutput defines the structure of the output returned by the SecretList function. It represents a Secret with its name and namespace.
type SecretListOutput struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Type      string `json:"type"`
}

// ToJson returns the JSON representation of the SecretListOutput struct. It marshals the struct into a JSON RawMessage and returns it. If there is an error during marshaling, it panics.
func (h *SecretListOutput) ToJson(o *corev1.Secret) json.RawMessage {

	// Forge object
	output := CloneObject(h)
	output.Name = o.Name
	output.Namespace = o.Namespace
	output.Type = string(o.Type)

	data, err := json.Marshal(output)
	if err != nil {
		panic(err)
	}
	return data
}

// NewSecretListTool creates a new instance of the SecretListTool. It takes a context and a Configs object as parameters, builds Kubernetes clients for the provided configurations, and infers the tool using the description and invoke function. It returns the invokable tool or an error if any step fails.
func NewSecretListTool(ctx context.Context, configs Configs) (tool.InvokableTool, error) {
	return NewListTool(ctx, configs, "kubernetes_list_secrets", secretListDescription, &corev1.SecretList{}, &SecretListOutput{}, nil)
}
