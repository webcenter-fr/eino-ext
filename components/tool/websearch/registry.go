package websearch

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
)

// NewAllTools creates both web_search and web_fetch tools and returns them
// as a flat slice ready to be registered with an eino ToolsNode.
func NewAllTools(ctx context.Context, cfg *Config) ([]tool.InvokableTool, error) {
	tools := make([]tool.InvokableTool, 0, 2)

	searchTool, err := NewWebSearchTool(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create web_search tool: %w", err)
	}
	tools = append(tools, searchTool)

	fetchTool, err := NewWebFetchTool(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create web_fetch tool: %w", err)
	}
	tools = append(tools, fetchTool)

	return tools, nil
}
