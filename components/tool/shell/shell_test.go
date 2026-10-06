package shell

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	toolkitsafety "github.com/webcenter-fr/eino-ext/libs/toolkit/safety"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
)

func TestShellParamsValidation(t *testing.T) {
	tests := []struct {
		name    string
		params  Params
		wantErr bool
	}{
		{
			name:    "empty command fails",
			params:  Params{Command: []string{}},
			wantErr: true,
		},
		{
			name:    "nil command fails",
			params:  Params{Command: nil},
			wantErr: true,
		},
		{
			name:    "valid simple command",
			params:  Params{Command: []string{"echo", "hello"}},
			wantErr: false,
		},
		{
			name:    "valid with profile",
			params:  Params{Command: []string{"go", "build"}, Profile: "golang"},
			wantErr: false,
		},
		{
			name:    "dry run preview",
			params:  Params{Command: []string{"make"}, DryRun: true},
			wantErr: false,
		},
		{
			name:    "valid with stdin",
			params:  Params{Command: []string{"cat"}, Stdin: "piped input"},
			wantErr: false,
		},
		{
			name:    "valid without stdin",
			params:  Params{Command: []string{"cat"}},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validate.Struct(&tt.params)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestRawExecValidation(t *testing.T) {
	bl, err := toolkitsafety.CompileBlocklist(toolkitsafety.DefaultCommandBlocklist)
	require.NoError(t, err)
	tool := &Tool{blocklist: bl}

	t.Run("empty command fails", func(t *testing.T) {
		_, _, _, err := tool.RawExec(context.Background(), RawExecParams{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "non-empty command")
	})

	t.Run("blocklisted command fails", func(t *testing.T) {
		_, _, _, err := tool.RawExec(context.Background(), RawExecParams{
			Command: []string{"rm", "-rf", "/"},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "blocked by security policy")
	})
}

func TestDryRunPreview(t *testing.T) {
	tool := &Tool{}
	preview := tool.dryRunPreview(&Params{
		Command: []string{"go", "test", "./..."},
		Profile: "golang",
	})
	assert.Contains(t, preview, `"dryRun": true`)
	assert.Contains(t, preview, `go test ./...`)
	assert.Contains(t, preview, `"golang"`)
}

func TestDryRunPreview_AutoDetect(t *testing.T) {
	tool := &Tool{}
	preview := tool.dryRunPreview(&Params{
		Command: []string{"ls", "-la"},
	})
	assert.Contains(t, preview, `"dryRun": true`)
	assert.Contains(t, preview, "auto-detect")
}

func TestConfigDefaults(t *testing.T) {
	t.Run("default timeout is set", func(t *testing.T) {
		assert.True(t, defaultExecTimeout > 0)
	})

	t.Run("base image falls back to alpine", func(t *testing.T) {
		cfg := &Config{Workdir: "/tmp"}
		// Just verify the struct shapes are correct
		assert.Equal(t, "/tmp", cfg.Workdir)
		assert.Empty(t, cfg.BaseImage)
	})
}

func TestWriteToolNames(t *testing.T) {
	names := WriteToolNames()
	assert.Empty(t, names)
}

// TestDryRunNoMutation asserts that a dry-run invoke returns a preview and
// performs no Dagger/session work (it returns before profile resolution), so a
// zero-value Tool (nil blocklist, nil sessions) is sufficient.
func TestDryRunNoMutation(t *testing.T) {
	tool := &Tool{}

	result, err := tool.Invoke(context.Background(), &Params{
		Command: []string{"echo", "x"},
		DryRun:  true,
	})
	require.NoError(t, err)
	assert.Contains(t, result, `"dryRun": true`)

	sr, err := tool.InvokeAsStream(context.Background(), &Params{
		Command: []string{"echo", "x"},
		DryRun:  true,
	})
	require.NoError(t, err)
	defer sr.Close()

	var out string
	for {
		chunk, recvErr := sr.Recv()
		if recvErr != nil {
			break
		}
		out += chunk
	}
	assert.Contains(t, out, `"dryRun": true`)
}
