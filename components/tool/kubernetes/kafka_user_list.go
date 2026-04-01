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

const kafkaUserListDescription = `
** General Purpose **
It lists all the KafkaUsers in a specified Kubernetes cluster.

** Output **
It return a JSON array of objects, where each object represents a KafkaUser with the following fields:
- name: the name of the KafkaUser.
- namespace: the namespace of the KafkaUser.
- username: the username of the KafkaUser.
- status: the status of the KafkaUser.
`

// KafkaUserListOutput defines the structure of the output returned by the KafkaUserList function. It represents a KafkaUser with its name and namespace.
type KafkaUserListOutput struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Username  string `json:"username"`
	Status    string `json:"status"`
}

// ToJson returns the JSON representation of the KafkaUserListOutput struct. It marshals the struct into a JSON RawMessage and returns it. If there is an error during marshaling, it panics.
func (h *KafkaUserListOutput) ToJson(o *strimzi.KafkaUser) json.RawMessage {

	// Forge object
	output := CloneObject(h)
	output.Name = o.Name
	output.Namespace = o.Namespace
	if o.Status.Username != nil {
		output.Username = *o.Status.Username
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

// NewKafkaUserListTool creates a new instance of the KafkaUserListTool. It takes a context and a Configs object as parameters, builds Kubernetes clients for the provided configurations, and infers the tool using the description and invoke function. It returns the invokable tool or an error if any step fails.
func NewKafkaUserListTool(ctx context.Context, configs Configs) (tool.InvokableTool, error) {
	s := scheme.Scheme
	utilruntime.Must(strimzi.AddToScheme(s))

	return NewListTool(ctx, configs, "kubernetes_list_kafka_users", kafkaUserListDescription, &strimzi.KafkaUserList{}, &KafkaUserListOutput{}, s)
}
