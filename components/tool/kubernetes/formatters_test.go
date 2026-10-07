package kubernetes

import (
	"testing"

	strimzi "github.com/RedHatInsights/strimzi-client-go/apis/kafka.strimzi.io/v1beta2"
	"github.com/goccy/go-json"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// strimziUnstructured builds an unstructured Strimzi object with the given kind
// and metadata, merging extra top-level fields (spec, status, ...).
func strimziUnstructured(kind, name, namespace string, fields map[string]any) *unstructured.Unstructured {
	obj := map[string]any{
		"apiVersion": "kafka.strimzi.io/v1beta2",
		"kind":       kind,
		"metadata":   map[string]any{"name": name, "namespace": namespace},
	}
	for k, v := range fields {
		obj[k] = v
	}
	return &unstructured.Unstructured{Object: obj}
}

func TestFormatKafkaTopicList(t *testing.T) {
	tests := []struct {
		name          string
		fields        map[string]any
		wantTopicName string
		wantStatus    string
	}{
		{
			name:          "no status",
			fields:        map[string]any{"spec": map[string]any{"topicName": "my-topic"}},
			wantTopicName: "my-topic",
		},
		{
			name:   "no spec",
			fields: map[string]any{},
		},
		{
			name: "spec null",
			fields: map[string]any{
				"spec": nil,
				"status": map[string]any{
					"conditions": []any{map[string]any{"type": "Ready", "status": "True"}},
				},
			},
			wantStatus: "Ready",
		},
		{
			name: "status null",
			fields: map[string]any{
				"spec":   map[string]any{"topicName": "my-topic"},
				"status": nil,
			},
			wantTopicName: "my-topic",
		},
		{
			name: "conditions empty",
			fields: map[string]any{
				"spec":   map[string]any{"topicName": "my-topic"},
				"status": map[string]any{"conditions": []any{}},
			},
			wantTopicName: "my-topic",
		},
		{
			name: "conditions missing",
			fields: map[string]any{
				"spec":   map[string]any{"topicName": "my-topic"},
				"status": map[string]any{},
			},
			wantTopicName: "my-topic",
		},
		{
			name: "Ready false",
			fields: map[string]any{
				"spec": map[string]any{"topicName": "my-topic"},
				"status": map[string]any{
					"conditions": []any{map[string]any{
						"type":    "Ready",
						"status":  "False",
						"reason":  "NotSupported",
						"message": "Decreasing partitions not supported",
					}},
				},
			},
			wantTopicName: "my-topic",
			wantStatus:    "Not Ready",
		},
		{
			name: "Ready true",
			fields: map[string]any{
				"spec": map[string]any{"topicName": "my-topic"},
				"status": map[string]any{
					"conditions": []any{map[string]any{"type": "Ready", "status": "True"}},
				},
			},
			wantTopicName: "my-topic",
			wantStatus:    "Ready",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := strimziUnstructured("KafkaTopic", "my-topic", "kafka", tt.fields)
			out := mustUnmarshal(t, formatListItem(u))
			assert.Equal(t, "my-topic", out["name"])
			assert.Equal(t, "kafka", out["namespace"])
			assert.Equal(t, tt.wantTopicName, out["topicName"])
			assert.Equal(t, tt.wantStatus, out["status"])
		})
	}
}

func TestFormatStrimziList_NoStatus(t *testing.T) {
	tests := []struct {
		name     string
		kind     string
		fields   map[string]any
		assertFn func(t *testing.T, out map[string]any)
	}{
		{
			name:   "Kafka no status",
			kind:   "Kafka",
			fields: map[string]any{},
			assertFn: func(t *testing.T, out map[string]any) {
				assert.Equal(t, "", out["version"])
				assert.Equal(t, "", out["status"])
			},
		},
		{
			name:   "KafkaNodePool no status",
			kind:   "KafkaNodePool",
			fields: map[string]any{},
			assertFn: func(t *testing.T, out map[string]any) {
				assert.Equal(t, float64(0), out["replicas"])
				assert.Equal(t, []any{}, out["roles"])
				assert.Equal(t, "", out["status"])
			},
		},
		{
			name: "KafkaNodePool no spec",
			kind: "KafkaNodePool",
			fields: map[string]any{
				"status": map[string]any{"replicas": float64(3)},
			},
			assertFn: func(t *testing.T, out map[string]any) {
				assert.Equal(t, float64(3), out["replicas"])
				assert.Equal(t, []any{}, out["roles"])
			},
		},
		{
			name:   "KafkaUser no status",
			kind:   "KafkaUser",
			fields: map[string]any{},
			assertFn: func(t *testing.T, out map[string]any) {
				assert.Equal(t, "", out["username"])
				assert.Equal(t, "", out["status"])
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := strimziUnstructured(tt.kind, "res", "kafka", tt.fields)
			out := mustUnmarshal(t, formatListItem(u))
			assert.Equal(t, "res", out["name"])
			assert.Equal(t, "kafka", out["namespace"])
			tt.assertFn(t, out)
		})
	}
}

