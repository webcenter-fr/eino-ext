package kubernetes

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
	"github.com/goccy/go-json"
	olmv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
)

const olmSubscriptionListDescription = `
** General Purpose **
It lists all the OLMSubscriptions in a specified Kubernetes cluster.

** Output **
It return a JSON array of objects, where each object represents an OLMSubscription with the following fields:
- name: the name of the OLMSubscription.
- namespace: the namespace of the OLMSubscription.
- status: the status of the OLMSubscription.
- version: the version of the OLMSubscription.
- sourceName: the source name of the OLMSubscription.
- packageName: the package name of the OLMSubscription.
`

// OLMSubscriptionListOutput defines the structure of the output returned by the OLMSubscriptionList function. It represents an OLMSubscription with its name and namespace.
type OLMSubscriptionListOutput struct {
	Name        string `json:"name"`
	Namespace   string `json:"namespace"`
	Status      string `json:"status"`
	Version     string `json:"version"`
	SourceName  string `json:"sourceName"`
	PackageName string `json:"packageName"`
}

// ToJson returns the JSON representation of the OLMSubscriptionListOutput struct. It marshals the struct into a JSON RawMessage and returns it. If there is an error during marshaling, it panics.
func (h *OLMSubscriptionListOutput) ToJson(o *olmv1alpha1.Subscription) json.RawMessage {

	// Forge object
	output := CloneObject(h)
	output.Name = o.Name
	output.Namespace = o.Namespace
	output.Version = o.Status.InstalledCSV
	output.SourceName = fmt.Sprintf("%s/%s", o.Spec.CatalogSourceNamespace, o.Spec.CatalogSource)
	output.PackageName = o.Spec.Package
	output.Status = string(o.Status.State)

	data, err := json.Marshal(output)
	if err != nil {
		panic(err)
	}
	return data
}

// NewOLMSubscriptionListTool creates a new instance of the OLMSubscriptionListTool. It takes a context and a Configs object as parameters, builds Kubernetes clients for the provided configurations, and infers the tool using the description and invoke function. It returns the invokable tool or an error if any step fails.
func NewOLMSubscriptionListTool(ctx context.Context, configs Configs) (tool.InvokableTool, error) {
	s := scheme.Scheme
	utilruntime.Must(olmv1alpha1.AddToScheme(s))

	return NewListTool(ctx, configs, "kubernetes_list_olm_subscriptions", olmSubscriptionListDescription, &olmv1alpha1.SubscriptionList{}, &OLMSubscriptionListOutput{}, s)
}
