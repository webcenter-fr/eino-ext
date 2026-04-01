package kubernetes

import (
	"context"

	"github.com/cloudwego/eino/components/tool"
	olmv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
)

const olmCSVDescribeDescription = `
** General Purpose **
It gets the details of a specific OLMClusterServiceVersion in a specified Kubernetes cluster.

** Output **
It return a JSON object representing the kubernetes OLMClusterServiceVersion
`

// NewOLMClusterServiceVersionDescribeTool creates a new instance of the OLMClusterServiceVersionDescribeTool. It takes a context and a Configs object as parameters, builds Kubernetes clients for the provided configurations, and infers the tool using the description and invoke function. It returns the invokable tool or an error if any step fails.
func NewOLMClusterServiceVersionDescribeTool(ctx context.Context, configs Configs) (tool.InvokableTool, error) {
	s := scheme.Scheme
	utilruntime.Must(olmv1alpha1.AddToScheme(s))

	return NewDescribeTool(ctx, configs, "kubernetes_describe_olm_cluster_service_version", olmCSVDescribeDescription, &olmv1alpha1.ClusterServiceVersion{}, s)
}
