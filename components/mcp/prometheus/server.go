// Package prometheus provides an MCP server exposing the Prometheus eino tools.
package prometheus

import (
	"context"

	emperrors "emperror.dev/errors"

	promtool "github.com/webcenter-fr/eino-ext/components/tool/prometheus"
	mcpserver "github.com/webcenter-fr/eino-ext/libs/mcp"
)

// NewServer builds an MCP server exposing all Prometheus tools. Prometheus has
// no write tools, so the server exposes read tools only — the gate is a no-op
// but the wiring is uniform. The instance argument is "instance".
func NewServer(ctx context.Context, configs promtool.Configs, cfg *mcpserver.Config) (*mcpserver.Server, error) {
	tools, err := promtool.NewAllTools(ctx, configs)
	if err != nil {
		return nil, emperrors.Wrap(err, "failed to create prometheus tools")
	}
	if cfg == nil {
		cfg = &mcpserver.Config{}
	}
	if cfg.Name == "" {
		cfg.Name = "prometheus"
	}
	cfg.Tools = tools
	cfg.WriteTools = promtool.WriteToolNames()
	cfg.InstanceParam = "instance"
	return mcpserver.NewServer(ctx, cfg)
}
