package kubernetes

import (
	"context"

	"github.com/cloudwego/eino/components/tool"
	olmv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
)

const olmSubscriptionDescribeDescription = `
** General Purpose **
It gets the details of a specific OLMSubscription in a specified Kubernetes cluster.

** Output **
It return a JSON object representing the kubernetes OLMSubscription
`

// NewOLMSubscriptionDescribeTool creates a new instance of the OLMSubscriptionDescribeTool. It takes a context and a Configs object as parameters, builds Kubernetes clients for the provided configurations, and infers the tool using the description and invoke function. It returns the invokable tool or an error if any step fails.
func NewOLMSubscriptionDescribeTool(ctx context.Context, configs Configs) (tool.InvokableTool, error) {
	s := scheme.Scheme
	utilruntime.Must(olmv1alpha1.AddToScheme(s))

	return NewDescribeTool(ctx, configs, "kubernetes_describe_olm_subscription", olmSubscriptionDescribeDescription, &olmv1alpha1.Subscription{}, s)
}
