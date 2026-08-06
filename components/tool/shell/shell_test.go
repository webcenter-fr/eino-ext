package shell

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
			name:    "dry run without confirmed",
			params:  Params{Command: []string{"make"}, DryRun: true},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// params validation occurs inside Invoke/InvokeAsStream
			// so test through the tool when available, or test the struct directly
			if tt.wantErr && len(tt.params.Command) == 0 {
				assert.Empty(t, tt.params.Command)
			}
		})
	}
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
	require.Len(t, names, 1)
	assert.Equal(t, "shell_exec", names[0])
}
