package pipe

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/webcenter-fr/eino-ext/components/tool/shell"
	toolkitsafety "github.com/webcenter-fr/eino-ext/libs/toolkit/safety"
)

type fakeResult struct {
	stdout string
	stderr string
	code   int
	err    error
}

type fakeShell struct {
	mu    sync.Mutex
	calls []shell.RawExecParams
	fn    func(p shell.RawExecParams) fakeResult
}

func (f *fakeShell) RawExec(_ context.Context, p shell.RawExecParams) (string, string, int, error) {
	f.mu.Lock()
	f.calls = append(f.calls, p)
	f.mu.Unlock()

	if f.fn != nil {
		r := f.fn(p)
		return r.stdout, r.stderr, r.code, r.err
	}
	// Default: behave like `cat`, echoing stdin to stdout.
	return p.Stdin, "", 0, nil
}

func (f *fakeShell) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

type fakeTool struct {
	fn func(ctx context.Context, args string) (string, error)
}

func (f *fakeTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: "fake"}, nil
}

func (f *fakeTool) InvokableRun(ctx context.Context, args string, opts ...tool.Option) (string, error) {
	return f.fn(ctx, args)
}

func newTestTool(s shellExecutor, tools map[string]tool.InvokableTool, cfg *Config) *Tool {
	if cfg == nil {
		cfg = &Config{}
	}
	if cfg.DefaultTimeout == 0 {
		cfg.DefaultTimeout = defaultPipeTimeout
	}
	if cfg.MaxOutputBytes <= 0 {
		cfg.MaxOutputBytes = defaultMaxOutputBytes
	}
	return &Tool{shell: s, tools: tools, writeTools: makeWriteToolsMap(cfg.WriteToolNames), cfg: cfg}
}

