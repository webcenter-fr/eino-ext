package argocd

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
)

// toolConstructor is a function that creates a single ArgoCD tool from configs.
type toolConstructor func(context.Context, Configs) (tool.InvokableTool, error)

// readOnlyConstructors lists all read-only ArgoCD tools (list + describe).
var readOnlyConstructors = []toolConstructor{
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewInstanceListTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewApplicationListTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewApplicationDescribeTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewCertificateListTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewClusterListTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewClusterDescribeTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewProjectListTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewProjectDescribeTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewRepositoryListTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewRepositoryDescribeTool(ctx, c) },
}

// writeConstructors lists all write/destructive ArgoCD tools.
var writeConstructors = []toolConstructor{
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewApplicationCreateTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewApplicationDeleteTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewApplicationSyncTool(ctx, c) },
}

// buildTools creates tools from the given constructors.
func buildTools(ctx context.Context, configs Configs, constructors []toolConstructor) ([]tool.InvokableTool, error) {
	tools := make([]tool.InvokableTool, 0, len(constructors))
	for i, fn := range constructors {
		t, err := fn(ctx, configs)
		if err != nil {
			return nil, fmt.Errorf("failed to create argocd tool %d: %w", i, err)
		}
		tools = append(tools, t)
	}
	return tools, nil
}

// NewAllTools creates all ArgoCD tools (read + write) for the given configurations
// and returns them as a flat slice ready to be registered with an eino ToolsNode.
func NewAllTools(ctx context.Context, configs Configs) ([]tool.InvokableTool, error) {
	return buildTools(ctx, configs, append(readOnlyConstructors, writeConstructors...))
}

// NewReadOnlyTools creates only the read-only ArgoCD tools (list + describe)
// and returns them as a flat slice ready to be registered with an eino ToolsNode.
// Write operations (create, delete, sync) are excluded.
func NewReadOnlyTools(ctx context.Context, configs Configs) ([]tool.InvokableTool, error) {
	return buildTools(ctx, configs, readOnlyConstructors)
}
