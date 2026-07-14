// Package shell provides an eino shell tool backed by the Dagger engine.
// It gives LLM agents a secure, sandboxed shell inside an OCI base image
// (golang, node, python, etc.) with shared caching across agents.
//
// The tool implements both tool.InvokableTool and tool.StreamableTool.
//
// Usage:
//
//	cfg := &shell.Config{
//	    Workdir: "/path/to/project",
//	}
//	shellTool, err := shell.NewShellTool(ctx, cfg)
//
//	// With safety middleware:
//	tools, mw, err := shell.NewAllToolsWithSafety(ctx, cfg, nil)
//	agent, _ := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
//	    Name:     "shell-agent",
//	    Model:    myModel,
//	    Tools:    tools,
//	    Handlers: []adk.ChatModelAgentMiddleware{mw},
//	})
//
// Requirements:
//   - A reachable Dagger engine (local or remote).
//   - The engine URL can be set via DAGGER_HOST env var or EngineURL config.
//
// Security:
//   - Command blocklist prevents destructive commands (rm, kill, etc.).
//   - Egress network policy restricts outbound traffic.
//   - Safety middleware gates all executions behind dry-run/confirmed flow.
package shell
