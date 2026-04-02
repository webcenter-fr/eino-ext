package kubernetes

import (
	"context"

	"github.com/cloudwego/eino/components/tool"
	corev1 "k8s.io/api/core/v1"
)

const eventDescribeDescription = `
** General Purpose **
It gets the details of a specific Event in a specified Kubernetes cluster.

** Output **
It return a JSON object representing the kubernetes Event
`

// NewEventDescribeTool creates a new instance of the EventDescribeTool. It takes a context and a Configs object as parameters, builds Kubernetes clients for the provided configurations, and infers the tool using the description and invoke function. It returns the invokable tool or an error if any step fails.
func NewEventDescribeTool(ctx context.Context, configs Configs) (tool.InvokableTool, error) {
	return NewDescribeTool(ctx, configs, "kubernetes_describe_event", eventDescribeDescription, &corev1.Event{}, nil)
}
