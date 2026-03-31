package kubernetes

import (
	"context"

	"github.com/cloudwego/eino/components/tool"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/kubernetes/pkg/apis/apps"
)

const statefulsetDescribeDescription = `
** General Purpose **
It gets the details of a specific statefulset in a specified Kubernetes cluster.

** Output **
It return a JSON object representing the kubernetes statefulset
`

// NewStatefulsetDescribeTool creates a new instance of the StatefulsetDescribeTool. It takes a context and a Configs object as parameters, builds Kubernetes clients for the provided configurations, and infers the tool using the description and invoke function. It returns the invokable tool or an error if any step fails.
func NewStatefulsetDescribeTool(ctx context.Context, configs Configs) (tool.InvokableTool, error) {
	return NewDescribeTool(ctx, configs, "kubernetes_describe_statefulset", statefulsetDescribeDescription, &apps.StatefulSet{}, runtime.NewScheme())
}
