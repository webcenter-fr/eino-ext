package kubernetes

import (
	"context"

	strimzi "github.com/RedHatInsights/strimzi-client-go/apis/kafka.strimzi.io/v1beta2"
	"github.com/cloudwego/eino/components/tool"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
)

const kafkaClusterDescribeDescription = `
** General Purpose **
It gets the details of a specific KafkaCluster in a specified Kubernetes cluster.

** Output **
It return a JSON object representing the kubernetes KafkaCluster
`

// NewKafkaClusterDescribeTool creates a new instance of the KafkaClusterDescribeTool. It takes a context and a Configs object as parameters, builds Kubernetes clients for the provided configurations, and infers the tool using the description and invoke function. It returns the invokable tool or an error if any step fails.
func NewKafkaClusterDescribeTool(ctx context.Context, configs Configs) (tool.InvokableTool, error) {
	s := scheme.Scheme
	utilruntime.Must(strimzi.AddToScheme(s))

	return NewDescribeTool(ctx, configs, "kubernetes_describe_kafka_cluster", kafkaClusterDescribeDescription, &strimzi.Kafka{}, s)
}
