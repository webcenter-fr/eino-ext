package kubernetes

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
	"github.com/goccy/go-json"
	"k8s.io/kubernetes/pkg/apis/apps"
)

const daemonSetListDescription = `
** General Purpose **
It lists all the DaemonSets in a specified Kubernetes cluster.

** Output **
It return a JSON array of objects, where each object represents a DaemonSet with the following fields:
- name: the name of the pod.
- namespace: the namespace of the pod.
- status: the current status of the pod (e.g., Running, Pending, Failed).
- expectedPod: the expected number of pods for the DaemonSet.
- currentPod: the current number of pods for the DaemonSet.
- readyPod: the number of ready pods for the DaemonSet.
- image: the container image used by the DaemonSet.
`

// DaemonSetListOutput defines the structure of the output returned by the DaemonSetList function. It represents a DaemonSet with its name and namespace.
type DaemonSetListOutput struct {
	Name        string   `json:"name"`
	Namespace   string   `json:"namespace"`
	Status      string   `json:"status"`
	ExpectedPod int32    `json:"expectedPod"`
	CurrentPod  int32    `json:"currentPod"`
	ReadyPod    int32    `json:"readyPod"`
	Images      []string `json:"images"`
}

// ToJson returns the JSON representation of the DaemonSetListOutput struct. It marshals the struct into a JSON RawMessage and returns it. If there is an error during marshaling, it panics.
func (h *DaemonSetListOutput) ToJson(o *apps.DaemonSet) json.RawMessage {

	// Forge object
	output := CloneObject(h)
	output.Name = o.Name
	output.Namespace = o.Namespace

	output.ExpectedPod = o.Status.DesiredNumberScheduled
	output.CurrentPod = o.Status.CurrentNumberScheduled
	output.ReadyPod = o.Status.NumberReady
	output.Status = fmt.Sprintf("%d/%d pods", o.Status.NumberReady, o.Status.DesiredNumberScheduled)
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

// NewDaemonSetListTool creates a new instance of the DaemonSetListTool. It takes a context and a Configs object as parameters, builds Kubernetes clients for the provided configurations, and infers the tool using the description and invoke function. It returns the invokable tool or an error if any step fails.
func NewDaemonSetListTool(ctx context.Context, configs Configs) (tool.InvokableTool, error) {
	return NewListTool(ctx, configs, "kubernetes_list_daemonsets", daemonSetListDescription, &apps.DaemonSetList{}, &DaemonSetListOutput{}, nil)
}
