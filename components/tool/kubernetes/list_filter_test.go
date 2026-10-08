package kubernetes

import (
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/filter"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func kafkaTopicList() *unstructured.UnstructuredList {
	return &unstructured.UnstructuredList{
		Items: []unstructured.Unstructured{
			*strimziUnstructured("KafkaTopic", "topic-1", "kafka", map[string]any{
				"spec": map[string]any{"topicName": "topic-1"},
				"status": map[string]any{
					"conditions": []any{
						map[string]any{"type": "Ready", "status": "False", "reason": "NotSupported"},
					},
				},
			}),
			*strimziUnstructured("KafkaTopic", "topic-2", "kafka", map[string]any{
				"spec": map[string]any{"topicName": "topic-2"},
				"status": map[string]any{
					"conditions": []any{
						map[string]any{"type": "Ready", "status": "True"},
					},
				},
			}),
		},
	}
}

func TestCollectMatchesKafkaTopic(t *testing.T) {
	tool := &ListTool{}

	tests := []struct {
		name    string
		filter  string
		fields  []string
		wantLen int
	}{
		{
			name:    "selector matches nested reason",
			filter:  `{"status.conditions[].reason":"NotSupported"}`,
			wantLen: 1,
		},
		{
			name:    "regex matches raw json",
			filter:  `"reason":"NotSupported"`,
			wantLen: 1,
		},
		{
			name:    "selector with fields projection still matches",
			filter:  `{"status.conditions[].reason":"NotSupported"}`,
			fields:  []string{"metadata.name"},
			wantLen: 1,
		},
		{
			name:    "non-matching selector",
			filter:  `{"status.conditions[].reason":"ConfigError"}`,
			wantLen: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := filter.CompileMatcher(tt.filter)
			require.NoError(t, err)

			out := tool.collectMatches(kafkaTopicList(), m, tt.fields)
			assert.Len(t, out, tt.wantLen)
		})
	}
}

func TestCollectMatchesConfigMapRawData(t *testing.T) {
	list := &unstructured.UnstructuredList{
		Items: []unstructured.Unstructured{
			{Object: map[string]any{
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"metadata":   map[string]any{"name": "cm-1", "namespace": "default"},
				"data":       map[string]any{"key1": "value1"},
			}},
		},
	}
	tool := &ListTool{}

	// The curated ConfigMap formatter omits data, so a match proves filtering
	// ran against the raw object.
	m, err := filter.CompileMatcher(`{"data.key1":"value1"}`)
	require.NoError(t, err)
	assert.Len(t, tool.collectMatches(list, m, nil), 1)

	m, err = filter.CompileMatcher(`value1`)
	require.NoError(t, err)
	assert.Len(t, tool.collectMatches(list, m, nil), 1)
}

func TestCollectMatchesRemovesManagedFields(t *testing.T) {
	list := &unstructured.UnstructuredList{
		Items: []unstructured.Unstructured{
			{Object: map[string]any{
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"metadata": map[string]any{
					"name":          "cm-1",
					"namespace":     "default",
					"managedFields": []any{map[string]any{"manager": "kubectl"}},
				},
			}},
		},
	}
	tool := &ListTool{}

	m, err := filter.CompileMatcher("")
	require.NoError(t, err)
	assert.Len(t, tool.collectMatches(list, m, nil), 1)

	_, found, err := unstructured.NestedFieldNoCopy(list.Items[0].Object, "metadata", "managedFields")
	require.NoError(t, err)
	assert.False(t, found)
}

func TestOffsetTokenRoundTrip(t *testing.T) {
	tok := encodeOffsetToken(42)
	got, err := decodeOffsetToken(tok)
	require.NoError(t, err)
	assert.Equal(t, 42, got)

	_, err = decodeOffsetToken("not-base64!!")
	assert.Error(t, err)

	_, err = decodeOffsetToken(base64.StdEncoding.EncodeToString([]byte("{}")))
	assert.Error(t, err)

	_, err = decodeOffsetToken(base64.StdEncoding.EncodeToString([]byte(`{"v":1,"offset":-1}`)))
	assert.Error(t, err)
}
