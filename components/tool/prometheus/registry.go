package prometheus

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
	"github.com/webcenter-fr/eino-ext/components/middleware/safety"
)

type toolConstructor func(context.Context, Configs) (tool.InvokableTool, error)

var readOnlyConstructors = []toolConstructor{
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewInstanceListTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewMetricTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewAlertTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewTargetListTool(ctx, c) },
}

// writeConstructors lists all write/destructive Prometheus tools.
var writeConstructors = []toolConstructor{
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewAlertWriteTool(ctx, c) },
}

func buildTools(ctx context.Context, configs Configs, constructors []toolConstructor) ([]tool.InvokableTool, error) {
	tools := make([]tool.InvokableTool, 0, len(constructors))
	for i, fn := range constructors {
		t, err := fn(ctx, configs)
		if err != nil {
			return nil, fmt.Errorf("failed to create prometheus tool %d: %w", i, err)
		}
		tools = append(tools, t)
	}
	return tools, nil
}

// NewAllTools creates all Prometheus tools (read + write).
func NewAllTools(ctx context.Context, configs Configs) ([]tool.InvokableTool, error) {
	all := make([]toolConstructor, 0, len(readOnlyConstructors)+len(writeConstructors))
	all = append(all, readOnlyConstructors...)
	all = append(all, writeConstructors...)
	return buildTools(ctx, configs, all)
}

// NewReadOnlyTools creates only the read-only Prometheus tools. Write
// operations (Alertmanager alert write) are excluded.
func NewReadOnlyTools(ctx context.Context, configs Configs) ([]tool.InvokableTool, error) {
	return buildTools(ctx, configs, readOnlyConstructors)
}

// WriteToolNames returns the names of all Prometheus write tools.
// These names can be passed to the safety middleware's Config.WriteToolNames.
func WriteToolNames() []string {
	return []string{alertWriteToolName}
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

// NewAllToolsWithSafety creates all Prometheus tools with safety middleware.
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
	_ tool.InvokableTool = (*MetricTool)(nil)
	_ tool.InvokableTool = (*AlertTool)(nil)
	_ tool.InvokableTool = (*TargetListTool)(nil)
	_ tool.InvokableTool = (*AlertWriteTool)(nil)
)
