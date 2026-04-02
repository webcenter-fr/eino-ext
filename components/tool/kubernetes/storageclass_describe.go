package kubernetes

import (
	"context"

	"github.com/cloudwego/eino/components/tool"
	storagev1 "k8s.io/api/storage/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
)

const storageclassDescribeDescription = `
** General Purpose **
It gets the details of a specific StorageClass in a specified Kubernetes cluster.

** Output **
It return a JSON object representing the kubernetes StorageClass
`

// NewStorageClassDescribeTool creates a new instance of the StorageClassDescribeTool. It takes a context and a Configs object as parameters, builds Kubernetes clients for the provided configurations, and infers the tool using the description and invoke function. It returns the invokable tool or an error if any step fails.
func NewStorageClassDescribeTool(ctx context.Context, configs Configs) (tool.InvokableTool, error) {
	s := scheme.Scheme
	utilruntime.Must(storagev1.AddToScheme(s))
	return NewDescribeTool(ctx, configs, "kubernetes_describe_storageclass", storageclassDescribeDescription, &storagev1.StorageClass{}, nil)
}
