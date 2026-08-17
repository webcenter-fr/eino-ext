package alertmanager

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/components/tool"

	"github.com/webcenter-fr/eino-ext/components/middleware/safety"
)

// toolConstructor is a function that creates a single Alertmanager tool from configs.
type toolConstructor func(context.Context, Configs) (tool.InvokableTool, error)

// readOnlyConstructors lists all read-only Alertmanager tools.
var readOnlyConstructors = []toolConstructor{
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewInstanceListTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewAlertTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewAlertWriteTool(ctx, c) },
}

// writeConstructors lists all write/destructive Alertmanager tools.
var writeConstructors = []toolConstructor{}

// buildTools creates tools from the given constructors.
func buildTools(ctx context.Context, configs Configs, constructors []toolConstructor) ([]tool.InvokableTool, error) {
	tools := make([]tool.InvokableTool, 0, len(constructors))
	for i, fn := range constructors {
		t, err := fn(ctx, configs)
		if err != nil {
			return nil, fmt.Errorf("failed to create alertmanager tool %d: %w", i, err)
		}
		tools = append(tools, t)
	}
	return tools, nil
}

// NewAllTools creates all Alertmanager tools (read + write) for the given
// configurations and returns them as a flat slice ready to be registered with
// an eino ToolsNode.
func NewAllTools(ctx context.Context, configs Configs) ([]tool.InvokableTool, error) {
	return buildTools(ctx, configs, append(readOnlyConstructors, writeConstructors...))
}

// NewReadOnlyTools creates only the read-only Alertmanager tools and returns
// them as a flat slice ready to be registered with an eino ToolsNode. Write
// operations (alert write) are excluded.
func NewReadOnlyTools(ctx context.Context, configs Configs) ([]tool.InvokableTool, error) {
	return buildTools(ctx, configs, readOnlyConstructors)
}

// WriteToolNames returns the tool names of all Alertmanager write tools.
// These names can be passed to the safety middleware's Config.WriteToolNames.
// The component has no mutating tools, so this returns an empty slice.
func WriteToolNames() []string {
	return []string{}
}

// ExtractWriteToolNames creates all write tools from the given configs and
// extracts their tool names via Info(). Use this when the write tool set may
// change. For the standard set, prefer the lighter WriteToolNames(). The
// component has no write tools, so this returns an empty slice with no error.
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

// NewAllToolsWithSafety creates all Alertmanager tools (read + write) and
// returns them together with a pre-configured safety middleware. The
// middleware's WriteToolNames are auto-populated from the known write tools.
func NewAllToolsWithSafety(ctx context.Context, configs Configs, safetyCfg *safety.Config) ([]tool.InvokableTool, *safety.Middleware, error) {
	tools, err := NewAllTools(ctx, configs)
	if err != nil {
		return nil, nil, err
	}

	if safetyCfg == nil {
		safetyCfg = &safety.Config{}
	}
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
	_ tool.InvokableTool = (*InstanceListTool)(nil)
	_ tool.InvokableTool = (*AlertTool)(nil)
	_ tool.InvokableTool = (*AlertWriteTool)(nil)
)
