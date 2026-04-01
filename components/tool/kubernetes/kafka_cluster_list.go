package kubernetes

import (
	"context"

	strimzi "github.com/RedHatInsights/strimzi-client-go/apis/kafka.strimzi.io/v1beta2"
	"github.com/cloudwego/eino/components/tool"
	"github.com/goccy/go-json"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
)

const kafkaClusterListDescription = `
** General Purpose **
It lists all the KafkaClusters in a specified Kubernetes cluster.

** Output **
It return a JSON array of objects, where each object represents a KafkaCluster with the following fields:
- name: the name of the KafkaCluster.
- namespace: the namespace of the KafkaCluster.
- status: the status of the KafkaCluster.
- version: the Kafka version of the KafkaCluster.
`

// KafkaClusterListOutput defines the structure of the output returned by the KafkaClusterList function. It represents a KafkaCluster with its name and namespace.
type KafkaClusterListOutput struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Status    string `json:"status"`
	Version   string `json:"version"`
}

// ToJson returns the JSON representation of the KafkaClusterListOutput struct. It marshals the struct into a JSON RawMessage and returns it. If there is an error during marshaling, it panics.
func (h *KafkaClusterListOutput) ToJson(o *strimzi.Kafka) json.RawMessage {

	// Forge object
	output := CloneObject(h)
	output.Name = o.Name
	output.Namespace = o.Namespace
	if o.Status.KafkaVersion != nil {
		output.Version = *o.Status.KafkaVersion
	}

	for _, con := range o.Status.Conditions {
		if con.Type == ptr.To("Ready") {
			if con.Status == ptr.To("True") {
				output.Status = "Ready"
			} else {
				output.Status = "Not Ready"
			}
			break
		}
	}

	data, err := json.Marshal(output)
	if err != nil {
		panic(err)
	}
	return data
}

// NewKafkaClusterListTool creates a new instance of the KafkaClusterListTool. It takes a context and a Configs object as parameters, builds Kubernetes clients for the provided configurations, and infers the tool using the description and invoke function. It returns the invokable tool or an error if any step fails.
func NewKafkaClusterListTool(ctx context.Context, configs Configs) (tool.InvokableTool, error) {
	s := scheme.Scheme
	utilruntime.Must(strimzi.AddToScheme(s))

	return NewListTool(ctx, configs, "kubernetes_list_kafka_clusters", kafkaClusterListDescription, &strimzi.KafkaList{}, &KafkaClusterListOutput{}, s)
}
