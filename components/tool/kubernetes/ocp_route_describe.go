package kubernetes

import (
	"context"

	"github.com/cloudwego/eino/components/tool"
	routev1 "github.com/openshift/api/route/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
)

const ocpRouteDescribeDescription = `
** General Purpose **
It gets the details of a specific OCP Route in a specified Kubernetes cluster.

** Output **
It return a JSON object representing the kubernetes OCP Route
`

// NewOcpRouteDescribeTool creates a new instance of the OcpRouteDescribeTool. It takes a context and a Configs object as parameters, builds Kubernetes clients for the provided configurations, and infers the tool using the description and invoke function. It returns the invokable tool or an error if any step fails.
func NewOcpRouteDescribeTool(ctx context.Context, configs Configs) (tool.InvokableTool, error) {
	s := scheme.Scheme
	utilruntime.Must(routev1.AddToScheme(s))

	return NewDescribeTool(ctx, configs, "kubernetes_describe_ocp_route", ocpRouteDescribeDescription, &routev1.Route{}, s)
}
