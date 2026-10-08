package filter

import (
	"testing"

	"github.com/goccy/go-json"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustSelector(t *testing.T, pattern string) *Selector {
	t.Helper()
	m, err := CompileMatcher(pattern)
	require.NoError(t, err)
	sm, ok := m.(selectorMatcher)
	require.True(t, ok, "expected a selector matcher")
	return sm.sel
}

func TestSelectorMatchObject(t *testing.T) {
	tests := []struct {
		name     string
		selector string
		obj      map[string]any
		want     bool
	}{
		{
			name:     "top-level case-insensitive",
			selector: `{"Namespace":"KAFKA-HPD1"}`,
			obj:      map[string]any{"namespace": "kafka-hpd1"},
			want:     true,
		},
		{
			name:     "nested dot path",
			selector: `{"spec.topicName":"rec1.public.sid.acte"}`,
			obj:      map[string]any{"spec": map[string]any{"topicName": "rec1.public.sid.acte"}},
			want:     true,
		},
		{
			name:     "array wildcard matches case-insensitively",
			selector: `{"status.conditions[].reason":"NotSupported"}`,
			obj: map[string]any{
				"status": map[string]any{
					"conditions": []any{
						map[string]any{"type": "Ready", "reason": "notsupported"},
					},
				},
			},
			want: true,
		},
		{
			name:     "bare array segment",
			selector: `{"conditions.[].reason":"NotSupported"}`,
			obj: map[string]any{
				"conditions": []any{
					map[string]any{"reason": "NotSupported"},
				},
			},
			want: true,
		},
		{
			name:     "bare key recursive",
			selector: `{"reason":"NotSupported"}`,
			obj: map[string]any{
				"status": map[string]any{
					"conditions": []any{
						map[string]any{"reason": "NotSupported"},
					},
				},
			},
			want: true,
		},
		{
			name:     "number coerced to string",
			selector: `{"spec.partitions":"3"}`,
			obj:      map[string]any{"spec": map[string]any{"partitions": float64(3)}},
			want:     true,
		},
		{
			name:     "bool coerced to string",
			selector: `{"spec.ready":"true"}`,
			obj:      map[string]any{"spec": map[string]any{"ready": true}},
			want:     true,
		},
		{
			name:     "explicit null matches",
			selector: `{"spec.x":null}`,
			obj:      map[string]any{"spec": map[string]any{"x": nil}},
			want:     true,
		},
		{
			name:     "IN array value",
			selector: `{"status.conditions[].reason":["NotSupported","ConfigError"]}`,
			obj: map[string]any{
				"status": map[string]any{
					"conditions": []any{
						map[string]any{"reason": "ConfigError"},
					},
				},
			},
			want: true,
		},
		{
			name:     "AND of multiple keys",
			selector: `{"metadata.name":"a","spec.topicName":"t"}`,
			obj: map[string]any{
				"metadata": map[string]any{"name": "a"},
				"spec":     map[string]any{"topicName": "t"},
			},
			want: true,
		},
		{
			name:     "AND fails when one key misses",
			selector: `{"metadata.name":"a","spec.topicName":"t"}`,
			obj: map[string]any{
				"metadata": map[string]any{"name": "a"},
				"spec":     map[string]any{"topicName": "other"},
			},
			want: false,
		},
		{
			name:     "missing path",
			selector: `{"spec.missing":"x"}`,
			obj:      map[string]any{"spec": map[string]any{}},
			want:     false,
		},
		{
			name:     "wrong value",
			selector: `{"spec.topicName":"a"}`,
			obj:      map[string]any{"spec": map[string]any{"topicName": "b"}},
			want:     false,
		},
		{
			name:     "empty array at wildcard",
			selector: `{"status.conditions[].reason":"NotSupported"}`,
			obj:      map[string]any{"status": map[string]any{"conditions": []any{}}},
			want:     false,
		},
		{
			name:     "empty selector matches everything",
			selector: `{}`,
			obj:      map[string]any{"anything": "goes"},
			want:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sel := mustSelector(t, tt.selector)
			assert.Equal(t, tt.want, sel.MatchObject(tt.obj))
		})
	}
}

func TestCompileSelectorErrors(t *testing.T) {
	tests := []struct {
		name     string
		selector string
	}{
		{name: "numeric index", selector: `{"status.conditions[0].reason":"x"}`},
		{name: "empty segment", selector: `{"a..b":"x"}`},
		{name: "trailing dot", selector: `{"a.":"x"}`},
		{name: "empty key", selector: `{"":"x"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := CompileMatcher(tt.selector)
			assert.Error(t, err)
		})
	}
}

func TestSelectorMatchJSON(t *testing.T) {
	m, err := CompileMatcher(`{"data.key1":"value1"}`)
	require.NoError(t, err)

	data, err := json.Marshal(map[string]any{"data": map[string]any{"key1": "value1"}})
	require.NoError(t, err)
	assert.True(t, m.MatchJSON(data))
}
