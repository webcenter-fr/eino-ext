// Package grafana provides an MCP server exposing the Grafana eino tools.
package grafana

import (
	"context"

	emperrors "emperror.dev/errors"

	grafanatool "github.com/webcenter-fr/eino-ext/components/tool/grafana"
	mcpserver "github.com/webcenter-fr/eino-ext/libs/mcp"
)

// NewServer builds an MCP server exposing all Grafana tools (read + write).
// The instance argument is "instance".
func NewServer(ctx context.Context, configs grafanatool.Configs, cfg *mcpserver.Config) (*mcpserver.Server, error) {
	tools, err := grafanatool.NewAllTools(ctx, configs)
	if err != nil {
		return nil, emperrors.Wrap(err, "failed to create grafana tools")
	}
	if cfg == nil {
		cfg = &mcpserver.Config{}
	}
	if cfg.Name == "" {
		cfg.Name = "grafana"
	}
	cfg.Tools = tools
	cfg.WriteTools = grafanatool.WriteToolNames()
	cfg.InstanceParam = "instance"
	return mcpserver.NewServer(ctx, cfg)
}
