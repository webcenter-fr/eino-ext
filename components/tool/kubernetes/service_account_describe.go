package kubernetes

import (
	"context"

	"github.com/cloudwego/eino/components/tool"
	corev1 "k8s.io/api/core/v1"
)

const serviceAccountDescribeDescription = `
** General Purpose **
It gets the details of a specific ServiceAccount in a specified Kubernetes cluster.

** Output **
It return a JSON object representing the kubernetes ServiceAccount
`

// NewServiceAccountDescribeTool creates a new instance of the ServiceAccountDescribeTool. It takes a context and a Configs object as parameters, builds Kubernetes clients for the provided configurations, and infers the tool using the description and invoke function. It returns the invokable tool or an error if any step fails.
func NewServiceAccountDescribeTool(ctx context.Context, configs Configs) (tool.InvokableTool, error) {
	return NewDescribeTool(ctx, configs, "kubernetes_describe_service_account", serviceAccountDescribeDescription, &corev1.ServiceAccount{}, nil)
}
