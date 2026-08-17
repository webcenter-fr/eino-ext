package alertmanager

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/goccy/go-json"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newWriteTool(t *testing.T) *AlertWriteTool {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The guidance tool must NOT call Alertmanager. If it does, fail.
		t.Errorf("guidance tool must not call Alertmanager; got %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	tool, err := NewAlertWriteTool(context.Background(), Configs{"t": {Address: server.URL}})
	require.NoError(t, err)
	return tool
}

func validParams(op string, overrides func(*AlertWriteParams)) *AlertWriteParams {
	p := &AlertWriteParams{
		Instance:    "t",
		Operation:   op,
		Labels:      map[string]string{"alertname": "HighCPU", "severity": "critical"},
		Annotations: map[string]string{"summary": "high cpu"},
	}
	if overrides != nil {
		overrides(p)
	}
	return p
}

func parseManifest(t *testing.T, m json.RawMessage) map[string]any {
	t.Helper()
	var out map[string]any
	require.NoError(t, json.Unmarshal(m, &out))
	return out
}

func manifestRule(t *testing.T, m json.RawMessage) map[string]any {
	t.Helper()
	manifest := parseManifest(t, m)
	spec, ok := manifest["spec"].(map[string]any)
	require.True(t, ok, "spec must be an object")
	groups, ok := spec["groups"].([]any)
	require.True(t, ok, "groups must be an array")
	require.Len(t, groups, 1)
	group, ok := groups[0].(map[string]any)
	require.True(t, ok, "group must be an object")
	rules, ok := group["rules"].([]any)
	require.True(t, ok, "rules must be an array")
	require.Len(t, rules, 1)
	rule, ok := rules[0].(map[string]any)
	require.True(t, ok, "rule must be an object")
	return rule
}

func TestAlertWriteCreateGuidance(t *testing.T) {
	tool := newWriteTool(t)

	result, err := tool.Invoke(context.Background(), validParams("create", nil))
	require.NoError(t, err)

	var out AlertGuidanceOutput
	require.NoError(t, json.Unmarshal([]byte(result), &out))

	assert.Equal(t, "create", out.Action)
	assert.Contains(t, out.RecommendedTools, "kubernetes_resource_apply")

	require.Len(t, out.RecommendedCRDs, 1)
	assert.Equal(t, "PrometheusRule", out.RecommendedCRDs[0].Name)
	assert.Equal(t, "monitoring.coreos.com/v1", out.RecommendedCRDs[0].APIVersion)
	assert.Equal(t, "PrometheusRule", out.RecommendedCRDs[0].Kind)
	assert.True(t, out.RecommendedCRDs[0].Namespaced)

	require.Len(t, out.Examples, 1)
	assert.Equal(t, "PrometheusRule", out.Examples[0].CRD)
	assert.Equal(t, "kubernetes_resource_apply", out.Examples[0].Tool)
	assert.Equal(t, "apply", out.Examples[0].Action)

	manifest := parseManifest(t, out.Examples[0].Manifest)
	assert.Equal(t, "monitoring.coreos.com/v1", manifest["apiVersion"])
	assert.Equal(t, "PrometheusRule", manifest["kind"])

	rule := manifestRule(t, out.Examples[0].Manifest)
	assert.Equal(t, "HighCPU", rule["alert"])
	assert.Equal(t, "REPLACE_WITH_PROMQL_EXPRESSION", rule["expr"])

	ruleLabels, ok := rule["labels"].(map[string]any)
	require.True(t, ok, "rule labels must be an object")
	assert.Equal(t, "critical", ruleLabels["severity"])
	_, hasAlertname := ruleLabels["alertname"]
	assert.False(t, hasAlertname, "alertname must not appear in rule labels")

	ruleAnnotations, ok := rule["annotations"].(map[string]any)
	require.True(t, ok, "rule annotations must be an object")
	assert.Equal(t, "high cpu", ruleAnnotations["summary"])
}

func TestAlertWriteUpdateGuidance(t *testing.T) {
	tool := newWriteTool(t)

	result, err := tool.Invoke(context.Background(), validParams("update", nil))
	require.NoError(t, err)

	var out AlertGuidanceOutput
	require.NoError(t, json.Unmarshal([]byte(result), &out))

	assert.Equal(t, "update", out.Action)
	assert.Contains(t, out.Message, "update")
	assert.Contains(t, out.RecommendedTools, "kubernetes_resource_apply")

	require.Len(t, out.RecommendedCRDs, 1)
	assert.Equal(t, "PrometheusRule", out.RecommendedCRDs[0].Name)
	assert.Equal(t, "monitoring.coreos.com/v1", out.RecommendedCRDs[0].APIVersion)
	assert.True(t, out.RecommendedCRDs[0].Namespaced)

	require.Len(t, out.Examples, 1)
	assert.Equal(t, "PrometheusRule", out.Examples[0].CRD)
	assert.Equal(t, "kubernetes_resource_apply", out.Examples[0].Tool)
	assert.Equal(t, "apply", out.Examples[0].Action)

	rule := manifestRule(t, out.Examples[0].Manifest)
	assert.Equal(t, "HighCPU", rule["alert"])
	assert.Equal(t, "REPLACE_WITH_PROMQL_EXPRESSION", rule["expr"])
}

