package kubernetes

import (
	"context"

	"github.com/cloudwego/eino/components/tool"
	networkingv1 "k8s.io/api/networking/v1"
)

const ingressDescribeDescription = `
** General Purpose **
It gets the details of a specific ingress in a specified Kubernetes cluster.

** Output **
It return a JSON object representing the kubernetes ingress
`

// NewIngressDescribeTool creates a new instance of the IngressDescribeTool. It takes a context and a Configs object as parameters, builds Kubernetes clients for the provided configurations, and infers the tool using the description and invoke function. It returns the invokable tool or an error if any step fails.
func NewIngressDescribeTool(ctx context.Context, configs Configs) (tool.InvokableTool, error) {
	return NewDescribeTool(ctx, configs, "kubernetes_describe_ingress", ingressDescribeDescription, &networkingv1.Ingress{}, nil)
}
