package kubernetes

import (
	"context"

	"github.com/cloudwego/eino/components/tool"
	corev1 "k8s.io/api/core/v1"
)

const serviceDescribeDescription = `
** General Purpose **
It gets the details of a specific Service in a specified Kubernetes cluster.

** Output **
It return a JSON object representing the kubernetes Service
`

// NewServiceDescribeTool creates a new instance of the ServiceDescribeTool. It takes a context and a Configs object as parameters, builds Kubernetes clients for the provided configurations, and infers the tool using the description and invoke function. It returns the invokable tool or an error if any step fails.
func NewServiceDescribeTool(ctx context.Context, configs Configs) (tool.InvokableTool, error) {
	return NewDescribeTool(ctx, configs, "kubernetes_describe_service", serviceDescribeDescription, &corev1.Service{}, nil)
}
