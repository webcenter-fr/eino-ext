package alertmanager

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/goccy/go-json"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/marshal"
)

const alertWriteToolDescription = `
** General Purpose **
This tool does NOT mutate Alertmanager. Alertmanager alert lifecycle
(create / update / delete / silence) must be managed declaratively via
Kubernetes CRDs from the prometheus-operator (monitoring.coreos.com), applied
through the kubernetes_resource_* tools. This tool returns structured
guidance telling you which CRD and which kubernetes tool to use, plus a
ready-to-adapt manifest skeleton built from the labels/annotations you
provide.

The required 'operation' param selects the intent:
- create: define a new alerting rule (PrometheusRule).
- update: modify an existing alerting rule (PrometheusRule).
- delete: stop an alert — either remove/silence the generating PrometheusRule
  or apply a temporary Silence CRD. Both options are returned.

** Read-only against Alertmanager **
This tool never calls the Alertmanager HTTP API. It only validates params and
returns guidance. Use the kubernetes_resource_* tools to perform the actual
write.

** Required labels **
All operations require a 'labels' map that includes the 'alertname' label.
`

// alertnameLabel is the required alert label that identifies an alert.
const alertnameLabel = "alertname"

// CRD kinds and apiVersions the guidance points the LLM to.
const (
	prometheusRuleKind       = "PrometheusRule"
	prometheusRuleAPIVersion = "monitoring.coreos.com/v1"
	silenceKind              = "Silence"
	silenceAPIVersion        = "monitoring.coreos.com/v1alpha1"
)

// Kubernetes tool names the guidance recommends for applying CRDs.
const (
	kubernetesResourceApply  = "kubernetes_resource_apply"
	kubernetesResourceCreate = "kubernetes_resource_create"
	kubernetesResourcePatch  = "kubernetes_resource_patch"
	kubernetesResourceDelete = "kubernetes_resource_delete"
)

// targetNamespacePlaceholder marks where the LLM must substitute the target
// namespace before applying a manifest.
const targetNamespacePlaceholder = "REPLACE_WITH_TARGET_NAMESPACE"

// maxRFC1123NameLen is the maximum length of a Kubernetes metadata.name
// (DNS-1123 subdomain, RFC 1123). Capping the sanitized name prevents the
// guidance from emitting a manifest Kubernetes would reject outright and
// keeps the derived Silence name within the same limit.
const maxRFC1123NameLen = 253

// silenceNamePrefix is the prefix used for derived Silence metadata.name
// values. It is also used to reserve space when truncating the rule-name
// portion so the full "silence-<rule>-<unix>" name stays within
// maxRFC1123NameLen.
const silenceNamePrefix = "silence-"

// AlertWriteParams defines the parameters for the alertmanager_alert_write
// guidance tool. The tool does NOT mutate Alertmanager; it returns guidance
// pointing the LLM to the Kubernetes CRD tools.
type AlertWriteParams struct {
	Instance    string            `json:"instance" validate:"required" jsonschema:"(required) The Alertmanager instance name (context only; no live call is made)."`
	Operation   string            `json:"operation" validate:"required,oneof=create update delete" jsonschema:"(required) Intent: 'create', 'update', or 'delete'."`
	Labels      map[string]string `json:"labels" validate:"required,min=1,max=64,dive,keys,required,endkeys,required" jsonschema:"(required) Alert labels; must include 'alertname'. Used to pre-fill the example manifest (PrometheusRule rule labels, Silence matchers)."`
	Annotations map[string]string `json:"annotations,omitempty" validate:"omitempty,max=64,dive,keys,required,endkeys,required" jsonschema:"(optional) Alert annotations. Used to pre-fill the example PrometheusRule rule annotations."`
}

// RecommendedCRD names a Kubernetes CRD the LLM should use.
type RecommendedCRD struct {
	Name       string `json:"name"`       // e.g. "PrometheusRule"
	APIVersion string `json:"apiVersion"` // e.g. "monitoring.coreos.com/v1"
	Kind       string `json:"kind"`       // e.g. "PrometheusRule"
	Namespaced bool   `json:"namespaced"` // true for all three CRDs
	Purpose    string `json:"purpose"`    // short description of when to use it
}