func TestAlertWriteDeleteGuidance(t *testing.T) {
	tool := newWriteTool(t)

	result, err := tool.Invoke(context.Background(), validParams("delete", nil))
	require.NoError(t, err)

	var out AlertGuidanceOutput
	require.NoError(t, json.Unmarshal([]byte(result), &out))

	assert.Equal(t, "delete", out.Action)
	assert.Contains(t, out.RecommendedTools, "kubernetes_resource_delete")
	assert.Contains(t, out.RecommendedTools, "kubernetes_resource_apply")

	require.Len(t, out.RecommendedCRDs, 2)
	assert.Equal(t, "PrometheusRule", out.RecommendedCRDs[0].Name)
	assert.Equal(t, "monitoring.coreos.com/v1", out.RecommendedCRDs[0].APIVersion)
	assert.Equal(t, "Silence", out.RecommendedCRDs[1].Name)
	assert.Equal(t, "monitoring.coreos.com/v1alpha1", out.RecommendedCRDs[1].APIVersion)

	require.Len(t, out.Examples, 2)

	assert.Equal(t, "PrometheusRule", out.Examples[0].CRD)
	assert.Equal(t, "kubernetes_resource_delete", out.Examples[0].Tool)
	assert.Equal(t, "delete", out.Examples[0].Action)

	assert.Equal(t, "Silence", out.Examples[1].CRD)
	assert.Equal(t, "kubernetes_resource_apply", out.Examples[1].Tool)
	assert.Equal(t, "apply", out.Examples[1].Action)

	silence := parseManifest(t, out.Examples[1].Manifest)
	assert.Equal(t, "monitoring.coreos.com/v1alpha1", silence["apiVersion"])
	assert.Equal(t, "Silence", silence["kind"])

	spec, ok := silence["spec"].(map[string]any)
	require.True(t, ok, "spec must be an object")

	matchers, ok := spec["matchers"].([]any)
	require.True(t, ok, "matchers must be an array")
	require.Len(t, matchers, 2)

	m0, ok := matchers[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "alertname", m0["name"])
	assert.Equal(t, "HighCPU", m0["value"])
	assert.Equal(t, false, m0["isRegex"])

	m1, ok := matchers[1].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "severity", m1["name"])
	assert.Equal(t, "critical", m1["value"])
	assert.Equal(t, false, m1["isRegex"])

	startsAt, err := time.Parse(time.RFC3339, spec["startsAt"].(string))
	require.NoError(t, err)
	endsAt, err := time.Parse(time.RFC3339, spec["endsAt"].(string))
	require.NoError(t, err)
	assert.True(t, endsAt.After(startsAt), "endsAt must be after startsAt")
}

func TestAlertWriteCreateGuidanceOmitsEmptyLabelsAndAnnotations(t *testing.T) {
	tool := newWriteTool(t)

	result, err := tool.Invoke(context.Background(), &AlertWriteParams{
		Instance:  "t",
		Operation: "create",
		Labels:    map[string]string{"alertname": "OnlyName"},
	})
	require.NoError(t, err)

	var out AlertGuidanceOutput
	require.NoError(t, json.Unmarshal([]byte(result), &out))

	require.Len(t, out.Examples, 1)
	rule := manifestRule(t, out.Examples[0].Manifest)
	assert.Equal(t, "OnlyName", rule["alert"])

	_, hasLabels := rule["labels"]
	assert.False(t, hasLabels, "labels key must be omitted when only alertname is provided")
	_, hasAnnotations := rule["annotations"]
	assert.False(t, hasAnnotations, "annotations key must be omitted when none are provided")
}

func TestAlertWriteRequiresAlertname(t *testing.T) {
	for _, op := range []string{"create", "update", "delete"} {
		t.Run(op, func(t *testing.T) {
			tool := newWriteTool(t)
			_, err := tool.Invoke(context.Background(), &AlertWriteParams{
				Instance:  "t",
				Operation: op,
				Labels:    map[string]string{"instance": "srv1"},
			})
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "labels must include 'alertname'")
		})
	}
}

