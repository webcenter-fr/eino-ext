package kubernetes

import (
	"context"

	"github.com/cloudwego/eino/components/tool"
	corev1 "k8s.io/api/core/v1"
)

const podDescribeDescription = `
** General Purpose **
It gets the details of a specific pod in a specified Kubernetes cluster.

** Output **
It return a JSON object representing the kubernetes pod
`

// NewPodDescribeTool creates a new instance of the PodDescribeTool. It takes a context and a Configs object as parameters, builds Kubernetes clients for the provided configurations, and infers the tool using the description and invoke function. It returns the invokable tool or an error if any step fails.
func NewPodDescribeTool(ctx context.Context, configs Configs) (tool.InvokableTool, error) {
	return NewDescribeTool(ctx, configs, "kubernetes_describe_pod", podDescribeDescription, &corev1.Pod{}, nil)
}
