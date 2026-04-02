package kubernetes

import (
	"context"

	"github.com/cloudwego/eino/components/tool"
	"github.com/goccy/go-json"
	routev1 "github.com/openshift/api/route/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
)

const ocpRouteListDescription = `
** General Purpose **
It lists all the OCP Routes in a specified Kubernetes cluster.

** Output **
It return a JSON array of objects, where each object represents an OCP Route with the following fields:
- name: the name of the OCP Route.
- namespace: the namespace of the OCP Route.
- host: the host defined in the OCP Route rules.
- tls: is true if TLS is enabled for the OCP Route, false otherwise.
`

// OcpRouteListOutput defines the structure of the output returned by the OcpRouteList function. It represents an OCP Route with its name and namespace.
type OcpRouteListOutput struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Host      string `json:"host"`
	TLS       bool   `json:"tls"`
}

// ToJson returns the JSON representation of the OcpRouteListOutput struct. It marshals the struct into a JSON RawMessage and returns it. If there is an error during marshaling, it panics.
func (h *OcpRouteListOutput) ToJson(o *routev1.Route) json.RawMessage {

	// Forge object
	output := CloneObject(h)
	output.Name = o.Name
	output.Namespace = o.Namespace
	output.Host = o.Spec.Host

	if o.Spec.TLS != nil && o.Spec.TLS.Termination != "" {
		output.TLS = true
	}

	data, err := json.Marshal(output)
	if err != nil {
		panic(err)
	}
	return data
}

// NewOcpRouteListTool creates a new instance of the OcpRouteListTool. It takes a context and a Configs object as parameters, builds Kubernetes clients for the provided configurations, and infers the tool using the description and invoke function. It returns the invokable tool or an error if any step fails.
func NewOcpRouteListTool(ctx context.Context, configs Configs) (tool.InvokableTool, error) {
	s := scheme.Scheme
	utilruntime.Must(routev1.AddToScheme(s))

	return NewListTool(ctx, configs, "kubernetes_list_ocp_routes", ocpRouteListDescription, &routev1.RouteList{}, &OcpRouteListOutput{}, s)
}
