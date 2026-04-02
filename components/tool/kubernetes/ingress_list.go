package kubernetes

import (
	"context"

	"github.com/cloudwego/eino/components/tool"
	"github.com/goccy/go-json"
	networkingv1 "k8s.io/api/networking/v1"
)

const ingressListDescription = `
** General Purpose **
It lists all the Ingresses in a specified Kubernetes cluster.

** Output **
It return a JSON array of objects, where each object represents an Ingress with the following fields:
- name: the name of the ingress.
- namespace: the namespace of the ingress.
- hosts: the list of hosts defined in the ingress rules.
- tls: the list of TLS configurations for the ingress.
`

// IngressListOutput defines the structure of the output returned by the IngressList function. It represents an Ingress with its name and namespace.
type IngressListOutput struct {
	Name      string   `json:"name"`
	Namespace string   `json:"namespace"`
	Hosts     []string `json:"hosts"`
	TLS       []string `json:"tls"`
}

// ToJson returns the JSON representation of the IngressListOutput struct. It marshals the struct into a JSON RawMessage and returns it. If there is an error during marshaling, it panics.
func (h *IngressListOutput) ToJson(o *networkingv1.Ingress) json.RawMessage {

	// Forge object
	output := CloneObject(h)
	output.Name = o.Name
	output.Namespace = o.Namespace

	hosts := make([]string, 0, len(o.Spec.Rules))
	for _, rule := range o.Spec.Rules {
		hosts = append(hosts, rule.Host)
	}
	output.Hosts = hosts

	tls := make([]string, 0, len(o.Spec.TLS))
	for _, t := range o.Spec.TLS {
		tls = append(tls, t.SecretName)
	}
	output.TLS = tls

	data, err := json.Marshal(output)
	if err != nil {
		panic(err)
	}
	return data
}

// NewIngressListTool creates a new instance of the IngressListTool. It takes a context and a Configs object as parameters, builds Kubernetes clients for the provided configurations, and infers the tool using the description and invoke function. It returns the invokable tool or an error if any step fails.
func NewIngressListTool(ctx context.Context, configs Configs) (tool.InvokableTool, error) {
	return NewListTool(ctx, configs, "kubernetes_list_ingresses", ingressListDescription, &networkingv1.IngressList{}, &IngressListOutput{}, nil)
}