// ManifestExample is one ready-to-adapt manifest skeleton plus the
// kubernetes tool the LLM should drive with it.
type ManifestExample struct {
	CRD      string          `json:"crd"`      // "PrometheusRule" | "Silence"
	Tool     string          `json:"tool"`     // e.g. "kubernetes_resource_apply"
	Action   string          `json:"action"`   // "apply" | "create" | "delete"
	Manifest json.RawMessage `json:"manifest"` // JSON skeleton (YAML not used; kubernetes tools take JSON)
	Comment  string          `json:"comment,omitempty"`
}

// AlertGuidanceOutput is the structured guidance returned by
// alertmanager_alert_write. It never mutates Alertmanager.
type AlertGuidanceOutput struct {
	Action           string            `json:"action"`           // requested operation: "create" | "update" | "delete"
	Message          string            `json:"message"`          // human/LLM-readable explanation
	RecommendedTools []string          `json:"recommendedTools"` // kubernetes tool names, e.g. ["kubernetes_resource_apply"]
	RecommendedCRDs  []RecommendedCRD  `json:"recommendedCRDs"`  // CRDs the LLM should target
	Examples         []ManifestExample `json:"examples"`         // one or more manifest skeletons
	Notes            []string          `json:"notes,omitempty"`  // extra guidance (e.g. delete dual options, routing hint)
}

// AlertWriteTool is an eino tool that returns CRD guidance instead of
// mutating Alertmanager. It implements tool.InvokableTool.
type AlertWriteTool struct {
	*baseTool
	tool.InvokableTool
}

// NewAlertWriteTool creates a new AlertWriteTool. It still builds a baseTool
// (so the Instance param can be validated against known instances), but
// Invoke never calls the Alertmanager API.
func NewAlertWriteTool(ctx context.Context, configs Configs) (*AlertWriteTool, error) {
	base, err := newBaseTool(ctx, configs)
	if err != nil {
		return nil, err
	}
	writeTool := &AlertWriteTool{baseTool: base}
	t, err := utils.InferTool(alertWriteToolName, alertWriteToolDescription, writeTool.Invoke)
	if err != nil {
		return nil, err
	}
	writeTool.InvokableTool = t
	return writeTool, nil
}

// Invoke executes the AlertWriteTool with the given parameters.
func (t *AlertWriteTool) Invoke(ctx context.Context, params *AlertWriteParams) (string, error) {
	if err := validateParams(params); err != nil {
		return "", err
	}
	// Validate instance is known (consistent with the read tool). Does NOT
	// make an HTTP call.
	if _, err := t.client(params.Instance); err != nil {
		return "", err
	}
	if params.Labels[alertnameLabel] == "" {
		return "", errors.Errorf("labels must include 'alertname'")
	}

	switch params.Operation {
	case "create", "update":
		return marshalString(buildRuleGuidance(params.Operation, params.Labels, params.Annotations)), nil
	case "delete":
		return marshalString(buildDeleteGuidance(params.Labels)), nil
	default:
		return "", errors.Errorf("unsupported operation %q", params.Operation)
	}
}

