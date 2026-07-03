package kubernetes

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
	"k8s.io/apimachinery/pkg/runtime"
)

// toolConstructor is a function that creates a single Kubernetes tool from configs.
type toolConstructor func(context.Context, Configs) (tool.InvokableTool, error)

// readOnlyConstructors lists all read-only Kubernetes tools (list + describe + pod logs).
var readOnlyConstructors = []toolConstructor{
	// Core list tools
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewPodListTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewDeploymentListTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewStatefulSetListTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewDaemonSetListTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewConfigMapListTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewSecretListTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewServiceListTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewIngressListTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewPVCListTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewNodeListTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewNamespaceListTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewEventListTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewServiceAccountListTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewStorageClassListTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewCustomResourceDefinitionListTool(ctx, c) },

	// Core describe tools
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewPodDescribeTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewDeploymentDescribeTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewStatefulsetDescribeTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewDaemonsetDescribeTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewConfigMapDescribeTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewSecretDescribeTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewServiceDescribeTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewIngressDescribeTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewPVCDescribeTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewNodeDescribeTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewNamespaceDescribeTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewEventDescribeTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewServiceAccountDescribeTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewStorageClassDescribeTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewCustomResourceDefinitionDescribeTool(ctx, c) },

	// Kafka tools
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewKafkaClusterListTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewKafkaClusterDescribeTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewKafkaTopicListTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewKafkaTopicDescribeTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewKafkaNodePoolListTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewKafkaNodePoolDescribeTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewKafkaUserListTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewKafkaUserDescribeTool(ctx, c) },

	// OLM tools
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewOLMClusterServiceVersionListTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewOLMClusterServiceVersionDescribeTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewOLMSubscriptionListTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewOLMSubscriptionDescribeTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewOLMInstallPlanListTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewOLMInstallPlanDescribeTool(ctx, c) },

	// OpenShift tools
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewOcpRouteListTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewOcpRouteDescribeTool(ctx, c) },

	// Spark tools
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewSparkApplicationListTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewSparkApplicationDescribeTool(ctx, c) },

	// Generic tools (for custom resources)
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewResourceListTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewResourceDescribeTool(ctx, c) },

	// Pod log (read-only streaming)
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewPodLogTool(ctx, c) },
}

// writeConstructors lists the Kubernetes tools that can execute commands on pods.
var writeConstructors = []toolConstructor{
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewPodExecTool(ctx, c) },
}

// buildTools creates tools from the given constructors.
func buildTools(ctx context.Context, configs Configs, constructors []toolConstructor) ([]tool.InvokableTool, error) {
	tools := make([]tool.InvokableTool, 0, len(constructors))
	for i, fn := range constructors {
		t, err := fn(ctx, configs)
		if err != nil {
			return nil, fmt.Errorf("failed to create kubernetes tool %d: %w", i, err)
		}
		tools = append(tools, t)
	}
	return tools, nil
}

// NewAllTools creates all Kubernetes tools (read + write) for the given configurations
// and returns them as a flat slice ready to be registered with an eino ToolsNode.
func NewAllTools(ctx context.Context, configs Configs, scheme *runtime.Scheme) ([]tool.InvokableTool, error) {
	_ = scheme // reserved for future use (e.g. CRD scheme registration)
	return buildTools(ctx, configs, append(readOnlyConstructors, writeConstructors...))
}

// NewReadOnlyTools creates only the read-only Kubernetes tools (list + describe + pod logs)
// and returns them as a flat slice ready to be registered with an eino ToolsNode.
// PodExecTool (command execution) is excluded.
func NewReadOnlyTools(ctx context.Context, configs Configs, scheme *runtime.Scheme) ([]tool.InvokableTool, error) {
	_ = scheme // reserved for future use
	return buildTools(ctx, configs, readOnlyConstructors)
}
