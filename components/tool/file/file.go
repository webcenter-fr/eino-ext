// Package file provides eino tools for local filesystem file operations
// within a session-scoped temporary directory. Tools include read, write,
// delete, copy, and move.
//
// Usage:
//
//	cfg := &file.Config{Workdir: "/tmp/eino-files"}
//	tools, err := file.NewAllTools(ctx, cfg)
package file

import (
	"context"

	"github.com/cloudwego/eino/components/tool"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/fileutil"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
)

// FileSessionKey is the adk session-value key for the per-user-session
// file namespace.
const FileSessionKey = "file_session_id"

// newConfig applies defaults and validates the configuration, returning the
// validated config or an error.
func newConfig(cfg *Config) (*Config, error) {
	if cfg == nil {
		cfg = &Config{}
	}
	if cfg.MaxReadBytes == 0 {
		cfg.MaxReadBytes = fileutil.DefaultMaxReadBytes
	}
	if cfg.MaxWriteBytes == 0 {
		cfg.MaxWriteBytes = fileutil.DefaultMaxWriteBytes
	}
	if err := validate.Struct(cfg); err != nil {
		return nil, err
	}
	if err := fileutil.ValidateRootDir(cfg.Workdir); err != nil {
		return nil, err
	}
	return cfg, nil
}

// NewAllTools creates all file tools (read, write, delete, copy, move) and
// returns them as a flat slice ready to be registered with an eino ToolsNode.
func NewAllTools(ctx context.Context, cfg *Config) ([]tool.InvokableTool, error) {
	cfg, err := newConfig(cfg)
	if err != nil {
		return nil, err
	}

	tools := []tool.InvokableTool{}

	readTool, err := NewReadTool(ctx, cfg)
	if err != nil {
		return nil, err
	}
	tools = append(tools, readTool)

	writeTool, err := NewWriteTool(ctx, cfg)
	if err != nil {
		return nil, err
	}
	tools = append(tools, writeTool)

	deleteTool, err := NewDeleteTool(ctx, cfg)
	if err != nil {
		return nil, err
	}
	tools = append(tools, deleteTool)

	copyTool, err := NewCopyTool(ctx, cfg)
	if err != nil {
		return nil, err
	}
	tools = append(tools, copyTool)

	moveTool, err := NewMoveTool(ctx, cfg)
	if err != nil {
		return nil, err
	}
	tools = append(tools, moveTool)

	return tools, nil
}

// NewReadOnlyTools creates only the read-only file tools (file_read).
func NewReadOnlyTools(ctx context.Context, cfg *Config) ([]tool.InvokableTool, error) {
	cfg, err := newConfig(cfg)
	if err != nil {
		return nil, err
	}

	readTool, err := NewReadTool(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return []tool.InvokableTool{readTool}, nil
}

// WriteToolNames returns the tool names of all file write tools.
func WriteToolNames() []string {
	return []string{"file_write", "file_delete", "file_copy", "file_move"}
}
