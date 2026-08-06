package s3

import (
	"context"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/webcenter-fr/eino-ext/components/middleware/safety"
)

type toolConstructor func(context.Context, Configs) (tool.InvokableTool, error)

var readOnlyConstructors = []toolConstructor{
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewBucketListTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewListObjectsTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewGetUsageTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) {
		return NewListObjectsWithSizeTool(ctx, c)
	},
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewGetLifecycleTool(ctx, c) },
}

//nolint:unused // placeholder for future write tools
var writeConstructors []toolConstructor

func buildTools(ctx context.Context, configs Configs, constructors []toolConstructor) ([]tool.InvokableTool, error) {
	tools := make([]tool.InvokableTool, 0, len(constructors))
	for i, fn := range constructors {
		t, err := fn(ctx, configs)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to create S3 tool %d", i)
		}
		tools = append(tools, t)
	}
	return tools, nil
}

// NewAllTools creates all S3 tools (all read-only).
func NewAllTools(ctx context.Context, configs Configs) ([]tool.InvokableTool, error) {
	return NewReadOnlyTools(ctx, configs)
}

// NewReadOnlyTools creates only the read-only S3 tools.
func NewReadOnlyTools(ctx context.Context, configs Configs) ([]tool.InvokableTool, error) {
	return buildTools(ctx, configs, readOnlyConstructors)
}

// WriteToolNames returns the names of write tools. All S3 tools are read-only.
func WriteToolNames() []string {
	return nil
}

// ExtractWriteToolNames dynamically extracts write tool names. Returns nil since there are no write tools.
func ExtractWriteToolNames(ctx context.Context, configs Configs) ([]string, error) {
	return nil, nil
}

// NewAllToolsWithSafety creates all S3 tools with safety middleware.
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
	_ tool.InvokableTool = (*BucketListTool)(nil)
	_ tool.InvokableTool = (*ListObjectsTool)(nil)
	_ tool.InvokableTool = (*GetUsageTool)(nil)
	_ tool.InvokableTool = (*ListObjectsWithSizeTool)(nil)
	_ tool.InvokableTool = (*GetLifecycleTool)(nil)
)