func TestPipeInvoke(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *Config
		tools   map[string]tool.InvokableTool
		shellFn func(p shell.RawExecParams) fakeResult
		params  *Params
		wantOut string
		wantErr string
		check   func(t *testing.T, f *fakeShell)
	}{
		{
			name: "tool stdout piped to shell stdin",
			tools: map[string]tool.InvokableTool{
				"echo": &fakeTool{fn: func(context.Context, string) (string, error) {
					return "hello\nworld", nil
				}},
			},
			params: &Params{

				Steps: []Step{
					{Tool: &ToolStep{Name: "echo", Args: map[string]any{"x": 1}}},
					{Shell: &ShellStep{Command: []string{"cat"}}},
				},
			},
			wantOut: "hello\nworld",
			check: func(t *testing.T, f *fakeShell) {
				require.Len(t, f.calls, 1)
				assert.Equal(t, "hello\nworld", f.calls[0].Stdin)
			},
		},
		{
			name: "shell stdout piped to tool raw JSON input",
			tools: map[string]tool.InvokableTool{
				"echo": &fakeTool{fn: func(_ context.Context, args string) (string, error) {
					return args, nil
				}},
			},
			shellFn: func(shell.RawExecParams) fakeResult {
				return fakeResult{stdout: `{"x":1}`}
			},
			params: &Params{

				Steps: []Step{
					{Shell: &ShellStep{Command: []string{"emit-json"}}},
					{Tool: &ToolStep{Name: "echo"}},
				},
			},
			wantOut: `{"x":1}`,
		},
		{
			name: "explicit tool args override previous stdout",
			tools: map[string]tool.InvokableTool{
				"echo": &fakeTool{fn: func(_ context.Context, args string) (string, error) {
					return args, nil
				}},
			},
			shellFn: func(shell.RawExecParams) fakeResult {
				return fakeResult{stdout: "previous"}
			},
			params: &Params{

				Steps: []Step{
					{Shell: &ShellStep{Command: []string{"emit"}}},
					{Tool: &ToolStep{Name: "echo", Args: map[string]any{"a": "b"}}},
				},
			},
			wantOut: `{"a":"b"}`,
		},
		{
			name: "single shell step returns stdout",
			shellFn: func(shell.RawExecParams) fakeResult {
				return fakeResult{stdout: "single"}
			},
			params: &Params{

				Steps: []Step{{Shell: &ShellStep{Command: []string{"echo", "single"}}}},
			},
			wantOut: "single",
		},
		{
			name:    "empty steps fails validation",
			params:  &Params{},
			wantErr: "steps",
		},
		{
			name: "both shell and tool set fails",
			params: &Params{

				Steps: []Step{{
					Shell: &ShellStep{Command: []string{"echo"}},
					Tool:  &ToolStep{Name: "x"},
				}},
			},
			wantErr: "exactly one",
		},
		{
			name: "neither shell nor tool set fails",
			params: &Params{

				Steps: []Step{{}},
			},
			wantErr: "exactly one",
		},
		{
			name: "unknown tool name lists available tools",
			tools: map[string]tool.InvokableTool{
				"known": &fakeTool{fn: func(context.Context, string) (string, error) { return "", nil }},
			},
			params: &Params{

				Steps: []Step{{Tool: &ToolStep{Name: "nope"}}},
			},
			wantErr: "not found",
		},
		{
			name: "non-zero shell exit aborts with stderr",
			shellFn: func(shell.RawExecParams) fakeResult {
				return fakeResult{stdout: "partial", stderr: "boom", code: 1}
			},
			params: &Params{

				Steps: []Step{{Shell: &ShellStep{Command: []string{"false"}}}},
			},
			wantErr: "exited with code 1",
		},
		{
			name: "tool error is wrapped with step index and name",
			tools: map[string]tool.InvokableTool{
				"bad": &fakeTool{fn: func(context.Context, string) (string, error) {
					return "", assert.AnError
				}},
			},
			params: &Params{

				Steps: []Step{{Tool: &ToolStep{Name: "bad", Args: map[string]any{}}}},
			},
			wantErr: "step 0",
		},
		{
			name: "max output bytes exceeded",
			cfg:  &Config{MaxOutputBytes: 3},
			shellFn: func(shell.RawExecParams) fakeResult {
				return fakeResult{stdout: "toolong"}
			},
			params: &Params{

				Steps: []Step{{Shell: &ShellStep{Command: []string{"echo"}}}},
			},
			wantErr: "MaxOutputBytes",
		},
		{
			name: "dry run previews without executing",
			shellFn: func(shell.RawExecParams) fakeResult {
				return fakeResult{stdout: "should-not-run"}
			},
			params: &Params{
				DryRun: true,
				Steps:  []Step{{Shell: &ShellStep{Command: []string{"echo"}}}},
			},
			wantOut: `"dryRun": true`,
			check: func(t *testing.T, f *fakeShell) {
				assert.Zero(t, f.callCount())
			},
		},
		{
			name: "invalid timeout falls back to default",
			shellFn: func(shell.RawExecParams) fakeResult {
				return fakeResult{stdout: "ok"}
			},
			params: &Params{

				Timeout: "not-a-duration",
				Steps:   []Step{{Shell: &ShellStep{Command: []string{"echo"}}}},
			},
			wantOut: "ok",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeShell{fn: tt.shellFn}
			pipeTool := newTestTool(f, tt.tools, tt.cfg)

			out, err := pipeTool.Invoke(context.Background(), tt.params)

			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Contains(t, out, tt.wantOut)
			if tt.check != nil {
				tt.check(t, f)
			}
		})
	}
}

