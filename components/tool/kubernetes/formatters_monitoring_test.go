package kubernetes

import (
	"fmt"
	"math"
	"testing"

	"github.com/goccy/go-json"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

func TestDescribeAlertmanager(t *testing.T) {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "monitoring.coreos.com/v1",
		"kind":       "Alertmanager",
		"metadata":   map[string]any{"name": "main", "namespace": "monitoring"},
		"spec": map[string]any{
			"replicas": float64(3),
			"version":  "v0.27.0",
			"image":    "quay.io/prometheus/alertmanager:v0.27.0",
			"paused":   false,
			"logLevel": "info",
			"resources": map[string]any{
				"requests": map[string]any{"memory": "200Mi"},
			},
			"storage": map[string]any{"volumeClaimTemplate": map[string]any{}},
		},
		"status": map[string]any{
			"paused":              false,
			"replicas":            float64(3),
			"updatedReplicas":     float64(3),
			"availableReplicas":   float64(3),
			"unavailableReplicas": float64(0),
			"conditions": []any{
				map[string]any{
					"type":               "Available",
					"status":             "True",
					"reason":             "AllReplicasReady",
					"message":            "all replicas ready",
					"lastTransitionTime": "2024-01-01T00:00:00Z",
				},
			},
		},
	}}

	out := describeAlertmanager(u)
	data, err := json.Marshal(out)
	assert.NoError(t, err)
	m := mustUnmarshal(t, data)

	assert.Equal(t, "Alertmanager", m["kind"])
	assert.Equal(t, "monitoring.coreos.com/v1", m["apiVersion"])

	meta := m["metadata"].(map[string]any)
	assert.Equal(t, "main", meta["name"])
	assert.Equal(t, "monitoring", meta["namespace"])

	spec := m["spec"].(map[string]any)
	assert.Equal(t, float64(3), spec["replicas"])
	assert.Equal(t, "v0.27.0", spec["version"])
	assert.Equal(t, "quay.io/prometheus/alertmanager:v0.27.0", spec["image"])
	assert.Equal(t, "info", spec["logLevel"])

	status := m["status"].(map[string]any)
	assert.Equal(t, float64(3), status["availableReplicas"])
	conds := status["conditions"].([]any)
	assert.Len(t, conds, 1)
	assert.Equal(t, "Available", conds[0].(map[string]any)["type"])
	assert.Equal(t, "True", conds[0].(map[string]any)["status"])
	assert.Equal(t, "AllReplicasReady", conds[0].(map[string]any)["reason"])
}

func TestDescribeAlertmanager_ExcludeSpec(t *testing.T) {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "monitoring.coreos.com/v1",
		"kind":       "Alertmanager",
		"metadata":   map[string]any{"name": "main", "namespace": "monitoring"},
		"spec":       map[string]any{"replicas": float64(3)},
		"status":     map[string]any{"availableReplicas": float64(3)},
	}}

	out := describeAlertmanager(u)
	assert.NoError(t, out.applyFieldExclusions([]string{"spec"}))
	data, err := json.Marshal(out)
	assert.NoError(t, err)
	m := mustUnmarshal(t, data)
	_, ok := m["spec"]
	assert.False(t, ok)
	assert.NotNil(t, m["status"])
	assert.NotNil(t, m["metadata"])
}

func TestDescribeAlertmanager_ExcludeMetadata(t *testing.T) {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "monitoring.coreos.com/v1",
		"kind":       "Alertmanager",
		"metadata":   map[string]any{"name": "main", "namespace": "monitoring"},
		"spec":       map[string]any{"replicas": float64(3)},
		"status":     map[string]any{"availableReplicas": float64(3)},
	}}

	out := describeAlertmanager(u)
	assert.NoError(t, out.applyFieldExclusions([]string{"metadata"}))
	data, err := json.Marshal(out)
	assert.NoError(t, err)
	m := mustUnmarshal(t, data)
	_, ok := m["metadata"]
	assert.False(t, ok)
	assert.NotNil(t, m["spec"])
	assert.NotNil(t, m["status"])
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

