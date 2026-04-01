package kubernetes

import (
	"context"

	"github.com/cloudwego/eino/components/tool"
	"github.com/goccy/go-json"
	spark "github.com/kubeflow/spark-operator/api/v1beta2"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
)

const sparkApplicationListDescription = `
** General Purpose **
It lists all the SparkApplications in a specified Kubernetes cluster.

** Output **
It return a JSON array of objects, where each object represents a SparkApplication with the following fields:
- name: the name of the SparkApplication.
- namespace: the namespace of the SparkApplication.
- status: the status of the SparkApplication.
- lastAttempt: the time of the last submission attempt of the SparkApplication.
- terminationTime: the termination time of the SparkApplication.
`

// SparkApplicationListOutput defines the structure of the output returned by the SparkApplicationList function. It represents a SparkApplication with its name and namespace.
type SparkApplicationListOutput struct {
	Name            string `json:"name"`
	Namespace       string `json:"namespace"`
	Status          string `json:"status"`
	LastAttempt     string `json:"lastAttempt"`
	TerminationTime string `json:"terminationTime"`
}

// ToJson returns the JSON representation of the SparkApplicationListOutput struct. It marshals the struct into a JSON RawMessage and returns it. If there is an error during marshaling, it panics.
func (h *SparkApplicationListOutput) ToJson(o *spark.SparkApplication) json.RawMessage {

	// Forge object
	output := CloneObject(h)
	output.Name = o.Name
	output.Namespace = o.Namespace
	output.Status = string(o.Status.AppState.State)

	if !o.Status.LastSubmissionAttemptTime.IsZero() {
		output.LastAttempt = o.Status.LastSubmissionAttemptTime.String()
	}

	if !o.Status.TerminationTime.IsZero() {
		output.TerminationTime = o.Status.TerminationTime.String()
	}

	data, err := json.Marshal(output)
	if err != nil {
		panic(err)
	}
	return data
}

// NewSparkApplicationListTool creates a new instance of the SparkApplicationListTool. It takes a context and a Configs object as parameters, builds Kubernetes clients for the provided configurations, and infers the tool using the description and invoke function. It returns the invokable tool or an error if any step fails.
func NewSparkApplicationListTool(ctx context.Context, configs Configs) (tool.InvokableTool, error) {
	s := scheme.Scheme
	utilruntime.Must(spark.AddToScheme(s))

	return NewListTool(ctx, configs, "kubernetes_list_spark_applications", sparkApplicationListDescription, &spark.SparkApplicationList{}, &SparkApplicationListOutput{}, s)
}