func TestStrimziReadyStatus(t *testing.T) {
	sp := func(s string) *string { return &s }

	tests := []struct {
		name       string
		conditions any
		want       string
	}{
		{"untyped nil", nil, ""},
		{"empty slice", []strimzi.KafkaTopicStatusConditionsElem{}, ""},
		{"typed nil slice", ([]strimzi.KafkaTopicStatusConditionsElem)(nil), ""},
		{"non-slice string", "foo", ""},
		{"non-slice int", 123, ""},
		{
			"ready true",
			[]strimzi.KafkaTopicStatusConditionsElem{{Type: sp("Ready"), Status: sp("True")}},
			"Ready",
		},
		{
			"ready false",
			[]strimzi.KafkaTopicStatusConditionsElem{{Type: sp("Ready"), Status: sp("False")}},
			"Not Ready",
		},
		{
			"pointer element ready",
			[]*strimzi.KafkaTopicStatusConditionsElem{{Type: sp("Ready"), Status: sp("True")}},
			"Ready",
		},
		{
			"nil pointer element then ready",
			[]*strimzi.KafkaTopicStatusConditionsElem{nil, {Type: sp("Ready"), Status: sp("True")}},
			"Ready",
		},
		{"nil type and status", []strimzi.KafkaTopicStatusConditionsElem{{}}, ""},
		{
			"non-ready condition",
			[]strimzi.KafkaTopicStatusConditionsElem{{Type: sp("Reconciling"), Status: sp("True")}},
			"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, strimziReadyStatus(tt.conditions))
		})
	}
}

func TestFormatListItem_RecoversFromPanickingFormatter(t *testing.T) {
	t.Run("unstructured path", func(t *testing.T) {
		gvk := schema.GroupVersionKind{Group: "test.example.com", Version: "v1", Kind: "Panic"}
		formatterRegistry[gvk] = formatterEntry{
			newObj: nil,
			format: func(o runtime.Object) json.RawMessage { panic("boom") },
		}
		defer delete(formatterRegistry, gvk)

		u := &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "test.example.com/v1",
			"kind":       "Panic",
			"metadata":   map[string]any{"name": "p", "namespace": "default"},
		}}
		out := mustUnmarshal(t, formatListItem(u))
		assert.Equal(t, "p", out["name"])
		assert.Equal(t, "default", out["namespace"])
	})

	t.Run("typed path", func(t *testing.T) {
		gvk := schema.GroupVersionKind{Group: "test.example.com", Version: "v1", Kind: "PanicTyped"}
		formatterRegistry[gvk] = formatterEntry{
			newObj: func() runtime.Object { return &corev1.Pod{} },
			format: func(o runtime.Object) json.RawMessage { panic("boom") },
		}
		defer delete(formatterRegistry, gvk)

		u := &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "test.example.com/v1",
			"kind":       "PanicTyped",
			"metadata":   map[string]any{"name": "pt", "namespace": "default"},
		}}
		out := mustUnmarshal(t, formatListItem(u))
		assert.Equal(t, "pt", out["name"])
		assert.Equal(t, "default", out["namespace"])
	})
}

func TestFormatListItem_MixedReconciledAndUnreconciled(t *testing.T) {
	items := []*unstructured.Unstructured{
		strimziUnstructured("KafkaTopic", "reconciled", "kafka", map[string]any{
			"spec": map[string]any{"topicName": "reconciled"},
			"status": map[string]any{
				"conditions": []any{map[string]any{"type": "Ready", "status": "True"}},
			},
		}),
		strimziUnstructured("KafkaTopic", "unreconciled", "kafka", map[string]any{
			"spec": map[string]any{"topicName": "unreconciled"},
		}),
		strimziUnstructured("KafkaTopic", "nil-spec", "kafka", map[string]any{}),
	}

	for _, u := range items {
		var out map[string]any
		assert.NotPanics(t, func() { out = mustUnmarshal(t, formatListItem(u)) })
		assert.Equal(t, u.GetName(), out["name"])
	}
}
