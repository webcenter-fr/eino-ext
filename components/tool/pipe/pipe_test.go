package pipe

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/cloudwego/eino/components/tool"
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
	return &Tool{shell: s, tools: tools, cfg: cfg}
}

func authorizedCtx() context.Context {
	return toolkitsafety.WithExecutionAuthorized(context.Background(), "pipe_exec")
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
				Confirmed: true,
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
				Confirmed: true,
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
				Confirmed: true,
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
				Confirmed: true,
				Steps:     []Step{{Shell: &ShellStep{Command: []string{"echo", "single"}}}},
			},
			wantOut: "single",
		},
		{
			name:    "empty steps fails validation",
			params:  &Params{Confirmed: true},
			wantErr: "steps",
		},
		{
			name: "both shell and tool set fails",
			params: &Params{
				Confirmed: true,
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
				Confirmed: true,
				Steps:     []Step{{}},
			},
			wantErr: "exactly one",
		},
		{
			name: "unknown tool name lists available tools",
			tools: map[string]tool.InvokableTool{
				"known": &fakeTool{fn: func(context.Context, string) (string, error) { return "", nil }},
			},
			params: &Params{
				Confirmed: true,
				Steps:     []Step{{Tool: &ToolStep{Name: "nope"}}},
			},
			wantErr: "not found",
		},
		{
			name: "non-zero shell exit aborts with stderr",
			shellFn: func(shell.RawExecParams) fakeResult {
				return fakeResult{stdout: "partial", stderr: "boom", code: 1}
			},
			params: &Params{
				Confirmed: true,
				Steps:     []Step{{Shell: &ShellStep{Command: []string{"false"}}}},
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
				Confirmed: true,
				Steps:     []Step{{Tool: &ToolStep{Name: "bad", Args: map[string]any{}}}},
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
				Confirmed: true,
				Steps:     []Step{{Shell: &ShellStep{Command: []string{"echo"}}}},
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
				Confirmed: true,
				Timeout:   "not-a-duration",
				Steps:     []Step{{Shell: &ShellStep{Command: []string{"echo"}}}},
			},
			wantOut: "ok",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeShell{fn: tt.shellFn}
			pipeTool := newTestTool(f, tt.tools, tt.cfg)

			out, err := pipeTool.Invoke(authorizedCtx(), tt.params)

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

	_, err := pipeTool.Invoke(authorizedCtx(), &Params{
		Confirmed: true,
		Steps:     []Step{{Tool: &ToolStep{Name: "missing"}}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
	assert.Contains(t, err.Error(), "alpha")
	assert.Contains(t, err.Error(), "beta")
}

func TestPipeInvokeConfirmation(t *testing.T) {
	f := &fakeShell{}
	pipeTool := newTestTool(f, nil, nil)
	params := &Params{
		Steps: []Step{{Shell: &ShellStep{Command: []string{"echo"}}}},
	}

	t.Run("not confirmed fails", func(t *testing.T) {
		_, err := pipeTool.Invoke(context.Background(), params)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "confirmed must be true")
		assert.Zero(t, f.callCount())
	})

	t.Run("confirmed without authorization fails closed", func(t *testing.T) {
		p := *params
		p.Confirmed = true
		_, err := pipeTool.Invoke(context.Background(), &p)
		require.ErrorIs(t, err, toolkitsafety.ErrExecutionNotAuthorized)
		assert.Zero(t, f.callCount())
	})

	t.Run("confirmed with authorization runs", func(t *testing.T) {
		p := *params
		p.Confirmed = true
		out, err := pipeTool.Invoke(authorizedCtx(), &p)
		require.NoError(t, err)
		assert.Equal(t, "", out)
		assert.Equal(t, 1, f.callCount())
	})
}

func TestPipeInvokeAsStream(t *testing.T) {
	f := &fakeShell{fn: func(shell.RawExecParams) fakeResult {
		return fakeResult{stdout: "line1\nline2\nline3"}
	}}
	pipeTool := newTestTool(f, nil, nil)

	sr, err := pipeTool.InvokeAsStream(authorizedCtx(), &Params{
		Confirmed: true,
		Steps:     []Step{{Shell: &ShellStep{Command: []string{"echo"}}}},
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
	require.Len(t, names, 1)
	assert.Equal(t, "pipe_exec", names[0])
}
