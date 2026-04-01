package kubernetes

import (
	"context"

	"github.com/cloudwego/eino/components/tool"
	"k8s.io/kubernetes/pkg/apis/apps"
)

const deploymentDescribeDescription = `
** General Purpose **
It gets the details of a specific deployment in a specified Kubernetes cluster.

** Output **
It return a JSON object representing the kubernetes deployment
`

// NewDeploymentDescribeTool creates a new instance of the DeploymentDescribeTool. It takes a context and a Configs object as parameters, builds Kubernetes clients for the provided configurations, and infers the tool using the description and invoke function. It returns the invokable tool or an error if any step fails.
func NewDeploymentDescribeTool(ctx context.Context, configs Configs) (tool.InvokableTool, error) {
	return NewDescribeTool(ctx, configs, "kubernetes_describe_deployment", deploymentDescribeDescription, &apps.Deployment{}, nil)
}
