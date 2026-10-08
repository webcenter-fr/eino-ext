package filter

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompileMatcher(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		wantErr bool
	}{
		{name: "empty matches all", pattern: ""},
		{name: "whitespace matches all", pattern: "   "},
		{name: "valid selector", pattern: `{"a":"b"}`},
		{name: "empty selector", pattern: `{}`},
		{name: "malformed selector", pattern: `{"a":`, wantErr: true},
		{name: "plain regex", pattern: `app-.*`},
		{name: "invalid regex", pattern: `(?=lookahead)`, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := CompileMatcher(tt.pattern)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, m)
				return
			}
			require.NoError(t, err)
			assert.NotNil(t, m)
		})
	}
}

func TestMatchAllMatcher(t *testing.T) {
	m, err := CompileMatcher("")
	require.NoError(t, err)

	assert.True(t, m.MatchJSON([]byte(`{"a":1}`)))
	assert.True(t, m.MatchObject(map[string]any{"a": 1}))
}

func TestRegexMatcher(t *testing.T) {
	m, err := CompileMatcher(`"reason":"NotSupported"`)
	require.NoError(t, err)

	assert.True(t, m.MatchJSON([]byte(`{"status":{"reason":"NotSupported"}}`)))
	assert.True(t, m.MatchObject(map[string]any{"status": map[string]any{"reason": "NotSupported"}}))
	assert.False(t, m.MatchJSON([]byte(`{"status":{"reason":"Ready"}}`)))
	assert.False(t, m.MatchObject(map[string]any{"status": map[string]any{"reason": "Ready"}}))
}

func TestSelectorMatcher(t *testing.T) {
	m, err := CompileMatcher(`{"status.conditions[].reason":"NotSupported"}`)
	require.NoError(t, err)

	obj := map[string]any{
		"status": map[string]any{
			"conditions": []any{
				map[string]any{"reason": "NotSupported"},
			},
		},
	}
	assert.True(t, m.MatchObject(obj))
	assert.True(t, m.MatchJSON([]byte(`{"status":{"conditions":[{"reason":"NotSupported"}]}}`)))
	assert.False(t, m.MatchJSON([]byte(`not json`)))
}
