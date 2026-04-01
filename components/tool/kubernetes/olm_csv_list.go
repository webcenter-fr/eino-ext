package kubernetes

import (
	"context"

	"github.com/cloudwego/eino/components/tool"
	"github.com/goccy/go-json"
	olmv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
)

const olmClusterServiceVersionListDescription = `
** General Purpose **
It lists all the OLMClusterServiceVersions in a specified Kubernetes cluster.

** Output **
It return a JSON array of objects, where each object represents an OLMClusterServiceVersion with the following fields:
- name: the name of the OLMClusterServiceVersion.
- namespace: the namespace of the OLMClusterServiceVersion.
- status: the status of the OLMClusterServiceVersion.
-  v
`

// OLMClusterServiceVersionListOutput defines the structure of the output returned by the OLMClusterServiceVersionList function. It represents an OLMClusterServiceVersion with its name and namespace.
type OLMClusterServiceVersionListOutput struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Status    string `json:"status"`
	Version   string `json:"version"`
}

// ToJson returns the JSON representation of the OLMClusterServiceVersionListOutput struct. It marshals the struct into a JSON RawMessage and returns it. If there is an error during marshaling, it panics.
func (h *OLMClusterServiceVersionListOutput) ToJson(o *olmv1alpha1.ClusterServiceVersion) json.RawMessage {

	// Forge object
	output := CloneObject(h)
	output.Name = o.Name
	output.Namespace = o.Namespace
	output.Status = string(o.Status.Phase)
	output.Version = o.Spec.Version.String()

	data, err := json.Marshal(output)
	if err != nil {
		panic(err)
	}
	return data
}

// NewOLMClusterServiceVersionListTool creates a new instance of the OLMClusterServiceVersionListTool. It takes a context and a Configs object as parameters, builds Kubernetes clients for the provided configurations, and infers the tool using the description and invoke function. It returns the invokable tool or an error if any step fails.
func NewOLMClusterServiceVersionListTool(ctx context.Context, configs Configs) (tool.InvokableTool, error) {
	s := scheme.Scheme
	utilruntime.Must(olmv1alpha1.AddToScheme(s))

	return NewListTool(ctx, configs, "kubernetes_list_olm_cluster_service_versions", olmClusterServiceVersionListDescription, &olmv1alpha1.ClusterServiceVersionList{}, &OLMClusterServiceVersionListOutput{}, s)
}
