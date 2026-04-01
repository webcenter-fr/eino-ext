package kubernetes

import (
	"context"

	"github.com/cloudwego/eino/components/tool"
	corev1 "k8s.io/api/core/v1"
)

const secretDescribeDescription = `
** General Purpose **
It gets the details of a specific Secret in a specified Kubernetes cluster.

** Output **
It return a JSON object representing the kubernetes Secret
`

// NewSecretDescribeTool creates a new instance of the SecretDescribeTool. It takes a context and a Configs object as parameters, builds Kubernetes clients for the provided configurations, and infers the tool using the description and invoke function. It returns the invokable tool or an error if any step fails.
func NewSecretDescribeTool(ctx context.Context, configs Configs) (tool.InvokableTool, error) {
	return NewDescribeTool(ctx, configs, "kubernetes_describe_secret", secretDescribeDescription, &corev1.Secret{}, nil)
}