func TestPipeUnknownToolListsAvailable(t *testing.T) {
	pipeTool := newTestTool(&fakeShell{}, map[string]tool.InvokableTool{
		"alpha": &fakeTool{fn: func(context.Context, string) (string, error) { return "", nil }},
		"beta":  &fakeTool{fn: func(context.Context, string) (string, error) { return "", nil }},
	}, nil)

	_, err := pipeTool.Invoke(context.Background(), &Params{

		Steps: []Step{{Tool: &ToolStep{Name: "missing"}}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
	assert.Contains(t, err.Error(), "alpha")
	assert.Contains(t, err.Error(), "beta")
}

// innerParams is the argument type of the real InferTool tool used below.
type innerParams struct {
	Value string `json:"value"`
}

func TestPipeToolStepWithoutArgs(t *testing.T) {
	t.Run("first step without args receives an empty JSON object", func(t *testing.T) {
		var gotArgs string
		pipeTool := newTestTool(&fakeShell{}, map[string]tool.InvokableTool{
			"echo": &fakeTool{fn: func(_ context.Context, args string) (string, error) {
				gotArgs = args
				return args, nil
			}},
		}, nil)

		out, err := pipeTool.Invoke(context.Background(), &Params{

			Steps: []Step{{Tool: &ToolStep{Name: "echo"}}},
		})
		require.NoError(t, err)
		assert.Equal(t, "{}", gotArgs)
		assert.Equal(t, "{}", out)
	})

	t.Run("real InferTool tool accepts the synthesized args", func(t *testing.T) {
		// eino's InferTool unmarshals arguments with sonic, which rejects the
		// empty string; this proves the synthesized "{}" actually unmarshals.
		inner, err := utils.InferTool("inner", "inner", func(_ context.Context, p *innerParams) (string, error) {
			return "value=" + p.Value, nil
		})
		require.NoError(t, err)

		pipeTool := newTestTool(&fakeShell{}, map[string]tool.InvokableTool{"inner": inner}, nil)
		out, err := pipeTool.Invoke(context.Background(), &Params{

			Steps: []Step{{Tool: &ToolStep{Name: "inner"}}},
		})
		require.NoError(t, err)
		assert.Equal(t, "value=", out)
	})
}

func TestPipeInvokeAsStream(t *testing.T) {
	f := &fakeShell{fn: func(shell.RawExecParams) fakeResult {
		return fakeResult{stdout: "line1\nline2\nline3"}
	}}
	pipeTool := newTestTool(f, nil, nil)

	sr, err := pipeTool.InvokeAsStream(context.Background(), &Params{

		Steps: []Step{{Shell: &ShellStep{Command: []string{"echo"}}}},
	})
	require.NoError(t, err)
	defer sr.Close()

	var lines []string
	for {
		chunk, recvErr := sr.Recv()
		if recvErr != nil {
			break
		}
		lines = append(lines, chunk)
	}
	assert.Equal(t, []string{"line1", "line2", "line3"}, lines)
}

func TestPipeInvokeAsStreamDryRun(t *testing.T) {
	f := &fakeShell{}
	pipeTool := newTestTool(f, nil, nil)

	sr, err := pipeTool.InvokeAsStream(context.Background(), &Params{
		DryRun: true,
		Steps:  []Step{{Shell: &ShellStep{Command: []string{"echo"}}}},
	})
	require.NoError(t, err)
	defer sr.Close()

	var out strings.Builder
	for {
		chunk, recvErr := sr.Recv()
		if recvErr != nil {
			break
		}
		out.WriteString(chunk)
	}
	assert.Contains(t, out.String(), `"dryRun": true`)
	assert.Zero(t, f.callCount())
}

func TestPipeInvokeAsStreamLongLine(t *testing.T) {
	// 128KB exceeds bufio.Scanner's default 64KB token limit: the stream must
	// not silently truncate it.
	long := strings.Repeat("x", 128*1024)
	f := &fakeShell{fn: func(shell.RawExecParams) fakeResult {
		return fakeResult{stdout: long}
	}}
	pipeTool := newTestTool(f, nil, nil)

	sr, err := pipeTool.InvokeAsStream(context.Background(), &Params{

		Steps: []Step{{Shell: &ShellStep{Command: []string{"echo"}}}},
	})
	require.NoError(t, err)
	defer sr.Close()

	var out strings.Builder
	for {
		chunk, recvErr := sr.Recv()
		if recvErr != nil {
			break
		}
		out.WriteString(chunk)
	}
	assert.Equal(t, long, out.String())
}

func TestPipeInvokeAsStreamEmptyResult(t *testing.T) {
	f := &fakeShell{fn: func(shell.RawExecParams) fakeResult {
		return fakeResult{stdout: ""}
	}}
	pipeTool := newTestTool(f, nil, nil)

	sr, err := pipeTool.InvokeAsStream(context.Background(), &Params{

		Steps: []Step{{Shell: &ShellStep{Command: []string{"true"}}}},
	})
	require.NoError(t, err)
	defer sr.Close()

	var chunks []string
	for {
		chunk, recvErr := sr.Recv()
		if recvErr != nil {
			break
		}
		chunks = append(chunks, chunk)
	}
	// Exactly one (empty) chunk: eino's compose fails to concatenate
	// zero-chunk streams ("stream reader is empty, concat fail").
	assert.Equal(t, []string{""}, chunks)
}

func TestPipeClose(t *testing.T) {
	t.Run("nil shell is a no-op", func(t *testing.T) {
		pipeTool := &Tool{}
		assert.NoError(t, pipeTool.Close())
	})

	t.Run("closes a closable shell", func(t *testing.T) {
		c := &closableShell{}
		pipeTool := &Tool{shell: c}
		require.NoError(t, pipeTool.Close())
		assert.True(t, c.closed)
	})
}

type closableShell struct {
	fakeShell
	closed bool
}

func (c *closableShell) Close() error {
	c.closed = true
	return nil
}

func TestWriteToolNames(t *testing.T) {
	names := WriteToolNames()
	assert.Empty(t, names)
}

// fakeAuthorizer approves the listed tool names and denies everything else,
// recording the names it was consulted for.
type fakeAuthorizer struct {
	approve map[string]bool
	denyErr error
	calls   []string
}

func (f *fakeAuthorizer) AuthorizeExecute(_ context.Context, toolName string, _ json.RawMessage) error {
	f.calls = append(f.calls, toolName)
	if f.denyErr != nil {
		return f.denyErr
	}
	if f.approve[toolName] {
		return nil
	}
	return assert.AnError
}

func TestPipeWriteToolStepGate(t *testing.T) {
	gatedCfg := func(auth toolkitsafety.ExecutionAuthorizer) *Config {
		return &Config{
			DefaultTimeout:      defaultPipeTimeout,
			MaxOutputBytes:      defaultMaxOutputBytes,
			WriteToolNames:      []string{"kw"},
			ExecutionAuthorizer: auth,
		}
	}

	t.Run("unlisted write tool fails closed via its own authorization", func(t *testing.T) {
		// No WriteToolNames/authorizer: the pipe passes the args through and
		// the inner tool's own confirmation layer finds no scope grant.
		pipeTool := newTestTool(&fakeShell{}, map[string]tool.InvokableTool{
			"kw": &fakeTool{fn: func(ctx context.Context, args string) (string, error) {
				if !toolkitsafety.ExecutionAuthorizedFor(ctx, "kw") {
					return "", toolkitsafety.ErrExecutionNotAuthorized
				}
				return "mutated", nil
			}},
		}, nil)

		_, err := pipeTool.Invoke(context.Background(), &Params{
			Steps: []Step{{Tool: &ToolStep{Name: "kw", Args: map[string]any{"confirmed": true}}}},
		})
		require.ErrorIs(t, err, toolkitsafety.ErrExecutionNotAuthorized)
	})

	t.Run("dry-run write step runs preview and appends guidance", func(t *testing.T) {
		pipeTool := newTestTool(&fakeShell{}, map[string]tool.InvokableTool{
			"kw": &fakeTool{fn: func(_ context.Context, args string) (string, error) {
				return "preview:" + args, nil
			}},
		}, gatedCfg(nil))

		out, err := pipeTool.Invoke(context.Background(), &Params{
			Steps: []Step{{Tool: &ToolStep{Name: "kw", Args: map[string]any{"dryRun": true}}}},
		})
		require.NoError(t, err)
		assert.Contains(t, out, `"dryRun":true`)
		assert.Contains(t, out, "DRY-RUN RESULT")
	})

	t.Run("confirmed without authorizer fails closed", func(t *testing.T) {
		pipeTool := newTestTool(&fakeShell{}, map[string]tool.InvokableTool{
			"kw": &fakeTool{fn: func(context.Context, string) (string, error) { return "", nil }},
		}, gatedCfg(nil))

		_, err := pipeTool.Invoke(context.Background(), &Params{
			Steps: []Step{{Tool: &ToolStep{Name: "kw", Args: map[string]any{"confirmed": true}}}},
		})
		require.ErrorIs(t, err, toolkitsafety.ErrExecutionNotAuthorized)
	})

	t.Run("neither dryRun nor confirmed requires the gate", func(t *testing.T) {
		pipeTool := newTestTool(&fakeShell{}, map[string]tool.InvokableTool{
			"kw": &fakeTool{fn: func(context.Context, string) (string, error) { return "", nil }},
		}, gatedCfg(nil))

		_, err := pipeTool.Invoke(context.Background(), &Params{
			Steps: []Step{{Tool: &ToolStep{Name: "kw", Args: map[string]any{}}}},
		})
		require.ErrorIs(t, err, toolkitsafety.ErrGateRequired)
	})

	t.Run("confirmed with approving authorizer executes with authorization", func(t *testing.T) {
		var innerAuthorized bool
		pipeTool := newTestTool(&fakeShell{}, map[string]tool.InvokableTool{
			"kw": &fakeTool{fn: func(ctx context.Context, args string) (string, error) {
				innerAuthorized = toolkitsafety.ExecutionAuthorizedFor(ctx, "kw")
				return "done:" + args, nil
			}},
		}, gatedCfg(&fakeAuthorizer{approve: map[string]bool{"kw": true}}))

		out, err := pipeTool.Invoke(context.Background(), &Params{
			Steps: []Step{{Tool: &ToolStep{Name: "kw", Args: map[string]any{"confirmed": true}}}},
		})
		require.NoError(t, err)
		assert.True(t, innerAuthorized)
		assert.Contains(t, out, "done:")
	})

	t.Run("confirmed with denying authorizer fails", func(t *testing.T) {
		pipeTool := newTestTool(&fakeShell{}, map[string]tool.InvokableTool{
			"kw": &fakeTool{fn: func(context.Context, string) (string, error) { return "", nil }},
		}, gatedCfg(&fakeAuthorizer{denyErr: assert.AnError}))

		_, err := pipeTool.Invoke(context.Background(), &Params{
			Steps: []Step{{Tool: &ToolStep{Name: "kw", Args: map[string]any{"confirmed": true}}}},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not authorized")
	})

	t.Run("allow model confirmation skips the authorizer", func(t *testing.T) {
		cfg := gatedCfg(nil)
		cfg.AllowModelConfirmation = true
		var innerAuthorized bool
		pipeTool := newTestTool(&fakeShell{}, map[string]tool.InvokableTool{
			"kw": &fakeTool{fn: func(ctx context.Context, args string) (string, error) {
				innerAuthorized = toolkitsafety.ExecutionAuthorizedFor(ctx, "kw")
				return "ok", nil
			}},
		}, cfg)

		out, err := pipeTool.Invoke(context.Background(), &Params{
			Steps: []Step{{Tool: &ToolStep{Name: "kw", Args: map[string]any{"confirmed": true}}}},
		})
		require.NoError(t, err)
		assert.True(t, innerAuthorized)
		assert.Equal(t, "ok", out)
	})

	t.Run("non-listed tools run ungated", func(t *testing.T) {
		pipeTool := newTestTool(&fakeShell{}, map[string]tool.InvokableTool{
			"ro": &fakeTool{fn: func(context.Context, string) (string, error) { return "read", nil }},
		}, gatedCfg(nil))

		out, err := pipeTool.Invoke(context.Background(), &Params{
			Steps: []Step{{Tool: &ToolStep{Name: "ro", Args: map[string]any{}}}},
		})
		require.NoError(t, err)
		assert.Equal(t, "read", out)
	})
}
