package kubernetes

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
	"github.com/goccy/go-json"
	corev1 "k8s.io/api/core/v1"
)

const serviceListDescription = `
** General Purpose **
It lists all the Services in a specified Kubernetes cluster.

** Output **
It return a JSON array of objects, where each object represents a Service with the following fields:
- name: the name of the Service.
- namespace: the namespace of the Service.
- type: the type of the Service (e.g., ClusterIP, NodePort, LoadBalancer).
- ips: the list of IPs associated with the Service.
- ports: the list of ports exposed by the Service.
`

// ServiceListOutput defines the structure of the output returned by the ServiceList function. It represents a Service with its name, namespace, type, IPs, and ports.
type ServiceListOutput struct {
	Name      string   `json:"name"`
	Namespace string   `json:"namespace"`
	Type      string   `json:"type"`
	IPs       []string `json:"ips"`
	Ports     []string `json:"ports"`
}

// ToJson returns the JSON representation of the ServiceListOutput struct. It marshals the struct into a JSON RawMessage and returns it. If there is an error during marshaling, it panics.
func (h *ServiceListOutput) ToJson(o *corev1.Service) json.RawMessage {

	// Forge object
	output := CloneObject(h)
	output.Name = o.Name
	output.Namespace = o.Namespace
	output.Type = string(o.Spec.Type)

	switch o.Spec.Type {
	case corev1.ServiceTypeClusterIP:
		if o.Spec.ClusterIP != "" {
			output.IPs = append(output.IPs, o.Spec.ClusterIP)
		}
		for _, port := range o.Spec.Ports {
			output.Ports = append(output.Ports, fmt.Sprintf("%d", port.Port))
		}
	case corev1.ServiceTypeNodePort:
		if o.Spec.ClusterIP != "" {
			output.IPs = append(output.IPs, o.Spec.ClusterIP)
		}
		for _, port := range o.Spec.Ports {
			output.Ports = append(output.Ports, fmt.Sprintf("%d", port.NodePort))
		}
	case corev1.ServiceTypeLoadBalancer:
		for _, ingress := range o.Status.LoadBalancer.Ingress {
			if ingress.IP != "" {
				output.IPs = append(output.IPs, ingress.IP)
			} else if ingress.Hostname != "" {
				output.IPs = append(output.IPs, ingress.Hostname)
			}
		}
	}

	data, err := json.Marshal(output)
	if err != nil {
		panic(err)
	}
	return data
}

// NewServiceListTool creates a new instance of the ServiceListTool. It takes a context and a Configs object as parameters, builds Kubernetes clients for the provided configurations, and infers the tool using the description and invoke function. It returns the invokable tool or an error if any step fails.
func NewServiceListTool(ctx context.Context, configs Configs) (tool.InvokableTool, error) {
	return NewListTool(ctx, configs, "kubernetes_list_services", serviceListDescription, &corev1.ServiceList{}, &ServiceListOutput{}, nil)
}
