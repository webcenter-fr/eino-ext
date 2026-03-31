package kubernetes

import (
	"context"

	"github.com/cloudwego/eino/components/tool"
	"github.com/goccy/go-json"
	corev1 "k8s.io/api/core/v1"
)

const nodeListDescription = `
** General Purpose **
It lists all the Nodes in a specified Kubernetes cluster.

** Output **
It return a JSON array of objects, where each object represents a Node with the following fields:
- name: the name of the Node.
- version: the Kubernetes version of the Node.
- internalIP: the internal IP address of the Node.
- externalIP: the external IP address of the Node.
- OS: the operating system of the Node.
- status: the current status of the Node (e.g., Ready, NotReady).
`

// NodeListOutput defines the structure of the output returned by the NodeList function. It represents a Node with its name, version, IP addresses, OS, and status.
type NodeListOutput struct {
	Name       string `json:"name"`
	Version    string `json:"version"`
	InternalIP string `json:"internalIP"`
	ExternalIP string `json:"externalIP"`
	OS         string `json:"OS"`
	Status     string `json:"status"`
}

// ToJson returns the JSON representation of the NodeListOutput struct. It marshals the struct into a JSON RawMessage and returns it. If there is an error during marshaling, it panics.
func (h *NodeListOutput) ToJson(o *corev1.Node) json.RawMessage {

	// Forge object
	output := CloneObject(h)
	output.Name = o.Name
	output.Version = o.Status.NodeInfo.KubeletVersion
	output.OS = o.Status.NodeInfo.OperatingSystem

	for _, address := range o.Status.Addresses {
		switch address.Type {
		case corev1.NodeInternalIP:
			output.InternalIP = address.Address
		case corev1.NodeExternalIP:
			output.ExternalIP = address.Address
		}
	}

	for _, condition := range o.Status.Conditions {
		if condition.Type == corev1.NodeReady {
			if condition.Status == corev1.ConditionTrue {
				output.Status = "Ready"
			} else {
				output.Status = "NotReady"
			}
			break
		}
	}

	data, err := json.Marshal(output)
	if err != nil {
		panic(err)
	}
	return data
}

// NewNodeListTool creates a new instance of the NodeListTool. It takes a context and a Configs object as parameters, builds Kubernetes clients for the provided configurations, and infers the tool using the description and invoke function. It returns the invokable tool or an error if any step fails.
func NewNodeListTool(ctx context.Context, configs Configs) (tool.InvokableTool, error) {
	return NewListTool(ctx, configs, "kubernetes_list_nodes", nodeListDescription, &corev1.NodeList{}, &NodeListOutput{}, nil)
}
