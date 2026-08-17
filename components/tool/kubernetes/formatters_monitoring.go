package kubernetes

import (
	"sort"

	"github.com/goccy/go-json"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/marshal"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// registerMonitoringFormatters registers curated list/describe formatters for
// the four prometheus-operator "alerting" CRDs from monitoring.coreos.com.
// These kinds have no typed Go API dependency in this package, so newObj is nil
// and the formatters operate directly on *unstructured.Unstructured.
func registerMonitoringFormatters(reg map[schema.GroupVersionKind]formatterEntry) {
	add := func(gvk schema.GroupVersionKind, list func(*unstructured.Unstructured) json.RawMessage, describe describeFormatter) {
		reg[gvk] = formatterEntry{
			newObj:   nil,
			format:   unstructuredListFormatter(list),
			describe: describe,
		}
	}

	add(schema.GroupVersionKind{Group: "monitoring.coreos.com", Version: "v1", Kind: "Alertmanager"}, formatAlertmanagerList, describeAlertmanager)
	add(schema.GroupVersionKind{Group: "monitoring.coreos.com", Version: "v1alpha1", Kind: "AlertmanagerConfig"}, formatAlertmanagerConfigList, describeAlertmanagerConfig)
	add(schema.GroupVersionKind{Group: "monitoring.coreos.com", Version: "v1", Kind: "PrometheusRule"}, formatPrometheusRuleList, describePrometheusRule)
	add(schema.GroupVersionKind{Group: "monitoring.coreos.com", Version: "v1alpha1", Kind: "Silence"}, formatSilenceList, describeSilence)
}

// unstructuredListFormatter adapts a per-kind unstructured formatter into a
// listFormatter, falling back to the default name/namespace/status formatter
// when the object is not an *unstructured.Unstructured (which cannot happen via
// formatListItem, since these kinds are registered with newObj == nil).
func unstructuredListFormatter(fn func(*unstructured.Unstructured) json.RawMessage) listFormatter {
	return func(o runtime.Object) json.RawMessage {
		u, ok := o.(*unstructured.Unstructured)
		if !ok || u == nil {
			return defaultListFormatter(&unstructured.Unstructured{})
		}
		return fn(u)
	}
}

// Shared view sub-structs.

type conditionView struct {
	Type               string `json:"type"`
	Status             string `json:"status"`
	Reason             string `json:"reason,omitempty"`
	Message            string `json:"message,omitempty"`
	LastTransitionTime string `json:"lastTransitionTime,omitempty"`
}

type matcherView struct {
	Name    string `json:"name"`
	Value   string `json:"value"`
	IsRegex bool   `json:"isRegex"`
}

type alertmanagerSpecView struct {
	Replicas  int32          `json:"replicas"`
	Version   string         `json:"version,omitempty"`
	Image     string         `json:"image,omitempty"`
	Paused    bool           `json:"paused"`
	LogLevel  string         `json:"logLevel,omitempty"`
	Resources map[string]any `json:"resources,omitempty"`
	Storage   map[string]any `json:"storage,omitempty"`
}

type alertmanagerStatusView struct {
	Paused              bool            `json:"paused"`
	Replicas            int32           `json:"replicas"`
	UpdatedReplicas     int32           `json:"updatedReplicas"`
	AvailableReplicas   int32           `json:"availableReplicas"`
	UnavailableReplicas int32           `json:"unavailableReplicas"`
	Conditions          []conditionView `json:"conditions,omitempty"`
}

type routeView struct {
	Receiver       string      `json:"receiver,omitempty"`
	GroupBy        []string    `json:"groupBy,omitempty"`
	GroupWait      string      `json:"groupWait,omitempty"`
	GroupInterval  string      `json:"groupInterval,omitempty"`
	RepeatInterval string      `json:"repeatInterval,omitempty"`
	Routes         []routeView `json:"routes,omitempty"`
}

// receiverView surfaces only the receiver name and config-type tags. Receiver
// configs reference Kubernetes Secrets by name (e.g. slackConfigs[].apiURL
// secretKeyRef) but never contain secret data, so no redaction is required.
type receiverView struct {
	Name  string   `json:"name"`
	Types []string `json:"types,omitempty"`
}

type alertmanagerConfigSpecView struct {
	Route        *routeView       `json:"route,omitempty"`
	Receivers    []receiverView   `json:"receivers,omitempty"`
	InhibitRules []map[string]any `json:"inhibitRules,omitempty"`
}

type alertmanagerConfigStatusView struct {
	Conditions []conditionView `json:"conditions,omitempty"`
}

type ruleGroupView struct {
	Name     string     `json:"name"`
	Interval string     `json:"interval,omitempty"`
	Rules    []ruleView `json:"rules,omitempty"`
}

type ruleView struct {
	Alert       string            `json:"alert,omitempty"`
	Record      string            `json:"record,omitempty"`
	Expr        string            `json:"expr"`
	For         string            `json:"for,omitempty"`
	Severity    string            `json:"severity,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

type prometheusRuleSpecView struct {
	Groups []ruleGroupView `json:"groups,omitempty"`
}

type prometheusRuleStatusView struct {
	Conditions []conditionView `json:"conditions,omitempty"`
}

type silenceSpecView struct {
	Matchers  []matcherView `json:"matchers,omitempty"`
	StartsAt  string        `json:"startsAt,omitempty"`
	EndsAt    string        `json:"endsAt,omitempty"`
	CreatedBy string        `json:"createdBy,omitempty"`
	Comment   string        `json:"comment,omitempty"`
}

type silenceStatusView struct {
	State      string          `json:"state,omitempty"`
	Conditions []conditionView `json:"conditions,omitempty"`
}

// monitoringDescribe assembles the shared skeleton of a curated describe
// output: a fixed TypeMeta plus the resource metadata, with caller-supplied
// spec and status views.
func monitoringDescribe(u *unstructured.Unstructured, kind, apiVersion string, spec, status any) describeOutput {
	return describeOutput{
		TypeMeta: metav1.TypeMeta{Kind: kind, APIVersion: apiVersion},
		Metadata: unstructuredMetadata(u),
		Spec:     spec,
		Status:   status,
	}
}

// extractConditions flattens both status.conditions[] (Alertmanager) and
// status.bindings[].conditions[] (AlertmanagerConfig / PrometheusRule) into a
// single []conditionView.
func extractConditions(status map[string]any) []conditionView {
	if status == nil {
		return nil
	}
	var out []conditionView
	appendConditions := func(conds []map[string]any) {
		for _, cm := range conds {
			out = append(out, conditionView{
				Type:               uString(cm, "type"),
				Status:             uString(cm, "status"),
				Reason:             uString(cm, "reason"),
				Message:            uString(cm, "message"),
				LastTransitionTime: uString(cm, "lastTransitionTime"),
			})
		}
	}
	appendConditions(uMapSlice(status, "conditions"))
	for _, bm := range uMapSlice(status, "bindings") {
		appendConditions(uMapSlice(bm, "conditions"))
	}
	return out
}

func extractMatchers(spec map[string]any) []matcherView {
	raw := uMapSlice(spec, "matchers")
	if raw == nil {
		return nil
	}
	out := make([]matcherView, 0, len(raw))
	for _, m := range raw {
		out = append(out, matcherView{
			Name:    uString(m, "name"),
			Value:   uString(m, "value"),
			IsRegex: uBool(m, "isRegex"),
		})
	}
	return out
}

// maxRouteDepth is the number of nested route levels to expand before
// flattening deeper routes into a receiver-only summary.
const maxRouteDepth = 1

// extractRoute builds a routeView, capping nesting at maxRouteDepth to bound
// output size and prevent cycles in malformed objects.
func extractRoute(m map[string]any, depth int) *routeView {
	if m == nil {
		return nil
	}
	rv := &routeView{
		Receiver:       uString(m, "receiver"),
		GroupBy:        uStringSlice(m, "groupBy"),
		GroupWait:      uString(m, "groupWait"),
		GroupInterval:  uString(m, "groupInterval"),
		RepeatInterval: uString(m, "repeatInterval"),
	}
	for _, rm := range uMapSlice(m, "routes") {
		if depth >= maxRouteDepth {
			rv.Routes = append(rv.Routes, routeView{Receiver: uString(rm, "receiver")})
			continue
		}
		rv.Routes = append(rv.Routes, *extractRoute(rm, depth+1))
	}
	return rv
}

func extractReceivers(spec map[string]any) []receiverView {
	raw := uMapSlice(spec, "receivers")
	if raw == nil {
		return nil
	}
	out := make([]receiverView, 0, len(raw))
	for _, rm := range raw {
		out = append(out, receiverView{
			Name:  uString(rm, "name"),
			Types: receiverTypes(rm),
		})
	}
	return out
}

// receiverTypes returns the sorted set of non-empty receiver config-slice
// types (e.g. ["slack", "webhook"]).
func receiverTypes(m map[string]any) []string {
	if m == nil {
		return nil
	}
	fields := []struct{ key, name string }{
		{"emailConfigs", "email"},
		{"webhookConfigs", "webhook"},
		{"slackConfigs", "slack"},
		{"pagerDutyConfigs", "pagerDuty"},
		{"opsgenieConfigs", "opsgenie"},
		{"pushoverConfigs", "pushover"},
		{"victoropsConfigs", "victorops"},
		{"wechatConfigs", "wechat"},
		{"telegramConfigs", "telegram"},
		{"snsConfigs", "sns"},
		{"webexConfigs", "webex"},
		{"discordConfigs", "discord"},
		{"msteamsConfigs", "msteams"},
		{"rocketchatConfigs", "rocketchat"},
		{"threemaConfigs", "threema"},
		{"alertmanagerConfigs", "alertmanager"},
	}
	var types []string
	for _, f := range fields {
		if len(uSlice(m, f.key)) > 0 {
			types = append(types, f.name)
		}
	}
	sort.Strings(types)
	return types
}

func extractRuleGroups(spec map[string]any) []ruleGroupView {
	raw := uMapSlice(spec, "groups")
	if raw == nil {
		return nil
	}
	out := make([]ruleGroupView, 0, len(raw))
	for _, gm := range raw {
		out = append(out, ruleGroupView{
			Name:     uString(gm, "name"),
			Interval: uString(gm, "interval"),
			Rules:    extractRules(uMapSlice(gm, "rules")),
		})
	}
	return out
}

func extractRules(raw []map[string]any) []ruleView {
	if raw == nil {
		return nil
	}
	out := make([]ruleView, 0, len(raw))
	for _, rm := range raw {
		labels := uStringMap(rm, "labels")
		annotations := uStringMap(rm, "annotations")
		out = append(out, ruleView{
			Alert:       uString(rm, "alert"),
			Record:      uString(rm, "record"),
			Expr:        uString(rm, "expr"),
			For:         uString(rm, "for"),
			Severity:    labels["severity"],
			Labels:      labels,
			Annotations: annotations,
		})
	}
	return out
}

func formatAlertmanagerList(u *unstructured.Unstructured) json.RawMessage {
	spec := uSpec(u)
	status := uStatus(u)
	replicas := uInt32(spec, "replicas")
	availableReplicas := uInt32(status, "availableReplicas")
	unavailableReplicas := uInt32(status, "unavailableReplicas")
	paused := uBool(spec, "paused")

	derived := ""
	switch {
	case paused:
		derived = "Paused"
	case availableReplicas >= replicas && replicas > 0:
		derived = "Available"
	case unavailableReplicas > 0:
		derived = "Degraded"
	}

	return marshal.MustMarshal(struct {
		Name              string `json:"name"`
		Namespace         string `json:"namespace"`
		Replicas          int32  `json:"replicas"`
		Version           string `json:"version,omitempty"`
		Paused            bool   `json:"paused"`
		AvailableReplicas int32  `json:"availableReplicas"`
		Status            string `json:"status,omitempty"`
	}{
		Name:              u.GetName(),
		Namespace:         u.GetNamespace(),
		Replicas:          replicas,
		Version:           uString(spec, "version"),
		Paused:            paused,
		AvailableReplicas: availableReplicas,
		Status:            derived,
	})
}

func formatAlertmanagerConfigList(u *unstructured.Unstructured) json.RawMessage {
	spec := uSpec(u)
	route := uMap(spec, "route")

	var receivers []string
	for _, rm := range uMapSlice(spec, "receivers") {
		if name := uString(rm, "name"); name != "" {
			receivers = append(receivers, name)
		}
	}

	return marshal.MustMarshal(struct {
		Name      string   `json:"name"`
		Namespace string   `json:"namespace"`
		Receiver  string   `json:"receiver,omitempty"`
		Receivers []string `json:"receivers,omitempty"`
		Routes    int      `json:"routes"`
	}{
		Name:      u.GetName(),
		Namespace: u.GetNamespace(),
		Receiver:  uString(route, "receiver"),
		Receivers: receivers,
		Routes:    len(uSlice(route, "routes")),
	})
}

// maxListAlerts caps the number of alert names surfaced in the PrometheusRule
// list view to bound output size (CWE-400). The total rule count is still
// reported in the "rules" field; use describe for the full alert list.
const maxListAlerts = 100

func formatPrometheusRuleList(u *unstructured.Unstructured) json.RawMessage {
	spec := uSpec(u)
	groups := uSlice(spec, "groups")

	totalRules := 0
	alertsTruncated := false
	var alerts []string
	severitySet := make(map[string]struct{})
	for _, gm := range mapSlice(groups) {
		for _, rm := range uMapSlice(gm, "rules") {
			totalRules++
			// Cap the surfaced alert names to bound output size; the total
			// count is still reported via "rules" so callers can detect
			// truncation and switch to describe for the full list.
			if alert := uString(rm, "alert"); alert != "" {
				if len(alerts) < maxListAlerts {
					alerts = append(alerts, alert)
				} else {
					alertsTruncated = true
				}
			}
			if sev := uString(uMap(rm, "labels"), "severity"); sev != "" {
				severitySet[sev] = struct{}{}
			}
		}
	}
	severities := make([]string, 0, len(severitySet))
	for s := range severitySet {
		severities = append(severities, s)
	}
	sort.Strings(severities)

	return marshal.MustMarshal(struct {
		Name       string   `json:"name"`
		Namespace  string   `json:"namespace"`
		Groups     int      `json:"groups"`
		Rules      int      `json:"rules"`
		Alerts     []string `json:"alerts,omitempty"`
		AlertsMore bool     `json:"alertsMore,omitempty"` // true when alerts was capped
		Severities []string `json:"severities,omitempty"`
	}{
		Name:       u.GetName(),
		Namespace:  u.GetNamespace(),
		Groups:     len(groups),
		Rules:      totalRules,
		Alerts:     alerts,
		AlertsMore: alertsTruncated,
		Severities: severities,
	})
}

func formatSilenceList(u *unstructured.Unstructured) json.RawMessage {
	spec := uSpec(u)
	status := uStatus(u)

	return marshal.MustMarshal(struct {
		Name      string        `json:"name"`
		Namespace string        `json:"namespace"`
		State     string        `json:"state,omitempty"`
		Matchers  []matcherView `json:"matchers,omitempty"`
		StartsAt  string        `json:"startsAt,omitempty"`
		EndsAt    string        `json:"endsAt,omitempty"`
		CreatedBy string        `json:"createdBy,omitempty"`
	}{
		Name:      u.GetName(),
		Namespace: u.GetNamespace(),
		State:     uString(status, "state"),
		Matchers:  extractMatchers(spec),
		StartsAt:  uString(spec, "startsAt"),
		EndsAt:    uString(spec, "endsAt"),
		CreatedBy: uString(spec, "createdBy"),
	})
}

func describeAlertmanager(u *unstructured.Unstructured) describeOutput {
	spec := uSpec(u)
	status := uStatus(u)

	return monitoringDescribe(u, "Alertmanager", "monitoring.coreos.com/v1",
		alertmanagerSpecView{
			Replicas:  uInt32(spec, "replicas"),
			Version:   uString(spec, "version"),
			Image:     uString(spec, "image"),
			Paused:    uBool(spec, "paused"),
			LogLevel:  uString(spec, "logLevel"),
			Resources: uMap(spec, "resources"),
			Storage:   uMap(spec, "storage"),
		},
		alertmanagerStatusView{
			Paused:              uBool(status, "paused"),
			Replicas:            uInt32(status, "replicas"),
			UpdatedReplicas:     uInt32(status, "updatedReplicas"),
			AvailableReplicas:   uInt32(status, "availableReplicas"),
			UnavailableReplicas: uInt32(status, "unavailableReplicas"),
			Conditions:          extractConditions(status),
		},
	)
}

func describeAlertmanagerConfig(u *unstructured.Unstructured) describeOutput {
	spec := uSpec(u)
	status := uStatus(u)

	return monitoringDescribe(u, "AlertmanagerConfig", "monitoring.coreos.com/v1alpha1",
		alertmanagerConfigSpecView{
			Route:        extractRoute(uMap(spec, "route"), 0),
			Receivers:    extractReceivers(spec),
			InhibitRules: uMapSlice(spec, "inhibitRules"),
		},
		alertmanagerConfigStatusView{
			Conditions: extractConditions(status),
		},
	)
}

func describePrometheusRule(u *unstructured.Unstructured) describeOutput {
	spec := uSpec(u)
	status := uStatus(u)

	return monitoringDescribe(u, "PrometheusRule", "monitoring.coreos.com/v1",
		prometheusRuleSpecView{
			Groups: extractRuleGroups(spec),
		},
		prometheusRuleStatusView{
			Conditions: extractConditions(status),
		},
	)
}

func describeSilence(u *unstructured.Unstructured) describeOutput {
	spec := uSpec(u)
	status := uStatus(u)

	return monitoringDescribe(u, "Silence", "monitoring.coreos.com/v1alpha1",
		silenceSpecView{
			Matchers:  extractMatchers(spec),
			StartsAt:  uString(spec, "startsAt"),
			EndsAt:    uString(spec, "endsAt"),
			CreatedBy: uString(spec, "createdBy"),
			Comment:   uString(spec, "comment"),
		},
		silenceStatusView{
			State:      uString(status, "state"),
			Conditions: extractConditions(status),
		},
	)
}
