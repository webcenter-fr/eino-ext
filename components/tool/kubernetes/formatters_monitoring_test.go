package kubernetes

import (
	"fmt"
	"math"
	"testing"

	"github.com/goccy/go-json"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func mustUnmarshal(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var out map[string]any
	assert.NoError(t, json.Unmarshal(raw, &out))
	return out
}

func TestFormatAlertmanagerList(t *testing.T) {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "monitoring.coreos.com/v1",
		"kind":       "Alertmanager",
		"metadata":   map[string]any{"name": "main", "namespace": "monitoring"},
		"spec": map[string]any{
			"replicas": float64(3),
			"version":  "v0.27.0",
			"paused":   false,
		},
		"status": map[string]any{
			"availableReplicas": float64(3),
		},
	}}

	out := mustUnmarshal(t, formatListItem(u))
	assert.Equal(t, "main", out["name"])
	assert.Equal(t, "monitoring", out["namespace"])
	assert.Equal(t, float64(3), out["replicas"])
	assert.Equal(t, "v0.27.0", out["version"])
	assert.Equal(t, false, out["paused"])
	assert.Equal(t, float64(3), out["availableReplicas"])
	assert.Equal(t, "Available", out["status"])
}

func TestFormatAlertmanagerList_Paused(t *testing.T) {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "monitoring.coreos.com/v1",
		"kind":       "Alertmanager",
		"metadata":   map[string]any{"name": "main", "namespace": "monitoring"},
		"spec": map[string]any{
			"replicas": float64(3),
			"paused":   true,
		},
		"status": map[string]any{
			"availableReplicas": float64(3),
		},
	}}

	out := mustUnmarshal(t, formatListItem(u))
	assert.Equal(t, true, out["paused"])
	assert.Equal(t, "Paused", out["status"])
}

func TestFormatAlertmanagerList_NilReplicas(t *testing.T) {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "monitoring.coreos.com/v1",
		"kind":       "Alertmanager",
		"metadata":   map[string]any{"name": "main", "namespace": "monitoring"},
		"spec":       map[string]any{},
		"status":     map[string]any{},
	}}

	out := mustUnmarshal(t, formatListItem(u))
	assert.Equal(t, float64(0), out["replicas"])
	assert.Empty(t, out["status"])
}

func TestFormatAlertmanagerList_Degraded(t *testing.T) {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "monitoring.coreos.com/v1",
		"kind":       "Alertmanager",
		"metadata":   map[string]any{"name": "main", "namespace": "monitoring"},
		"spec": map[string]any{
			"replicas": float64(3),
		},
		"status": map[string]any{
			"availableReplicas":   float64(2),
			"unavailableReplicas": float64(1),
		},
	}}

	out := mustUnmarshal(t, formatListItem(u))
	assert.Equal(t, "Degraded", out["status"])
}

func TestFormatAlertmanagerConfigList(t *testing.T) {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "monitoring.coreos.com/v1alpha1",
		"kind":       "AlertmanagerConfig",
		"metadata":   map[string]any{"name": "amc", "namespace": "monitoring"},
		"spec": map[string]any{
			"route": map[string]any{
				"receiver": "slack",
				"routes":   []any{map[string]any{"receiver": "email"}, map[string]any{"receiver": "pager"}},
			},
			"receivers": []any{
				map[string]any{"name": "slack"},
				map[string]any{"name": "email"},
			},
		},
	}}

	out := mustUnmarshal(t, formatListItem(u))
	assert.Equal(t, "amc", out["name"])
	assert.Equal(t, "monitoring", out["namespace"])
	assert.Equal(t, "slack", out["receiver"])
	assert.Equal(t, []any{"slack", "email"}, out["receivers"])
	assert.Equal(t, float64(2), out["routes"])
}

func TestFormatPrometheusRuleList(t *testing.T) {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "monitoring.coreos.com/v1",
		"kind":       "PrometheusRule",
		"metadata":   map[string]any{"name": "rules", "namespace": "monitoring"},
		"spec": map[string]any{
			"groups": []any{
				map[string]any{
					"name": "group1",
					"rules": []any{
						map[string]any{
							"alert":  "HighErrorRate",
							"expr":   "sum(rate(errors[5m])) > 0",
							"labels": map[string]any{"severity": "critical"},
						},
						map[string]any{
							"alert":  "LowDiskSpace",
							"expr":   "disk_free < 10",
							"labels": map[string]any{"severity": "warning"},
						},
					},
				},
				map[string]any{
					"name": "group2",
					"rules": []any{
						map[string]any{
							"alert":  "HighErrorRate",
							"expr":   "sum(rate(errors[5m])) > 10",
							"labels": map[string]any{"severity": "critical"},
						},
					},
				},
			},
		},
	}}

	out := mustUnmarshal(t, formatListItem(u))
	assert.Equal(t, "rules", out["name"])
	assert.Equal(t, "monitoring", out["namespace"])
	assert.Equal(t, float64(2), out["groups"])
	assert.Equal(t, float64(3), out["rules"])
	assert.Equal(t, []any{"HighErrorRate", "LowDiskSpace", "HighErrorRate"}, out["alerts"])
	assert.Equal(t, []any{"critical", "warning"}, out["severities"])
}

