package kubernetes

import (
	"context"

	"github.com/cloudwego/eino/components/tool"
	"github.com/goccy/go-json"
	corev1 "k8s.io/api/core/v1"
)

const podListDescription = `
** General Purpose **
It lists all the pods in a specified Kubernetes cluster.

** Output **
It return a JSON array of objects, where each object represents a pod with the following fields:
- name: the name of the pod.
- namespace: the namespace of the pod.
- status: the current status of the pod (e.g., Running, Pending, Failed).
- node: the name of the node where the pod is running.
- image: the container image used by the pod.
- ip: the IP address of the pod.
`

// PodListOutput defines the structure of the output returned by the PodList function. It represents a pod with its name, namespace, status, node, image, and IP address.
type PodListOutput struct {
	Name      string   `json:"name"`
	Namespace string   `json:"namespace"`
	Status    string   `json:"status"`
	Node      string   `json:"node"`
	Images    []string `json:"images"`
	IP        string   `json:"ip"`
}

// ToJson returns the JSON representation of the PodListOutput struct. It marshals the struct into a JSON RawMessage and returns it. If there is an error during marshaling, it panics.
func (h *PodListOutput) ToJson(o *corev1.Pod) json.RawMessage {

	// Forge object
	output := CloneObject(h)
	output.Name = o.Name
	output.Namespace = o.Namespace
	output.Status = string(o.Status.Phase)
	output.Node = o.Spec.NodeName
	output.IP = o.Status.PodIP
	images := make([]string, 0, len(o.Spec.Containers))
	for _, container := range o.Spec.Containers {
		images = append(images, container.Image)
	}
	output.Images = images

	data, err := json.Marshal(output)
	if err != nil {
		panic(err)
	}
	return data
}

// NewPodListTool creates a new instance of the PodListTool. It takes a context and a Configs object as parameters, builds Kubernetes clients for the provided configurations, and infers the tool using the description and invoke function. It returns the invokable tool or an error if any step fails.
func NewPodListTool(ctx context.Context, configs Configs) (tool.InvokableTool, error) {
	return NewListTool(ctx, configs, "kubernetes_list_pods", podListDescription, &corev1.PodList{}, &PodListOutput{}, nil)
}
