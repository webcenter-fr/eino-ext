package kubernetes

import (
	"context"

	"github.com/cloudwego/eino/components/tool"
	spark "github.com/kubeflow/spark-operator/api/v1beta2"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
)

const sparkApplicationDescribeDescription = `
** General Purpose **
It gets the details of a specific SparkApplication in a specified Kubernetes cluster.

** Output **
It return a JSON object representing the kubernetes SparkApplication
`

// NewSparkApplicationDescribeTool creates a new instance of the SparkApplicationDescribeTool. It takes a context and a Configs object as parameters, builds Kubernetes clients for the provided configurations, and infers the tool using the description and invoke function. It returns the invokable tool or an error if any step fails.
func NewSparkApplicationDescribeTool(ctx context.Context, configs Configs) (tool.InvokableTool, error) {
	s := scheme.Scheme
	utilruntime.Must(spark.AddToScheme(s))

	return NewDescribeTool(ctx, configs, "kubernetes_describe_spark_application", sparkApplicationDescribeDescription, &spark.SparkApplication{}, s)
}
