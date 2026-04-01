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

const kafkaNodePoolListDescription = `
** General Purpose **
It lists all the KafkaNodePools in a specified Kubernetes cluster.

** Output **
It return a JSON array of objects, where each object represents a KafkaNodePool with the following fields:
- name: the name of the KafkaNodePool.
- namespace: the namespace of the KafkaNodePool.
- status: the status of the KafkaNodePool.
- replicas: the number of replicas of the KafkaNodePool.
- roles: the roles of the KafkaNodePool.
`

// KafkaNodePoolListOutput defines the structure of the output returned by the KafkaNodePoolList function. It represents a KafkaNodePool with its name and namespace.
type KafkaNodePoolListOutput struct {
	Name      string   `json:"name"`
	Namespace string   `json:"namespace"`
	Status    string   `json:"status"`
	Replicas  int32    `json:"replicas"`
	Roles     []string `json:"roles"`
}

// ToJson returns the JSON representation of the KafkaNodePoolListOutput struct. It marshals the struct into a JSON RawMessage and returns it. If there is an error during marshaling, it panics.
func (h *KafkaNodePoolListOutput) ToJson(o *strimzi.KafkaNodePool) json.RawMessage {

	// Forge object
	output := CloneObject(h)
	output.Name = o.Name
	output.Namespace = o.Namespace
	if o.Status.Replicas != nil {
		output.Replicas = *o.Status.Replicas
	}
	output.Roles = make([]string, 0, len(o.Spec.Roles))
	for _, role := range o.Spec.Roles {
		output.Roles = append(output.Roles, string(role))
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

// NewKafkaNodePoolListTool creates a new instance of the KafkaNodePoolListTool. It takes a context and a Configs object as parameters, builds Kubernetes clients for the provided configurations, and infers the tool using the description and invoke function. It returns the invokable tool or an error if any step fails.
func NewKafkaNodePoolListTool(ctx context.Context, configs Configs) (tool.InvokableTool, error) {
	s := scheme.Scheme
	utilruntime.Must(strimzi.AddToScheme(s))

	return NewListTool(ctx, configs, "kubernetes_list_kafka_node_pools", kafkaNodePoolListDescription, &strimzi.KafkaNodePoolList{}, &KafkaNodePoolListOutput{}, s)
}