func TestFormatPrometheusRuleList_RecordingRules(t *testing.T) {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "monitoring.coreos.com/v1",
		"kind":       "PrometheusRule",
		"metadata":   map[string]any{"name": "rules", "namespace": "monitoring"},
		"spec": map[string]any{
			"groups": []any{
				map[string]any{
					"name": "recording",
					"rules": []any{
						map[string]any{
							"record": "job:http_requests:rate5m",
							"expr":   "sum(rate(http_requests[5m]))",
						},
						map[string]any{
							"alert":  "HighErrorRate",
							"expr":   "sum(rate(errors[5m])) > 0",
							"labels": map[string]any{"severity": "critical"},
						},
					},
				},
			},
		},
	}}

	out := mustUnmarshal(t, formatListItem(u))
	assert.Equal(t, float64(1), out["groups"])
	assert.Equal(t, float64(2), out["rules"])
	assert.Equal(t, []any{"HighErrorRate"}, out["alerts"])
}

func TestFormatPrometheusRuleList_AlertsCapped(t *testing.T) {
	// A maliciously large PrometheusRule must not blow up list output: the
	// surfaced alert names are capped (CWE-400) while the total rule count
	// is still reported.
	const totalRules = maxListAlerts + 50
	rules := make([]any, 0, totalRules)
	for i := 0; i < totalRules; i++ {
		rules = append(rules, map[string]any{
			"alert": fmt.Sprintf("Alert%d", i),
			"expr":  "vector(1)",
		})
	}
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "monitoring.coreos.com/v1",
		"kind":       "PrometheusRule",
		"metadata":   map[string]any{"name": "rules", "namespace": "monitoring"},
		"spec": map[string]any{
			"groups": []any{
				map[string]any{"name": "g", "rules": rules},
			},
		},
	}}

	out := mustUnmarshal(t, formatListItem(u))
	assert.Equal(t, float64(totalRules), out["rules"])
	alerts := out["alerts"].([]any)
	assert.Len(t, alerts, maxListAlerts)
	assert.Equal(t, true, out["alertsMore"])
}

func TestFormatSilenceList(t *testing.T) {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "monitoring.coreos.com/v1alpha1",
		"kind":       "Silence",
		"metadata":   map[string]any{"name": "silence-1", "namespace": "monitoring"},
		"spec": map[string]any{
			"matchers": []any{
				map[string]any{"name": "alertname", "value": "HighErrorRate", "isRegex": false},
				map[string]any{"name": "cluster", "value": "prod-.*", "isRegex": true},
			},
			"startsAt":  "2024-01-01T00:00:00Z",
			"endsAt":    "2024-01-02T00:00:00Z",
			"createdBy": "admin",
		},
		"status": map[string]any{"state": "active"},
	}}

	out := mustUnmarshal(t, formatListItem(u))
	assert.Equal(t, "silence-1", out["name"])
	assert.Equal(t, "monitoring", out["namespace"])
	assert.Equal(t, "active", out["state"])
	assert.Equal(t, "2024-01-01T00:00:00Z", out["startsAt"])
	assert.Equal(t, "2024-01-02T00:00:00Z", out["endsAt"])
	assert.Equal(t, "admin", out["createdBy"])

	matchers := out["matchers"].([]any)
	assert.Len(t, matchers, 2)
	assert.Equal(t, "alertname", matchers[0].(map[string]any)["name"])
	assert.Equal(t, "HighErrorRate", matchers[0].(map[string]any)["value"])
	assert.Equal(t, false, matchers[0].(map[string]any)["isRegex"])
	assert.Equal(t, true, matchers[1].(map[string]any)["isRegex"])
}