func TestDescribeAlertmanagerConfig(t *testing.T) {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "monitoring.coreos.com/v1alpha1",
		"kind":       "AlertmanagerConfig",
		"metadata":   map[string]any{"name": "amc", "namespace": "monitoring"},
		"spec": map[string]any{
			"route": map[string]any{
				"receiver":       "slack",
				"groupBy":        []any{"alertname", "cluster"},
				"groupWait":      "30s",
				"groupInterval":  "5m",
				"repeatInterval": "4h",
				"routes": []any{
					map[string]any{"receiver": "email"},
				},
			},
			"receivers": []any{
				map[string]any{
					"name":         "slack",
					"slackConfigs": []any{map[string]any{"channel": "#alerts"}},
					"webhookConfigs": []any{
						map[string]any{"url": "https://hooks.example.com"},
					},
				},
				map[string]any{"name": "email"},
			},
			"inhibitRules": []any{
				map[string]any{"sourceMatch": []any{map[string]any{"name": "severity", "value": "critical"}}},
			},
		},
		"status": map[string]any{
			"bindings": []any{
				map[string]any{
					"conditions": []any{
						map[string]any{"type": "Reconciled", "status": "True", "reason": "OK"},
					},
				},
			},
		},
	}}

	out := describeAlertmanagerConfig(u)
	data, err := json.Marshal(out)
	assert.NoError(t, err)
	m := mustUnmarshal(t, data)

	assert.Equal(t, "AlertmanagerConfig", m["kind"])
	assert.Equal(t, "monitoring.coreos.com/v1alpha1", m["apiVersion"])

	spec := m["spec"].(map[string]any)
	route := spec["route"].(map[string]any)
	assert.Equal(t, "slack", route["receiver"])
	assert.Equal(t, []any{"alertname", "cluster"}, route["groupBy"])
	assert.Equal(t, "30s", route["groupWait"])
	assert.Len(t, route["routes"].([]any), 1)

	receivers := spec["receivers"].([]any)
	assert.Len(t, receivers, 2)
	assert.Equal(t, "slack", receivers[0].(map[string]any)["name"])
	assert.Equal(t, []any{"slack", "webhook"}, receivers[0].(map[string]any)["types"])

	inhibitRules := spec["inhibitRules"].([]any)
	assert.Len(t, inhibitRules, 1)

	status := m["status"].(map[string]any)
	conds := status["conditions"].([]any)
	assert.Len(t, conds, 1)
	assert.Equal(t, "Reconciled", conds[0].(map[string]any)["type"])
	assert.Equal(t, "True", conds[0].(map[string]any)["status"])
}

func TestDescribeAlertmanagerConfig_NoRedaction(t *testing.T) {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "monitoring.coreos.com/v1alpha1",
		"kind":       "AlertmanagerConfig",
		"metadata":   map[string]any{"name": "amc", "namespace": "monitoring"},
		"spec": map[string]any{
			"route": map[string]any{"receiver": "slack"},
			"receivers": []any{
				map[string]any{
					"name": "slack",
					"slackConfigs": []any{
						map[string]any{
							"apiURL": map[string]any{
								"key":  "url",
								"name": "slack-webhook-secret",
							},
						},
					},
				},
			},
		},
	}}

	out := describeAlertmanagerConfig(u)
	data, err := json.Marshal(out)
	assert.NoError(t, err)
	raw := string(data)

	// The CRD references Secrets by name only (secretKeyRef) and never holds
	// secret data. The curated view surfaces only receiver names and
	// config-type tags, so no secret reference or payload is emitted and no
	// redaction is required.
	assert.NotContains(t, raw, "REDACTED")
	assert.NotContains(t, raw, "slack-webhook-secret")
	assert.NotContains(t, raw, "apiURL")

	m := mustUnmarshal(t, data)
	receivers := m["spec"].(map[string]any)["receivers"].([]any)
	assert.Len(t, receivers, 1)
	assert.Equal(t, "slack", receivers[0].(map[string]any)["name"])
	assert.Equal(t, []any{"slack"}, receivers[0].(map[string]any)["types"])
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

func TestDescribePrometheusRule(t *testing.T) {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "monitoring.coreos.com/v1",
		"kind":       "PrometheusRule",
		"metadata":   map[string]any{"name": "rules", "namespace": "monitoring"},
		"spec": map[string]any{
			"groups": []any{
				map[string]any{
					"name":     "group1",
					"interval": "30s",
					"rules": []any{
						map[string]any{
							"alert":       "HighErrorRate",
							"expr":        "sum(rate(errors[5m])) > 0",
							"for":         "5m",
							"labels":      map[string]any{"severity": "critical", "team": "sre"},
							"annotations": map[string]any{"summary": "high error rate"},
						},
					},
				},
			},
		},
	}}

	out := describePrometheusRule(u)
	data, err := json.Marshal(out)
	assert.NoError(t, err)
	m := mustUnmarshal(t, data)

	assert.Equal(t, "PrometheusRule", m["kind"])
	assert.Equal(t, "monitoring.coreos.com/v1", m["apiVersion"])

	groups := m["spec"].(map[string]any)["groups"].([]any)
	assert.Len(t, groups, 1)
	group := groups[0].(map[string]any)
	assert.Equal(t, "group1", group["name"])
	assert.Equal(t, "30s", group["interval"])

	rules := group["rules"].([]any)
	assert.Len(t, rules, 1)
	rule := rules[0].(map[string]any)
	assert.Equal(t, "HighErrorRate", rule["alert"])
	assert.Equal(t, "sum(rate(errors[5m])) > 0", rule["expr"])
	assert.Equal(t, "5m", rule["for"])
	assert.Equal(t, "critical", rule["severity"])
	assert.Equal(t, map[string]any{"severity": "critical", "team": "sre"}, rule["labels"])
	assert.Equal(t, map[string]any{"summary": "high error rate"}, rule["annotations"])
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