func buildRuleGuidance(operation string, labels, annotations map[string]string) AlertGuidanceOutput {
	alertname := labels[alertnameLabel]
	ruleLabels := withoutAlertname(labels)

	rule := map[string]any{
		"alert": alertname,
		"expr":  "REPLACE_WITH_PROMQL_EXPRESSION",
		"for":   "5m",
	}
	if len(ruleLabels) > 0 {
		rule["labels"] = ruleLabels
	}
	if len(annotations) > 0 {
		rule["annotations"] = annotations
	}

	prometheusRuleManifest := map[string]any{
		"apiVersion": prometheusRuleAPIVersion,
		"kind":       prometheusRuleKind,
		"metadata": map[string]any{
			"name":      sanitizeRuleName(alertname),
			"namespace": targetNamespacePlaceholder,
			"labels": map[string]any{
				"prometheus": "REPLACE_WITH_PROMETHEUS_CR_SELECTOR",
			},
		},
		"spec": map[string]any{
			"groups": []any{
				map[string]any{
					"name": alertname,
					"rules": []any{
						rule,
					},
				},
			},
		},
	}

	return AlertGuidanceOutput{
		Action:           operation,
		Message:          fmt.Sprintf("Alertmanager is read-only. To %s the alert %q, declare it as a PrometheusRule CRD and apply it via the kubernetes_resource_apply tool. The example manifest below is pre-filled with your labels and annotations; fill in expr, for, namespace, and the Prometheus CR selector label.", operation, alertname),
		RecommendedTools: []string{kubernetesResourceApply, kubernetesResourceCreate, kubernetesResourcePatch},
		RecommendedCRDs: []RecommendedCRD{
			recommendedCRD(prometheusRuleKind, prometheusRuleAPIVersion, "Defines alerting rules (alert + expr + for + labels + annotations) evaluated by Prometheus."),
		},
		Examples: []ManifestExample{
			manifestExample(prometheusRuleKind, kubernetesResourceApply, "apply", prometheusRuleManifest,
				"For update, apply the modified PrometheusRule (server-side apply will reconcile the existing rule). For create, the same apply creates it if absent."),
		},
		Notes: []string{
			"If your intent is to change alert ROUTING or receivers (not the rule itself), use the AlertmanagerConfig CRD (apiVersion monitoring.coreos.com/v1alpha1, kind AlertmanagerConfig) via kubernetes_resource_apply instead.",
		},
	}
}

func buildDeleteGuidance(labels map[string]string) AlertGuidanceOutput {
	alertname := labels[alertnameLabel]
	ruleName := sanitizeRuleName(alertname)
	matchers := toSilenceMatchers(labels)
	now := time.Now().UTC()
	endsAt := now.Add(2 * time.Hour)

	// Option A: delete the generating PrometheusRule.
	prometheusRuleDeleteManifest := map[string]any{
		"apiVersion": prometheusRuleAPIVersion,
		"kind":       prometheusRuleKind,
		"metadata": map[string]any{
			"name":      ruleName,
			"namespace": targetNamespacePlaceholder,
		},
	}

	// Option B: apply a temporary Silence CRD. The rule-name portion is
	// truncated so the full "silence-<rule>-<unix>" name stays within the
	// Kubernetes metadata.name limit (maxRFC1123NameLen).
	silenceNameRule := truncateRFC1123(ruleName, maxRFC1123NameLen-len(silenceNamePrefix)-1-11)
	silenceManifest := map[string]any{
		"apiVersion": silenceAPIVersion,
		"kind":       silenceKind,
		"metadata": map[string]any{
			"name":      fmt.Sprintf("%s%s-%d", silenceNamePrefix, silenceNameRule, now.Unix()),
			"namespace": targetNamespacePlaceholder,
		},
		"spec": map[string]any{
			"matchers":  matchers,
			"startsAt":  now.Format(time.RFC3339),
			"endsAt":    endsAt.Format(time.RFC3339),
			"createdBy": "eino-agent",
			"comment":   "Silence created by alertmanager_alert_write guidance tool",
		},
	}

	return AlertGuidanceOutput{
		Action:           "delete",
		Message:          fmt.Sprintf("Alertmanager is read-only. To stop the alert %q, choose one of two declarative options: (A) permanently remove or update the generating PrometheusRule CRD, or (B) temporarily suppress it with a Silence CRD. Both example manifests are provided below.", alertname),
		RecommendedTools: []string{kubernetesResourceDelete, kubernetesResourceApply},
		RecommendedCRDs: []RecommendedCRD{
			recommendedCRD(prometheusRuleKind, prometheusRuleAPIVersion, "Source of the alert. Delete or update this CRD to permanently stop the alert firing."),
			recommendedCRD(silenceKind, silenceAPIVersion, "Temporary suppression of matching alerts without touching the rule. Expires at endsAt."),
		},
		Examples: []ManifestExample{
			manifestExample(prometheusRuleKind, kubernetesResourceDelete, "delete", prometheusRuleDeleteManifest,
				"Pass kind=\"PrometheusRule\", name=<metadata.name>, namespace=<metadata.namespace> to kubernetes_resource_delete. This permanently stops the alert (rule removal)."),
			manifestExample(silenceKind, kubernetesResourceApply, "apply", silenceManifest,
				"Temporary suppression. Adjust endsAt to the desired silence window. The silence auto-expires; the rule keeps firing afterwards."),
		},
		Notes: []string{
			"Prefer option A (delete/update PrometheusRule) when the alert should no longer exist. Prefer option B (Silence) for temporary maintenance windows.",
			"If the alert is produced by a rule you do not own, use the Silence CRD (option B) — do not delete someone else's PrometheusRule.",
			"If your intent is to change alert routing rather than stop the alert, use the AlertmanagerConfig CRD (apiVersion monitoring.coreos.com/v1alpha1, kind AlertmanagerConfig) via kubernetes_resource_apply.",
		},
	}
}

