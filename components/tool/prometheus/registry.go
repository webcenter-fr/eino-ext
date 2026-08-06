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
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewMetricQueryTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewMetricRangeTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewAlertListTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewAlertDescribeTool(ctx, c) },
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

// NewAllTools creates all Prometheus tools (currently all read-only).
func NewAllTools(ctx context.Context, configs Configs) ([]tool.InvokableTool, error) {
	return NewReadOnlyTools(ctx, configs)
}

// NewReadOnlyTools creates only the read-only Prometheus tools (all tools in this component).
func NewReadOnlyTools(ctx context.Context, configs Configs) ([]tool.InvokableTool, error) {
	return buildTools(ctx, configs, readOnlyConstructors)
}

// WriteToolNames returns the names of write tools. All Prometheus tools are read-only, so this returns nil.
func WriteToolNames() []string {
	return nil
}

// ExtractWriteToolNames dynamically extracts write tool names. Returns nil since there are no write tools.
func ExtractWriteToolNames(ctx context.Context, configs Configs) ([]string, error) {
	return nil, nil
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
)