func TestFormatSilenceList_NoStatus(t *testing.T) {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "monitoring.coreos.com/v1alpha1",
		"kind":       "Silence",
		"metadata":   map[string]any{"name": "silence-1", "namespace": "monitoring"},
		"spec": map[string]any{
			"matchers": []any{map[string]any{"name": "alertname", "value": "X", "isRegex": false}},
		},
	}}

	out := mustUnmarshal(t, formatListItem(u))
	assert.Empty(t, out["state"])
}

func TestRegistryMonitoringEntries(t *testing.T) {
	gvks := []schema.GroupVersionKind{
		{Group: "monitoring.coreos.com", Version: "v1", Kind: "Alertmanager"},
		{Group: "monitoring.coreos.com", Version: "v1alpha1", Kind: "AlertmanagerConfig"},
		{Group: "monitoring.coreos.com", Version: "v1", Kind: "PrometheusRule"},
		{Group: "monitoring.coreos.com", Version: "v1alpha1", Kind: "Silence"},
	}
	for _, gvk := range gvks {
		entry, ok := formatterRegistry[gvk]
		assert.True(t, ok, "%s should be registered", gvk.Kind)
		assert.Nil(t, entry.newObj, "%s should be unstructured-only", gvk.Kind)
		assert.NotNil(t, entry.format, "%s should have a list formatter", gvk.Kind)
	}
}

func TestFormatListItem_FallbackUnknownGVK(t *testing.T) {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "example.com/v1",
		"kind":       "Widget",
		"metadata":   map[string]any{"name": "w1", "namespace": "default"},
		"status": map[string]any{
			"conditions": []any{
				map[string]any{"type": "Ready", "status": "True"},
			},
		},
	}}

	out := mustUnmarshal(t, formatListItem(u))
	assert.Equal(t, "w1", out["name"])
	assert.Equal(t, "default", out["namespace"])
	assert.Equal(t, "Ready", out["status"])
}

func TestFormatListItem_FallbackMissingAPIVersion(t *testing.T) {
	u := &unstructured.Unstructured{Object: map[string]any{
		"kind":     "ConfigMap",
		"metadata": map[string]any{"name": "cm1", "namespace": "default"},
	}}

	out := mustUnmarshal(t, formatListItem(u))
	assert.Equal(t, "cm1", out["name"])
	assert.Equal(t, "default", out["namespace"])
	_, ok := out["status"]
	assert.False(t, ok)
}

func TestUnstructuredHelpers(t *testing.T) {
	tests := []struct {
		name string
		got  func() any
		want any
	}{
		{"uInt32 float64", func() any { return uInt32(map[string]any{"r": float64(3)}, "r") }, int32(3)},
		{"uInt32 int64", func() any { return uInt32(map[string]any{"r": int64(7)}, "r") }, int32(7)},
		{"uInt32 int", func() any { return uInt32(map[string]any{"r": 9}, "r") }, int32(9)},
		{"uInt32 nil map", func() any { return uInt32(nil, "r") }, int32(0)},
		{"uInt32 absent key", func() any { return uInt32(map[string]any{}, "r") }, int32(0)},
		{"uInt32 overflow clamped", func() any { return uInt32(map[string]any{"r": float64(1e20)}, "r") }, int32(math.MaxInt32)},
		{"uInt32 underflow clamped", func() any { return uInt32(map[string]any{"r": float64(-1e20)}, "r") }, int32(math.MinInt32)},
		{"uInt32 non-number", func() any { return uInt32(map[string]any{"r": "3"}, "r") }, int32(0)},
		{"uStringSlice mixed skips non-strings", func() any { return uStringSlice(map[string]any{"x": []any{"a", 1, "b", true}}, "x") }, []string{"a", "b"}},
		{"uString", func() any { return uString(map[string]any{"x": "s"}, "x") }, "s"},
		{"uBool", func() any { return uBool(map[string]any{"x": true}, "x") }, true},
		{"uMap present", func() any { return uMap(map[string]any{"x": map[string]any{"a": "b"}}, "x") }, map[string]any{"a": "b"}},
		{"uMapSlice filters non-maps", func() any { return uMapSlice(map[string]any{"x": []any{map[string]any{"a": "b"}, "not-a-map"}}, "x") }, []map[string]any{{"a": "b"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.got())
		})
	}

	t.Run("uStringSlice absent key", func(t *testing.T) {
		assert.Nil(t, uStringSlice(map[string]any{}, "x"))
	})
	t.Run("uMap absent key", func(t *testing.T) {
		assert.Nil(t, uMap(map[string]any{}, "x"))
	})
	t.Run("uMapSlice absent key", func(t *testing.T) {
		assert.Nil(t, uMapSlice(map[string]any{}, "x"))
	})
}
