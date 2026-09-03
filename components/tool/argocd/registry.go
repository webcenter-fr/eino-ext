package argocd

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/components/tool"

	"github.com/webcenter-fr/eino-ext/components/middleware/safety"
)

// toolConstructor is a function that creates a single ArgoCD tool from configs.
type toolConstructor func(context.Context, Configs) (tool.InvokableTool, error)

// readOnlyConstructors lists all read-only ArgoCD tools (list + describe).
var readOnlyConstructors = []toolConstructor{
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewInstanceListTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) {
		return NewApplicationListTool(ctx, c)
	},
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) {
		return NewApplicationDescribeTool(ctx, c)
	},
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) {
		return NewCertificateListTool(ctx, c)
	},
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewClusterListTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) {
		return NewClusterDescribeTool(ctx, c)
	},
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewProjectListTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) {
		return NewProjectDescribeTool(ctx, c)
	},
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewRepositoryListTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) {
		return NewRepositoryDescribeTool(ctx, c)
	},
}

// writeConstructors lists all write/destructive ArgoCD tools.
var writeConstructors = []toolConstructor{
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) {
		return NewApplicationCreateTool(ctx, c)
	},
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) {
		return NewApplicationDeleteTool(ctx, c)
	},
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) {
		return NewApplicationSyncTool(ctx, c)
	},
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

// WriteToolNames returns the tool names of all ArgoCD write tools.
// These names can be passed to the safety middleware's Config.WriteToolNames.
//
// Contract: every name listed here MUST honor dryRun=true as a no-side-effect
// preview. The safety gate treats dry-run as always-safe, so a tool that mutates
// during dry-run would let an unconfirmed model call bypass the gate.
func WriteToolNames() []string {
	return []string{
		"argocd_application_create",
		"argocd_application_delete",
		"argocd_application_sync",
	}
}

// ExtractWriteToolNames creates all write tools from the given configs and
// extracts their tool names via Info(). Use this when the write tool set may
// change. For the standard set, prefer the lighter WriteToolNames().
func ExtractWriteToolNames(ctx context.Context, configs Configs) ([]string, error) {
	tools, err := buildTools(ctx, configs, writeConstructors)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(tools))
	for i, t := range tools {
		info, infoErr := t.Info(ctx)
		if infoErr != nil {
			return nil, fmt.Errorf("failed to get info for write tool %d: %w", i, infoErr)
		}
		names[i] = info.Name
	}
	return names, nil
}

// NewAllToolsWithSafety creates all ArgoCD tools (read + write) and returns them
// together with a pre-configured safety middleware. The middleware's
// WriteToolNames are auto-populated from the known write tools.
//
// Usage:
//
//	tools, mw, err := argocd.NewAllToolsWithSafety(ctx, configs, &safety.Config{
//	    Policy: myCELPolicy,
//	})
//	if err != nil {
//	    log.Fatal(err)
//	}
//	agent, _ := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
//	    Name:     "my-agent",
//	    Model:    myModel,
//	    Tools:    tools,
//	    Handlers: []adk.ChatModelAgentMiddleware{mw},
//	})
func NewAllToolsWithSafety(ctx context.Context, configs Configs, safetyCfg *safety.Config) ([]tool.InvokableTool, *safety.Middleware, error) {
	tools, err := NewAllTools(ctx, configs)
	if err != nil {
		return nil, nil, err
	}

	if safetyCfg == nil {
		safetyCfg = &safety.Config{}
	}
	// Auto-populate write tool names if not already set.
	if len(safetyCfg.WriteToolNames) == 0 {
		safetyCfg.WriteToolNames = WriteToolNames()
	}

	mw, err := safety.New(safetyCfg)
	if err != nil {
		return nil, nil, err
	}

	return tools, mw, nil
}

var (
	_ tool.InvokableTool = (*ApplicationCreateTool)(nil)
	_ tool.InvokableTool = (*ApplicationDeleteTool)(nil)
	_ tool.InvokableTool = (*ApplicationSyncTool)(nil)
	_ tool.InvokableTool = (*ApplicationDescribeTool)(nil)
	_ tool.InvokableTool = (*ApplicationListTool)(nil)
	_ tool.InvokableTool = (*ClusterDescribeTool)(nil)
	_ tool.InvokableTool = (*ClusterListTool)(nil)
	_ tool.InvokableTool = (*ProjectDescribeTool)(nil)
	_ tool.InvokableTool = (*ProjectListTool)(nil)
	_ tool.InvokableTool = (*RepositoryDescribeTool)(nil)
	_ tool.InvokableTool = (*RepositoryListTool)(nil)
	_ tool.InvokableTool = (*CertificateListTool)(nil)
	_ tool.InvokableTool = (*InstanceListTool)(nil)
)
