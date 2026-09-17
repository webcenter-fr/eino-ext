package kubernetes

import (
	"sort"

	"github.com/goccy/go-json"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/marshal"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// registerMonitoringFormatters registers curated list formatters for
// the four prometheus-operator "alerting" CRDs from monitoring.coreos.com.
// These kinds have no typed Go API dependency in this package, so newObj is nil
// and the formatters operate directly on *unstructured.Unstructured.
func registerMonitoringFormatters(reg map[schema.GroupVersionKind]formatterEntry) {
	add := func(gvk schema.GroupVersionKind, list func(*unstructured.Unstructured) json.RawMessage) {
		reg[gvk] = formatterEntry{
			newObj: nil,
			format: unstructuredListFormatter(list),
		}
	}

	add(schema.GroupVersionKind{Group: "monitoring.coreos.com", Version: "v1", Kind: "Alertmanager"}, formatAlertmanagerList)
	add(schema.GroupVersionKind{Group: "monitoring.coreos.com", Version: "v1alpha1", Kind: "AlertmanagerConfig"}, formatAlertmanagerConfigList)
	add(schema.GroupVersionKind{Group: "monitoring.coreos.com", Version: "v1", Kind: "PrometheusRule"}, formatPrometheusRuleList)
	add(schema.GroupVersionKind{Group: "monitoring.coreos.com", Version: "v1alpha1", Kind: "Silence"}, formatSilenceList)
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

type matcherView struct {
	Name    string `json:"name"`
	Value   string `json:"value"`
	IsRegex bool   `json:"isRegex"`
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
