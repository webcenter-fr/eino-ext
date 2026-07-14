package safety

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompileBlocklist(t *testing.T) {
	tests := []struct {
		name     string
		patterns []string
		wantErr  bool
	}{
		{
			name:     "valid patterns",
			patterns: []string{`\brm\b`, `\bkill\b`},
			wantErr:  false,
		},
		{
			name:     "invalid regex",
			patterns: []string{`[unclosed`},
			wantErr:  true,
		},
		{
			name:     "empty list",
			patterns: []string{},
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			compiled, err := CompileBlocklist(tt.patterns)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Len(t, compiled, len(tt.patterns))
		})
	}
}

func TestCheckBlocklist(t *testing.T) {
	compiled, err := CompileBlocklist(DefaultCommandBlocklist)
	require.NoError(t, err)

	tests := []struct {
		name    string
		command []string
		wantErr bool
	}{
		{
			name:    "safe ls command",
			command: []string{"ls", "-la"},
			wantErr: false,
		},
		{
			name:    "safe echo command",
			command: []string{"echo", "hello"},
			wantErr: false,
		},
		{
			name:    "safe go test command",
			command: []string{"go", "test", "./..."},
			wantErr: false,
		},
		{
			name:    "blocked rm command",
			command: []string{"rm", "-rf", "/"},
			wantErr: true,
		},
		{
			name:    "blocked absolute path rm",
			command: []string{"/bin/rm", "-rf", "/"},
			wantErr: true,
		},
		{
			name:    "blocked relative path rm",
			command: []string{"./rm", "file"},
			wantErr: true,
		},
		{
			name:    "blocked kill command",
			command: []string{"kill", "1234"},
			wantErr: true,
		},
		{
			name:    "blocked shutdown",
			command: []string{"shutdown", "-h", "now"},
			wantErr: true,
		},
		{
			name:    "blocked eval",
			command: []string{"eval", "$(something)"},
			wantErr: true,
		},
		{
			name:    "blocked shell -c",
			command: []string{"bash", "-c", "rm -rf /"},
			wantErr: true,
		},
		{
			name:    "blocked python -c",
			command: []string{"python", "-c", "import os; os.system('rm -rf /')"},
			wantErr: true,
		},
		{
			name:    "blocked perl -e",
			command: []string{"perl", "-e", "system('rm -rf /')"},
			wantErr: true,
		},
		{
			name:    "blocked busybox",
			command: []string{"busybox", "rm", "-rf", "/"},
			wantErr: true,
		},
		{
			name:    "blocked mount",
			command: []string{"mount", "/dev/sda1", "/mnt"},
			wantErr: true,
		},
		{
			name:    "blocked chmod recursive on root path",
			command: []string{"chmod", "-R", "/etc"},
			wantErr: true,
		},
		{
			name:    "blocked openssl enc",
			command: []string{"openssl", "enc", "-aes-256-cbc"},
			wantErr: true,
		},
		{
			name:    "blocked apt-get install",
			command: []string{"apt-get", "install", "-y", "jq"},
			wantErr: true,
		},
		{
			name:    "blocked standalone install",
			command: []string{"install", "file"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckBlocklist(compiled, tt.command)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestDefaultCommandBlocklist_BypassVectors(t *testing.T) {
	compiled, err := CompileBlocklist(DefaultCommandBlocklist)
	require.NoError(t, err)

	bypassTests := []struct {
		name    string
		command []string
	}{
		{name: "/bin/rm", command: []string{"/bin/rm", "-rf", "/tmp/foo"}},
		{name: "./rm", command: []string{"./rm", "file"}},
		{name: "bash -c rm", command: []string{"bash", "-c", "rm -rf /"}},
		{name: "sh -c rm", command: []string{"sh", "-c", "rm -rf /"}},
		{name: "dash -c rm", command: []string{"dash", "-c", "rm -rf /"}},
		{name: "zsh -c kill", command: []string{"zsh", "-c", "kill 1"}},
		{name: "python -c", command: []string{"python", "-c", "__import__('os').system('rm -rf /')"}},
		{name: "python3 -c", command: []string{"python3", "-c", "import os"}},
		{name: "perl -e", command: []string{"perl", "-e", "unlink('/etc/passwd')"}},
		{name: "node -e", command: []string{"node", "-e", "require('child_process').exec('rm -rf /')"}},
		{name: "php -r", command: []string{"php", "-r", "exec('rm -rf /')"}},
		{name: "env command", command: []string{"env", "PATH=/tmp", "bash"}},
		{name: "eval", command: []string{"eval", "something"}},
		{name: "source script", command: []string{"source", "evil.sh"}},
		{name: "dot space", command: []string{".", "evil.sh"}},
		{name: "awk exec", command: []string{"awk", "{print}"}},
		{name: "gawk exec", command: []string{"gawk", "{system(\"rm\")}"}},
		{name: "nawk exec", command: []string{"nawk", "{system(\"rm\")}"}},
		{name: "busybox rm", command: []string{"busybox", "rm", "-rf", "/"}},
		{name: "toybox rm", command: []string{"toybox", "rm", "-rf", "/"}},
		{name: "openssl enc", command: []string{"openssl", "enc", "-aes-256-cbc", "-k", "key"}},
	}

	for _, tt := range bypassTests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckBlocklist(compiled, tt.command)
			assert.Error(t, err, "expected blocklist to catch %q", tt.name)
		})
	}
}
