// Package argocd provides an MCP server exposing the ArgoCD eino tools.
package argocd

import (
	"context"

	emperrors "emperror.dev/errors"

	argocdtool "github.com/webcenter-fr/eino-ext/components/tool/argocd"
	mcpserver "github.com/webcenter-fr/eino-ext/libs/mcp"
)

// NewServer builds an MCP server exposing all ArgoCD tools (read + write).
// The instance argument is "instance".
func NewServer(ctx context.Context, configs argocdtool.Configs, cfg *mcpserver.Config) (*mcpserver.Server, error) {
	tools, err := argocdtool.NewAllTools(ctx, configs)
	if err != nil {
		return nil, emperrors.Wrap(err, "failed to create argocd tools")
	}
	if cfg == nil {
		cfg = &mcpserver.Config{}
	}
	if cfg.Name == "" {
		cfg.Name = "argocd"
	}
	cfg.Tools = tools
	cfg.WriteTools = argocdtool.WriteToolNames()
	cfg.InstanceParam = "instance"
	return mcpserver.NewServer(ctx, cfg)
}
