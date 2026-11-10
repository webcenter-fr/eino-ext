package secretref

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "secret")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func TestResolve(t *testing.T) {
	file := writeFile(t, "from-file\r\n")
	t.Setenv("SECRETREF_TEST", "from-env")

	tests := []struct {
		name    string
		file    string
		env     string
		literal string
		want    string
		wantErr string
	}{
		{name: "file wins", file: file, env: "SECRETREF_TEST", literal: "from-literal", want: "from-file"},
		{name: "env wins over literal", env: "SECRETREF_TEST", literal: "from-literal", want: "from-env"},
		{name: "literal fallback", literal: "from-literal", want: "from-literal"},
		{name: "unset env falls back to literal", env: "SECRETREF_UNSET", literal: "from-literal", want: "from-literal"},
		{name: "missing file", file: filepath.Join(t.TempDir(), "missing"), wantErr: "failed to read"},
		{name: "empty file", file: writeFile(t, "\n"), wantErr: "is empty"},
		{name: "oversize file", file: writeFile(t, strings.Repeat("x", MaxBytes+1)), wantErr: "exceeds"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Resolve(tt.file, tt.env, tt.literal, "test secret")
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestResolveEmptyEnv(t *testing.T) {
	t.Setenv("SECRETREF_EMPTY", "")
	_, err := Resolve("", "SECRETREF_EMPTY", "literal", "test secret")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is empty")
}
