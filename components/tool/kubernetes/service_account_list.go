package kubernetes

import (
	"context"

	"github.com/cloudwego/eino/components/tool"
	"github.com/goccy/go-json"
	corev1 "k8s.io/api/core/v1"
)

const serviceAccountListDescription = `
** General Purpose **
It lists all the ServiceAccounts in a specified Kubernetes cluster.

** Output **
It return a JSON array of objects, where each object represents a ServiceAccount with the following fields:
- name: the name of the ServiceAccount.
- namespace: the namespace of the ServiceAccount.
`

// ServiceAccountListOutput defines the structure of the output returned by the ServiceAccountList function. It represents a ServiceAccount with its name and namespace.
type ServiceAccountListOutput struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
}

// ToJson returns the JSON representation of the ServiceAccountListOutput struct. It marshals the struct into a JSON RawMessage and returns it. If there is an error during marshaling, it panics.
func (h *ServiceAccountListOutput) ToJson(o *corev1.ServiceAccount) json.RawMessage {

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

// NewServiceAccountListTool creates a new instance of the ServiceAccountListTool. It takes a context and a Configs object as parameters, builds Kubernetes clients for the provided configurations, and infers the tool using the description and invoke function. It returns the invokable tool or an error if any step fails.
func NewServiceAccountListTool(ctx context.Context, configs Configs) (tool.InvokableTool, error) {
	return NewListTool(ctx, configs, "kubernetes_list_service_accounts", serviceAccountListDescription, &corev1.ServiceAccountList{}, &ServiceAccountListOutput{}, nil)
}