func TestDescribeSilence(t *testing.T) {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "monitoring.coreos.com/v1alpha1",
		"kind":       "Silence",
		"metadata":   map[string]any{"name": "silence-1", "namespace": "monitoring"},
		"spec": map[string]any{
			"matchers": []any{
				map[string]any{"name": "alertname", "value": "HighErrorRate", "isRegex": false},
			},
			"startsAt":  "2024-01-01T00:00:00Z",
			"endsAt":    "2024-01-02T00:00:00Z",
			"createdBy": "admin",
			"comment":   "maintenance window",
		},
		"status": map[string]any{
			"state": "active",
			"conditions": []any{
				map[string]any{"type": "Reconciled", "status": "True"},
			},
		},
	}}

	out := describeSilence(u)
	data, err := json.Marshal(out)
	assert.NoError(t, err)
	m := mustUnmarshal(t, data)

	assert.Equal(t, "Silence", m["kind"])
	assert.Equal(t, "monitoring.coreos.com/v1alpha1", m["apiVersion"])

	spec := m["spec"].(map[string]any)
	assert.Equal(t, "2024-01-01T00:00:00Z", spec["startsAt"])
	assert.Equal(t, "2024-01-02T00:00:00Z", spec["endsAt"])
	assert.Equal(t, "admin", spec["createdBy"])
	assert.Equal(t, "maintenance window", spec["comment"])
	assert.Len(t, spec["matchers"].([]any), 1)

	status := m["status"].(map[string]any)
	assert.Equal(t, "active", status["state"])
	assert.Len(t, status["conditions"].([]any), 1)
}

func TestDescribeSilence_ExcludeStatus(t *testing.T) {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "monitoring.coreos.com/v1alpha1",
		"kind":       "Silence",
		"metadata":   map[string]any{"name": "silence-1", "namespace": "monitoring"},
		"spec":       map[string]any{"startsAt": "2024-01-01T00:00:00Z"},
		"status":     map[string]any{"state": "active"},
	}}

	out := describeSilence(u)
	assert.NoError(t, out.applyFieldExclusions([]string{"status"}))
	data, err := json.Marshal(out)
	assert.NoError(t, err)
	m := mustUnmarshal(t, data)
	_, ok := m["status"]
	assert.False(t, ok)
	assert.NotNil(t, m["spec"])
	assert.NotNil(t, m["metadata"])
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
		assert.NotNil(t, entry.describe, "%s should have a describe formatter", gvk.Kind)
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

func TestDescribeRawPathUnchangedForConfigMap(t *testing.T) {
	cmGVK := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"}
	entry, ok := formatterRegistry[cmGVK]
	assert.True(t, ok)
	assert.Nil(t, entry.describe, "ConfigMap must keep the raw describe path")

	cm := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata":   map[string]any{"name": "cm1", "namespace": "default"},
		"data":       map[string]any{"key1": "value1"},
	}}

	out := describeOutput{
		TypeMeta: metav1.TypeMeta{
			Kind:       cm.GetKind(),
			APIVersion: cm.GetAPIVersion(),
		},
		Metadata: unstructuredMetadata(cm),
		Spec:     cm.Object["spec"],
		Status:   cm.Object["status"],
		Data:     cm.Object["data"],
	}

	data, err := json.Marshal(out)
	assert.NoError(t, err)
	m := mustUnmarshal(t, data)

	assert.Equal(t, "ConfigMap", m["kind"])
	assert.Equal(t, "v1", m["apiVersion"])
	meta := m["metadata"].(map[string]any)
	assert.Equal(t, "cm1", meta["name"])
	assert.Equal(t, "default", meta["namespace"])
	assert.Equal(t, map[string]any{"key1": "value1"}, m["data"])
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
