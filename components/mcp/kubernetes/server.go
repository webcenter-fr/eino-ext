// Package kubernetes provides an MCP server exposing the Kubernetes eino tools.
package kubernetes

import (
	"context"

	emperrors "emperror.dev/errors"
	"k8s.io/apimachinery/pkg/runtime"

	k8stool "github.com/webcenter-fr/eino-ext/components/tool/kubernetes"
	mcpserver "github.com/webcenter-fr/eino-ext/libs/mcp"
)

// NewServer builds an MCP server exposing all Kubernetes tools (read + write).
// The instance argument is "cluster". Client construction is lazy (no live
// cluster needed at build time).
func NewServer(ctx context.Context, configs k8stool.Configs, scheme *runtime.Scheme, cfg *mcpserver.Config) (*mcpserver.Server, error) {
	tools, err := k8stool.NewAllTools(ctx, configs, scheme)
	if err != nil {
		return nil, emperrors.Wrap(err, "failed to create kubernetes tools")
	}
	if cfg == nil {
		cfg = &mcpserver.Config{}
	}
	if cfg.Name == "" {
		cfg.Name = "kubernetes"
	}
	cfg.Tools = tools
	cfg.WriteTools = k8stool.WriteToolNames()
	cfg.InstanceParam = "cluster"
	return mcpserver.NewServer(ctx, cfg)
}
