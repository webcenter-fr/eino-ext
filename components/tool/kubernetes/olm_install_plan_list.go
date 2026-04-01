package kubernetes

import (
	"context"

	"github.com/cloudwego/eino/components/tool"
	"github.com/goccy/go-json"
	olmv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
)

const olmInstallPlanListDescription = `
** General Purpose **
It lists all the OLMInstallPlans in a specified Kubernetes cluster.

** Output **
It return a JSON array of objects, where each object represents an OLMInstallPlan with the following fields:
- name: the name of the OLMInstallPlan.
- namespace: the namespace of the OLMInstallPlan.
- status: the status of the OLMInstallPlan.
- isApproved: whether the OLMInstallPlan is approved or not.
`

// OLMInstallPlanListOutput defines the structure of the output returned by the OLMInstallPlanList function. It represents an OLMInstallPlan with its name and namespace.
type OLMInstallPlanListOutput struct {
	Name       string `json:"name"`
	Namespace  string `json:"namespace"`
	Status     string `json:"status"`
	IsApproved bool   `json:"isApproved"`
}

// ToJson returns the JSON representation of the OLMInstallPlanListOutput struct. It marshals the struct into a JSON RawMessage and returns it. If there is an error during marshaling, it panics.
func (h *OLMInstallPlanListOutput) ToJson(o *olmv1alpha1.InstallPlan) json.RawMessage {

	// Forge object
	output := CloneObject(h)
	output.Name = o.Name
	output.Namespace = o.Namespace
	output.Status = string(o.Status.Phase)
	output.IsApproved = o.Spec.Approved

	data, err := json.Marshal(output)
	if err != nil {
		panic(err)
	}
	return data
}

// NewOLMInstallPlanListTool creates a new instance of the OLMInstallPlanListTool. It takes a context and a Configs object as parameters, builds Kubernetes clients for the provided configurations, and infers the tool using the description and invoke function. It returns the invokable tool or an error if any step fails.
func NewOLMInstallPlanListTool(ctx context.Context, configs Configs) (tool.InvokableTool, error) {
	s := scheme.Scheme
	utilruntime.Must(olmv1alpha1.AddToScheme(s))

	return NewListTool(ctx, configs, "kubernetes_list_olm_install_plans", olmInstallPlanListDescription, &olmv1alpha1.InstallPlanList{}, &OLMInstallPlanListOutput{}, s)
}
