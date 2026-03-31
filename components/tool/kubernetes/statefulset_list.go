package kubernetes

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
	"github.com/goccy/go-json"
	"k8s.io/kubernetes/pkg/apis/apps"
)

const statefulSetListDescription = `
** General Purpose **
It lists all the StatefulSets in a specified Kubernetes cluster.

** Output **
It return a JSON array of objects, where each object represents a StatefulSet with the following fields:
- name: the name of the StatefulSet.
- namespace: the namespace of the StatefulSet.
- status: the current status of the StatefulSet (e.g., Running, Pending, Failed).
- expectedPod: the expected number of pods for the StatefulSet.
- currentPod: the current number of pods for the StatefulSet.
- readyPod: the number of ready pods for the StatefulSet.
- image: the container image used by the StatefulSet.
`

// StatefulSetListOutput defines the structure of the output returned by the StatefulSetList function. It represents a StatefulSet with its name and namespace.
type StatefulSetListOutput struct {
	Name        string   `json:"name"`
	Namespace   string   `json:"namespace"`
	Status      string   `json:"status"`
	ExpectedPod int32    `json:"expectedPod"`
	CurrentPod  int32    `json:"currentPod"`
	ReadyPod    int32    `json:"readyPod"`
	Images      []string `json:"images"`
}

// ToJson returns the JSON representation of the StatefulSetListOutput struct. It marshals the struct into a JSON RawMessage and returns it. If there is an error during marshaling, it panics.
func (h *StatefulSetListOutput) ToJson(o *apps.StatefulSet) json.RawMessage {

	// Forge object
	output := CloneObject(h)
	output.Name = o.Name
	output.Namespace = o.Namespace

	output.ExpectedPod = o.Status.Replicas
	output.CurrentPod = o.Status.Replicas
	output.ReadyPod = o.Status.ReadyReplicas
	output.Status = fmt.Sprintf("%d/%d pods", o.Status.ReadyReplicas, o.Status.Replicas)
	images := make([]string, 0, len(o.Spec.Template.Spec.Containers))
	for _, container := range o.Spec.Template.Spec.Containers {
		images = append(images, container.Image)
	}
	output.Images = images

	data, err := json.Marshal(output)
	if err != nil {
		panic(err)
	}
	return data
}

// NewStatefulSetListTool creates a new instance of the StatefulSetListTool. It takes a context and a Configs object as parameters, builds Kubernetes clients for the provided configurations, and infers the tool using the description and invoke function. It returns the invokable tool or an error if any step fails.
func NewStatefulSetListTool(ctx context.Context, configs Configs) (tool.InvokableTool, error) {
	return NewListTool(ctx, configs, "kubernetes_list_statefulsets", statefulSetListDescription, &apps.StatefulSetList{}, &StatefulSetListOutput{}, nil)
}
