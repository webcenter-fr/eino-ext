package pipe

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/webcenter-fr/eino-ext/components/tool/shell"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/confirm"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/marshal"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/toolutil"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
)

const (
	defaultPipeTimeout    = 5 * time.Minute
	defaultMaxOutputBytes = 10 << 20 // 10 MiB
)

// Tool is an eino tool that runs an ordered pipeline of shell and tool steps.
type Tool struct {
	invokable  tool.InvokableTool
	streamable tool.StreamableTool

	shell shellExecutor // interface, implemented by *shell.Tool (faked in tests)
	tools map[string]tool.InvokableTool
	cfg   *Config
}

// shellExecutor is the minimal shell primitive the pipe needs. *shell.Tool
// satisfies it via RawExec; tests supply a fake.
type shellExecutor interface {
	RawExec(ctx context.Context, params shell.RawExecParams) (stdout, stderr string, exitCode int, err error)
}

// Invoke runs the pipeline and returns the final step's output.
func (t *Tool) Invoke(ctx context.Context, params *Params) (string, error) {
	if err := t.validateParams(params); err != nil {
		return "", err
	}
	if params.DryRun {
		return t.dryRunPreview(params), nil
	}
	if err := confirm.RequireConfirmationCtx(ctx, "pipe_exec", params.DryRun, params.Confirmed); err != nil {
		return "", err
	}
	return t.runPipeline(ctx, params)
}

// InvokeAsStream runs the pipeline and streams the final result line by line.
func (t *Tool) InvokeAsStream(ctx context.Context, params *Params) (*schema.StreamReader[string], error) {
	if err := t.validateParams(params); err != nil {
		return nil, err
	}
	if params.DryRun {
		sr, sw := schema.Pipe[string](1)
		sw.Send(t.dryRunPreview(params), nil)
		sw.Close()
		return sr, nil
	}
	if err := confirm.RequireConfirmationCtx(ctx, "pipe_exec", params.DryRun, params.Confirmed); err != nil {
		return nil, err
	}

	result, err := t.runPipeline(ctx, params)
	if err != nil {
		return nil, err
	}

	sr, sw := schema.Pipe[string](100)
	go func() {
		defer sw.Close()
		scanner := bufio.NewScanner(strings.NewReader(result))
		for scanner.Scan() {
			if closed := sw.Send(scanner.Text(), nil); closed {
				return
			}
		}
	}()
	return sr, nil
}

// Info returns metadata about the pipe tool.
func (t *Tool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return t.invokable.Info(ctx)
}

// InvokableRun implements the eino InvokableTool interface.
func (t *Tool) InvokableRun(ctx context.Context, args string, opts ...tool.Option) (string, error) {
	return t.invokable.InvokableRun(ctx, args, opts...)
}

// StreamableRun implements the eino StreamableTool interface.
func (t *Tool) StreamableRun(ctx context.Context, args string, opts ...tool.Option) (*schema.StreamReader[string], error) {
	return t.streamable.StreamableRun(ctx, args, opts...)
}

// Close shuts down the underlying shell tool (and its Dagger client).
func (t *Tool) Close() error {
	if t.shell != nil {
		if c, ok := t.shell.(io.Closer); ok {
			return c.Close()
		}
	}
	return nil
}

func (t *Tool) validateParams(params *Params) error {
	if params == nil {
		return errors.New("params is nil")
	}
	if err := validate.Struct(params); err != nil {
		return err
	}
	if len(params.Steps) == 0 {
		return errors.New("pipeline requires at least one step")
	}
	for i, step := range params.Steps {
		if (step.Shell == nil) == (step.Tool == nil) {
			return errors.Errorf("pipeline step %d must set exactly one of 'shell' or 'tool'", i)
		}
		if step.Shell != nil {
			if err := validate.Struct(step.Shell); err != nil {
				return errors.Wrapf(err, "invalid shell step %d", i)
			}
		}
		if step.Tool != nil {
			if err := validate.Struct(step.Tool); err != nil {
				return errors.Wrapf(err, "invalid tool step %d", i)
			}
		}
	}
	return nil
}

func (t *Tool) runPipeline(ctx context.Context, params *Params) (string, error) {
	timeout := t.cfg.DefaultTimeout
	if params.Timeout != "" {
		if d, err := time.ParseDuration(params.Timeout); err == nil {
			timeout = d
		}
	}
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	input := "" // initial stdin for step 0

	for i, step := range params.Steps {
		switch {
		case step.Shell != nil:
			out, err := t.runShellStep(execCtx, params, step.Shell, input)
			if err != nil {
				return "", errors.Wrapf(err, "pipeline step %d (shell) failed", i)
			}
			input = out
		case step.Tool != nil:
			out, err := t.runToolStep(execCtx, step.Tool, input)
			if err != nil {
				return "", errors.Wrapf(err, "pipeline step %d (tool %q) failed", i, step.Tool.Name)
			}
			input = out
		}
		if len(input) > t.cfg.MaxOutputBytes {
			return "", errors.Errorf("pipeline step %d output exceeds MaxOutputBytes (%d > %d)",
				i, len(input), t.cfg.MaxOutputBytes)
		}
	}
	return input, nil
}

func (t *Tool) runShellStep(ctx context.Context, params *Params, step *ShellStep, stdin string) (string, error) {
	profile := step.Profile
	if profile == "" {
		profile = params.Profile
	}
	stdout, stderr, code, err := t.shell.RawExec(ctx, shell.RawExecParams{
		Command: step.Command,
		Profile: profile,
		Stdin:   stdin,
		Timeout: t.cfg.DefaultTimeout,
	})
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", errors.Errorf("command %v exited with code %d: %s",
			step.Command, code, strings.TrimSpace(stderr))
	}
	// On success, stderr is informational and is intentionally dropped so it
	// does not corrupt the next step's stdin.
	return stdout, nil
}

func (t *Tool) runToolStep(ctx context.Context, step *ToolStep, stdin string) (string, error) {
	tl, ok := t.tools[step.Name]
	if !ok {
		return "", toolutil.NotFoundError("pipe tool", step.Name, toolutil.SortedKeys(t.tools))
	}

	argsJSON := stdin
	if step.Args != nil {
		b, err := json.Marshal(step.Args)
		if err != nil {
			return "", errors.Wrap(err, "failed to marshal tool args")
		}
		argsJSON = string(b)
	}

	return tl.InvokableRun(ctx, argsJSON)
}

func (t *Tool) dryRunPreview(params *Params) string {
	steps := make([]map[string]any, 0, len(params.Steps))
	for _, s := range params.Steps {
		switch {
		case s.Shell != nil:
			steps = append(steps, map[string]any{"type": "shell", "command": s.Shell.Command})
		case s.Tool != nil:
			steps = append(steps, map[string]any{"type": "tool", "name": s.Tool.Name, "args": s.Tool.Args})
		}
	}
	return fmt.Sprintf(`{"dryRun": true, "steps": %s}`, string(marshal.MustMarshal(steps)))
}
