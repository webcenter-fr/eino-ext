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
	"github.com/webcenter-fr/eino-ext/libs/toolkit/marshal"
	toolkitsafety "github.com/webcenter-fr/eino-ext/libs/toolkit/safety"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/toolutil"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
)

const (
	defaultPipeTimeout    = 5 * time.Minute
	defaultMaxOutputBytes = 10 << 20 // 10 MiB
)

// writeStepDryRunGuidance is appended to the pipeline result when at least one
// write tool step ran in dry-run mode, instructing the LLM to present the
// preview to the user and request confirmation.
const writeStepDryRunGuidance = "\n\nDRY-RUN RESULT: this pipeline preview includes a write tool step that ran in dry-run mode. Show this preview to the user and ask for confirmation, then re-call the pipeline with confirmed=true in that write tool step's args."

// Tool is an eino tool that runs an ordered pipeline of shell and tool steps.
type Tool struct {
	invokable  tool.InvokableTool
	streamable tool.StreamableTool

	shell      shellExecutor // interface, implemented by *shell.Tool (faked in tests)
	tools      map[string]tool.InvokableTool
	writeTools map[string]bool
	cfg        *Config
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

	result, err := t.runPipeline(ctx, params)
	if err != nil {
		return nil, err
	}

	sr, sw := schema.Pipe[string](100)
	go func() {
		defer sw.Close()
		if result == "" {
			// Never leave the stream empty: eino's compose fails to
			// concatenate zero-chunk streams ("stream reader is empty,
			// concat fail"), so emit a single empty chunk instead.
			sw.Send("", nil)
			return
		}
		scanner := bufio.NewScanner(strings.NewReader(result))
		// The result is capped at MaxOutputBytes, so a scanner maximum of
		// MaxOutputBytes+1 never truncates; the default 64KB token limit
		// would silently drop the tail of long lines (e.g. `jq -c` output).
		scanner.Buffer(make([]byte, 0, 64*1024), t.cfg.MaxOutputBytes+1)
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
	sawWriteDryRun := false

	for i, step := range params.Steps {
		switch {
		case step.Shell != nil:
			out, err := t.runShellStep(execCtx, params, step.Shell, input)
			if err != nil {
				return "", errors.Wrapf(err, "pipeline step %d (shell) failed", i)
			}
			input = out
		case step.Tool != nil:
			out, dryRunWrite, err := t.runToolStep(execCtx, step.Tool, input)
			if err != nil {
				return "", errors.Wrapf(err, "pipeline step %d (tool %q) failed", i, step.Tool.Name)
			}
			sawWriteDryRun = sawWriteDryRun || dryRunWrite
			input = out
		}
		if len(input) > t.cfg.MaxOutputBytes {
			return "", errors.Errorf("pipeline step %d output exceeds MaxOutputBytes (%d > %d)",
				i, len(input), t.cfg.MaxOutputBytes)
		}
	}
	if sawWriteDryRun {
		input += writeStepDryRunGuidance
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

// runToolStep invokes a registered tool. It returns the tool output and a
// flag indicating whether the step was a write tool that ran in dry-run mode
// (so the pipeline result can carry the confirmation guidance).
//
// Tools listed in Config.WriteToolNames are gated like the safety middleware:
// the step's args must carry dryRun=true (preview) or confirmed=true, and real
// execution additionally requires host authorization through the configured
// ExecutionAuthorizer. On grant, the inner tool's name is marked authorized on
// the context so its own confirmation layer passes.
func (t *Tool) runToolStep(ctx context.Context, step *ToolStep, stdin string) (string, bool, error) {
	tl, ok := t.tools[step.Name]
	if !ok {
		return "", false, toolutil.NotFoundError("pipe tool", step.Name, toolutil.SortedKeys(t.tools))
	}

	argsJSON := stdin
	if step.Args != nil {
		b, err := json.Marshal(step.Args)
		if err != nil {
			return "", false, errors.Wrap(err, "failed to marshal tool args")
		}
		argsJSON = string(b)
	} else if argsJSON == "" {
		// No piped input (e.g. this is the first step) and no explicit args:
		// pass an empty JSON object. InferTool-based tools unmarshal their
		// arguments with sonic, which rejects the empty string.
		argsJSON = "{}"
	}

	execCtx := ctx
	if t.writeTools[step.Name] {
		gp, err := toolkitsafety.ExtractGateParams(argsJSON)
		if err != nil {
			return "", false, errors.Wrapf(err, "failed to parse dryRun/confirmed args for write tool %q", step.Name)
		}
		var gateErr error
		if t.cfg.AllowModelConfirmation {
			switch {
			case gp.DryRun:
			case gp.Confirmed:
			default:
				gateErr = toolkitsafety.ErrGateRequired
			}
		} else {
			gateErr = toolkitsafety.ShouldGateWithAuthorization(ctx, step.Name, t.writeTools, gp, json.RawMessage(argsJSON), t.cfg.ExecutionAuthorizer)
		}
		if gateErr != nil {
			return "", false, gateErr
		}
		if gp.DryRun {
			out, err := tl.InvokableRun(execCtx, argsJSON)
			return out, true, err
		}
		// Real execution authorized: mark the inner tool name on the context so
		// its own confirm.RequireConfirmationCtx second layer sees the grant.
		execCtx = toolkitsafety.WithExecutionAuthorized(ctx, step.Name)
	}

	out, err := tl.InvokableRun(execCtx, argsJSON)
	if err != nil {
		return "", false, err
	}
	return out, false, nil
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
