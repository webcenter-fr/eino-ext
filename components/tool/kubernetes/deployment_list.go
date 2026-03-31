package kubernetes

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
	"github.com/goccy/go-json"
	"k8s.io/kubernetes/pkg/apis/apps"
)

const deploymentListDescription = `
** General Purpose **
It lists all the Deployments in a specified Kubernetes cluster.

** Output **
It return a JSON array of objects, where each object represents a Deployment with the following fields:
- name: the name of the deployment.
- namespace: the namespace of the deployment.
- status: the current status of the deployment (e.g., Running, Pending, Failed).
- expectedPod: the expected number of pods for the deployment.
- currentPod: the current number of pods for the deployment.
- readyPod: the number of ready pods for the deployment.
- image: the container image used by the deployment.
`

// DeploymentListOutput defines the structure of the output returned by the DeploymentList function. It represents a Deployment with its name and namespace.
type DeploymentListOutput struct {
	Name        string   `json:"name"`
	Namespace   string   `json:"namespace"`
	Status      string   `json:"status"`
	ExpectedPod int32    `json:"expectedPod"`
	CurrentPod  int32    `json:"currentPod"`
	ReadyPod    int32    `json:"readyPod"`
	Images      []string `json:"images"`
}

// ToJson returns the JSON representation of the DeploymentListOutput struct. It marshals the struct into a JSON RawMessage and returns it. If there is an error during marshaling, it panics.
func (h *DeploymentListOutput) ToJson(o *apps.Deployment) json.RawMessage {

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

// NewDeploymentListTool creates a new instance of the DeploymentListTool. It takes a context and a Configs object as parameters, builds Kubernetes clients for the provided configurations, and infers the tool using the description and invoke function. It returns the invokable tool or an error if any step fails.
func NewDeploymentListTool(ctx context.Context, configs Configs) (tool.InvokableTool, error) {
	return NewListTool(ctx, configs, "kubernetes_list_deployments", deploymentListDescription, &apps.DeploymentList{}, &DeploymentListOutput{}, nil)
}
