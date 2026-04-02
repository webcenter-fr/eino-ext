package kubernetes

import (
	"context"

	"github.com/cloudwego/eino/components/tool"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
)

const customResourceDefinitionDescribeDescription = `
** General Purpose **
It gets the details of a specific CustomResourceDefinition (CRD) in a specified Kubernetes cluster.

** Output **
It return a JSON object representing the kubernetes CustomResourceDefinition
`

// NewCustomResourceDefinitionDescribeTool creates a new instance of the CustomResourceDefinitionDescribeTool. It takes a context and a Configs object as parameters, builds Kubernetes clients for the provided configurations, and infers the tool using the description and invoke function. It returns the invokable tool or an error if any step fails.
func NewCustomResourceDefinitionDescribeTool(ctx context.Context, configs Configs) (tool.InvokableTool, error) {
	s := scheme.Scheme
	utilruntime.Must(apiextensionsv1.AddToScheme(s))

	return NewDescribeTool(ctx, configs, "kubernetes_describe_customresourcedefinition", customResourceDefinitionDescribeDescription, &apiextensionsv1.CustomResourceDefinition{}, s)
}