func TestAlertWriteInvalidOperation(t *testing.T) {
	tool := newWriteTool(t)
	_, err := tool.Invoke(context.Background(), &AlertWriteParams{
		Instance:  "t",
		Operation: "foo",
		Labels:    map[string]string{"alertname": "HighCPU"},
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid parameters")
}

func TestAlertWriteMissingInstance(t *testing.T) {
	for _, op := range []string{"create", "update", "delete"} {
		t.Run(op, func(t *testing.T) {
			tool := newWriteTool(t)
			_, err := tool.Invoke(context.Background(), &AlertWriteParams{
				Instance:  "nope",
				Operation: op,
				Labels:    map[string]string{"alertname": "HighCPU"},
			})
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "nope")
		})
	}
}

func TestAlertWriteInputSizeLimits(t *testing.T) {
	t.Run("too many labels rejected", func(t *testing.T) {
		tool := newWriteTool(t)
		labels := make(map[string]string, 66)
		for i := 0; i < 66; i++ {
			labels[fmt.Sprintf("l%d", i)] = "v"
		}
		labels["alertname"] = "HighCPU"
		_, err := tool.Invoke(context.Background(), &AlertWriteParams{
			Instance:  "t",
			Operation: "create",
			Labels:    labels,
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid parameters")
	})

	t.Run("too many annotations rejected", func(t *testing.T) {
		tool := newWriteTool(t)
		annotations := make(map[string]string, 65)
		for i := 0; i < 65; i++ {
			annotations[fmt.Sprintf("a%d", i)] = "v"
		}
		_, err := tool.Invoke(context.Background(), &AlertWriteParams{
			Instance:    "t",
			Operation:   "create",
			Labels:      map[string]string{"alertname": "HighCPU"},
			Annotations: annotations,
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid parameters")
	})
}

func TestAlertWriteNoExternalCall(t *testing.T) {
	for _, op := range []string{"create", "update", "delete"} {
		t.Run(op, func(t *testing.T) {
			tool := newWriteTool(t)
			result, err := tool.Invoke(context.Background(), validParams(op, nil))
			require.NoError(t, err)

			var out AlertGuidanceOutput
			require.NoError(t, json.Unmarshal([]byte(result), &out))
			assert.Equal(t, op, out.Action)
			// If the tool had made any HTTP call, the httptest handler in
			// newWriteTool would have t.Errorf'd and failed this test.
		})
	}
}

func TestSanitizeRuleName(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"HighCPU", "highcpu"},
		{"High/CPU", "high-cpu"},
		{"1Alert", "rule-1alert"},
		{"123", "rule-123"},
		{"---", "rule"},
		{"", "rule"},
		{"-High-", "high"},
		{"High--CPU", "high--cpu"},
		{"Foo.Bar", "foo-bar"},
		{"Älert", "lert"},
		{"Ünïcode", "n-code"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			assert.Equal(t, tt.want, sanitizeRuleName(tt.in))
		})
	}
}

func TestSanitizeRuleNameLengthCap(t *testing.T) {
	// A 300-char alertname must be capped to maxRFC1123NameLen (253) and the
	// result must remain a valid RFC 1123 label (no trailing hyphen).
	long := strings.Repeat("a", 300)
	got := sanitizeRuleName(long)
	assert.LessOrEqual(t, len(got), maxRFC1123NameLen)
	assert.NotEmpty(t, got)
	assert.False(t, strings.HasSuffix(got, "-"), "must not end with hyphen after truncation")

	// The silence name derived from a long alertname must also stay within
	// the Kubernetes metadata.name limit.
	ruleName := sanitizeRuleName(long)
	silenceNameRule := truncateRFC1123(ruleName, maxRFC1123NameLen-len(silenceNamePrefix)-1-11)
	silenceName := fmt.Sprintf("%s%s-%d", silenceNamePrefix, silenceNameRule, time.Now().Unix())
	assert.LessOrEqual(t, len(silenceName), maxRFC1123NameLen, "silence name must fit RFC 1123 limit")
}

func TestWithoutAlertname(t *testing.T) {
	got := withoutAlertname(map[string]string{"alertname": "X", "severity": "y"})
	assert.Equal(t, "y", got["severity"])
	_, ok := got["alertname"]
	assert.False(t, ok, "alertname must be removed")
}

func TestToSilenceMatchers(t *testing.T) {
	matchers := toSilenceMatchers(map[string]string{"alertname": "X", "b": "2", "a": "1"})

	require.Len(t, matchers, 3)

	assert.Equal(t, "alertname", matchers[0]["name"])
	assert.Equal(t, "X", matchers[0]["value"])
	assert.Equal(t, false, matchers[0]["isRegex"])

	assert.Equal(t, "a", matchers[1]["name"])
	assert.Equal(t, "1", matchers[1]["value"])
	assert.Equal(t, false, matchers[1]["isRegex"])

	assert.Equal(t, "b", matchers[2]["name"])
	assert.Equal(t, "2", matchers[2]["value"])
	assert.Equal(t, false, matchers[2]["isRegex"])
}