// recommendedCRD returns a RecommendedCRD for the given CRD. Every CRD the
// guidance targets has Kind == Name and is namespaced, so only the name,
// apiVersion, and purpose vary.
func recommendedCRD(name, apiVersion, purpose string) RecommendedCRD {
	return RecommendedCRD{
		Name:       name,
		APIVersion: apiVersion,
		Kind:       name,
		Namespaced: true,
		Purpose:    purpose,
	}
}

// manifestExample builds a ManifestExample from a manifest skeleton, encoding
// it as JSON for the kubernetes_resource_* tools.
func manifestExample(crd, tool, action string, manifest map[string]any, comment string) ManifestExample {
	return ManifestExample{
		CRD:      crd,
		Tool:     tool,
		Action:   action,
		Manifest: json.RawMessage(marshal.MustMarshal(manifest)),
		Comment:  comment,
	}
}

// withoutAlertname returns a copy of labels with the alertname key removed.
// Used to build PrometheusRule rule.labels (which must not contain alertname;
// alertname is the rule's `alert` field).
func withoutAlertname(labels map[string]string) map[string]string {
	out := make(map[string]string, len(labels))
	for k, v := range labels {
		if k == alertnameLabel {
			continue
		}
		out[k] = v
	}
	return out
}

// sanitizeRuleName derives a Kubernetes-safe metadata.name for the
// PrometheusRule from the alertname. It lowercases and replaces any
// character outside [a-z0-9-] with '-'. The result is RFC1123-compliant
// (lowercase, alphanumeric and hyphens). An empty result becomes "rule", and
// a result starting with a digit is prefixed with "rule-". The result is
// capped at maxRFC1123NameLen characters so the emitted manifest is not
// rejected by Kubernetes on length grounds.
func sanitizeRuleName(alertname string) string {
	s := strings.ToLower(alertname)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "rule"
	}
	if out[0] < 'a' || out[0] > 'z' { // starts with digit
		out = "rule-" + out
	}
	return truncateRFC1123(out, maxRFC1123NameLen)
}

// truncateRFC1123 trims s to at most maxLen characters, then strips trailing
// hyphens so the result remains a valid RFC 1123 label. It returns "rule" if
// the result is empty (defensive — should not happen for sane inputs since
// sanitizeRuleName already guarantees a non-empty, letter-prefixed string).
func truncateRFC1123(s string, maxLen int) string {
	if maxLen < 1 {
		return "rule"
	}
	if len(s) > maxLen {
		s = s[:maxLen]
	}
	s = strings.TrimRight(s, "-")
	if s == "" {
		return "rule"
	}
	return s
}

// silenceMatcher builds a Silence CRD matcher object with exact (non-regex)
// matching.
func silenceMatcher(name, value string) map[string]any {
	return map[string]any{
		"name":    name,
		"value":   value,
		"isRegex": false,
	}
}

// toSilenceMatchers converts the alert labels into Silence CRD matcher
// objects: {name, value, isRegex:false}. alertname is included as the first
// matcher. Deterministic order: sorted by key, with alertname first.
func toSilenceMatchers(labels map[string]string) []map[string]any {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		if k != alertnameLabel {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	matchers := make([]map[string]any, 0, len(keys)+1)
	matchers = append(matchers, silenceMatcher(alertnameLabel, labels[alertnameLabel]))
	for _, k := range keys {
		matchers = append(matchers, silenceMatcher(k, labels[k]))
	}
	return matchers
}
