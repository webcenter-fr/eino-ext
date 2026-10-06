package pipe

import (
	"context"
	_ "embed"
	"io"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"

	safetymw "github.com/webcenter-fr/eino-ext/components/middleware/safety"
	"github.com/webcenter-fr/eino-ext/components/tool/shell"
	toolkitsafety "github.com/webcenter-fr/eino-ext/libs/toolkit/safety"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
)

//go:embed prompts/pipe_description.md
var pipeDescription string

var (
	_ tool.InvokableTool  = (*Tool)(nil)
	_ tool.StreamableTool = (*Tool)(nil)
	_ io.Closer           = (*Tool)(nil)
)

// NewPipeTool creates a new pipe Tool from the given configuration.
func NewPipeTool(ctx context.Context, cfg *Config) (*Tool, error) {
	if cfg == nil {
		cfg = &Config{}
	}
	if cfg.DefaultTimeout == 0 {
		cfg.DefaultTimeout = defaultPipeTimeout
	}
	if cfg.MaxOutputBytes <= 0 {
		cfg.MaxOutputBytes = defaultMaxOutputBytes
	}
	if err := validate.Struct(cfg); err != nil {
		return nil, err
	}

	shellTool, err := shell.NewShellTool(ctx, cfg.Shell)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create underlying shell tool")
	}

	t := &Tool{
		shell:      shellTool,
		tools:      cfg.Tools,
		writeTools: toolkitsafety.NewWriteToolSet(cfg.WriteToolNames),
		cfg:        cfg,
	}

	invokable, err := utils.InferTool("pipe_exec", pipeDescription, t.Invoke)
	if err != nil {
		_ = shellTool.Close()
		return nil, errors.Wrap(err, "failed to create invokable tool")
	}
	t.invokable = invokable

	streamable, err := utils.InferStreamTool("pipe_exec", pipeDescription, t.InvokeAsStream)
	if err != nil {
		_ = shellTool.Close()
		return nil, errors.Wrap(err, "failed to create streamable tool")
	}
	t.streamable = streamable

	return t, nil
}

// WriteToolNames returns the tool names of all pipe write tools.
//
// pipe_exec is intentionally NOT a write tool: its shell steps run in the
// isolated Dagger sandbox and its tool steps invoke read-only registered tools,
// so it is not gated by the safety middleware. (The command blocklist is still
// enforced on every shell step, and write tools used as tool steps are gated
// per-step via Config.WriteToolNames + Config.ExecutionAuthorizer.)
func WriteToolNames() []string {
	return nil
}

// NewAllToolsWithSafety creates the pipe tool with a pre-configured safety middleware.
// The middleware's ExecutionAuthorizer and AllowModelConfirmation are forwarded
// to the pipe config when unset, so write tool steps gate on the same host
// approval mechanism as top-level write tools.
func NewAllToolsWithSafety(ctx context.Context, cfg *Config, safetyCfg *safetymw.Config) ([]tool.InvokableTool, *safetymw.Middleware, error) {
	if cfg == nil {
		cfg = &Config{}
	}
	if safetyCfg == nil {
		safetyCfg = &safetymw.Config{}
	}
	if cfg.ExecutionAuthorizer == nil {
		cfg.ExecutionAuthorizer = safetyCfg.ExecutionAuthorizer
	}
	if !cfg.AllowModelConfirmation {
		cfg.AllowModelConfirmation = safetyCfg.AllowModelConfirmation
	}

	pipeTool, err := NewPipeTool(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}

	tools := []tool.InvokableTool{pipeTool}

	if len(safetyCfg.WriteToolNames) == 0 {
		safetyCfg.WriteToolNames = WriteToolNames()
	}

	mw, err := safetymw.New(safetyCfg)
	if err != nil {
		return nil, nil, err
	}

	return tools, mw, nil
}
