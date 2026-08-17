package alertmanager

import (
	"context"
	"fmt"
	"strings"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/goccy/go-json"
	"github.com/prometheus/alertmanager/api/v2/models"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/filter"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/marshal"
)

const alertDescription = `
** General Purpose **
It lists alerts from an Alertmanager instance.
Supports listing all alerts, fetching a single alert by its Alertmanager
fingerprint, and filtering by Alertmanager state or Alertmanager matchers.

** Output **
It returns a JSON array of objects, where each object represents an alert with the following fields:
- labels: the alert labels (e.g. alertname, severity, instance).
- annotations: the alert annotations.
- state: the Alertmanager alert state ('active', 'unprocessed', or 'suppressed').
- startsAt: when the alert started, in RFC3339 format.
- endsAt: when the alert is expected to end, in RFC3339 format.
- fingerprint: the Alertmanager alert fingerprint.
- silencedBy: the IDs of silencers currently silencing the alert.
- receivers: the receiver names the alert is routed to.

** Read-only **
This is a read-only tool: it never modifies alerts, so no confirmation is needed.
When 'fingerprint' is set, it takes precedence over 'alertFilter' and 'state',
which are ignored in that case.
`

// AlertParams defines the parameters for reading alerts from Alertmanager.
type AlertParams struct {
	Instance    string         `json:"instance" validate:"required" jsonschema:"(required) The Alertmanager instance to query."`
	Fingerprint string         `json:"fingerprint,omitempty" jsonschema:"(optional) If set, return only the alert with this fingerprint. Takes precedence over AlertFilter/State."`
	Filter      string         `json:"filter,omitempty" jsonschema:"(optional) Go RE2 regex applied on each alert JSON. Keep only alerts that match."`
	State       string         `json:"state,omitempty" validate:"omitempty,oneof=active unprocessed suppressed" jsonschema:"(optional) Filter by Alertmanager alert state: 'active', 'unprocessed', or 'suppressed'."`
	AlertFilter string         `json:"alertFilter,omitempty" jsonschema:"(optional) Alertmanager matcher string passed to the API, e.g. alertname=\"HighCPU\". Multiple matchers can be comma-separated."`
	Paginate    *AlertPaginate `json:"paginate,omitempty" jsonschema:"(optional) Pagination parameters."`
}

// AlertOutput is the structured output for an Alertmanager alert.
type AlertOutput struct {
	Labels      models.LabelSet `json:"labels"`
	Annotations models.LabelSet `json:"annotations"`
	State       string          `json:"state"`
	StartsAt    string          `json:"startsAt"`
	EndsAt      string          `json:"endsAt"`
	Fingerprint string          `json:"fingerprint"`
	SilencedBy  []string        `json:"silencedBy"`
	Receivers   []string        `json:"receivers"`
}

// AlertTool is the eino tool for reading Alertmanager alerts.
type AlertTool struct {
	*baseTool
	tool.InvokableTool
}

// Invoke returns matching Alertmanager alerts as JSON.
func (t *AlertTool) Invoke(ctx context.Context, params *AlertParams) (result string, err error) {
	if params.Paginate != nil && params.Paginate.PageSize == 0 {
		params.Paginate.PageSize = 20
	}
	if err := validateParams(params); err != nil {
		return "", err
	}

	re, err := filter.Compile(params.Filter)
	if err != nil {
		return "", errors.Wrap(err, "error when compile regex")
	}

	c, err := t.client(params.Instance)
	if err != nil {
		return "", err
	}

	p := &listAlertsParams{
		Active:    boolPtr(true),
		Silenced:  boolPtr(false),
		Inhibited: boolPtr(false),
	}
	if params.Fingerprint != "" {
		// Alertmanager v2 has no `fingerprint` query param, so fetch all states
		// and match client-side by fingerprint.
		p.Silenced = boolPtr(true)
		p.Inhibited = boolPtr(true)
	} else {
		if params.State == "suppressed" {
			// Suppressed alerts are either silenced or inhibited, so request
			// both categories before applying the client-side state filter.
			p.Silenced = boolPtr(true)
			p.Inhibited = boolPtr(true)
		}
		for _, m := range strings.Split(params.AlertFilter, ",") {
			if m = strings.TrimSpace(m); m != "" {
				p.Filter = append(p.Filter, m)
			}
		}
	}

	alerts, err := c.ListAlerts(ctx, p)
	if err != nil {
		return "", err
	}

	if params.Fingerprint != "" {
		filtered := make([]*models.GettableAlert, 0, 1)
		for _, a := range alerts {
			if a.Fingerprint != nil && *a.Fingerprint == params.Fingerprint {
				filtered = append(filtered, a)
			}
		}
		alerts = filtered
	} else if params.State != "" {
		filtered := make([]*models.GettableAlert, 0, len(alerts))
		for _, a := range alerts {
			if a.Status != nil && a.Status.State != nil && *a.Status.State == params.State {
				filtered = append(filtered, a)
			}
		}
		alerts = filtered
	}

	// Apply pagination
	startIdx, endIdx, err := paginateWindow(params.Paginate, len(alerts))
	if err != nil {
		return "", err
	}

	outputs := make([]json.RawMessage, 0, endIdx-startIdx)
	for _, a := range alerts[startIdx:endIdx] {
		output := AlertOutput{
			Labels:      a.Labels,
			Annotations: a.Annotations,
			State:       alertStatusState(a.Status),
			StartsAt:    ptrDateTimeFormat(a.StartsAt),
			EndsAt:      ptrDateTimeFormat(a.EndsAt),
			Fingerprint: ptrString(a.Fingerprint),
			SilencedBy:  alertStatusSilencedBy(a.Status),
			Receivers:   receiverNames(a.Receivers),
		}

		outputJSON := json.RawMessage(marshal.MustMarshal(output))
		if !filter.Match(outputJSON, re) {
			continue
		}
		outputs = append(outputs, outputJSON)
	}

	if token := nextPageToken(endIdx, len(alerts)); token != nil {
		outputs = append(outputs, token)
	}

	return marshalOutputs(outputs)
}

// NewAlertTool creates a new AlertTool.
func NewAlertTool(ctx context.Context, configs Configs) (*AlertTool, error) {
	base, err := newBaseTool(ctx, configs)
	if err != nil {
		return nil, err
	}

	listTool := &AlertTool{baseTool: base}
	t, err := utils.InferTool(alertToolName, fmt.Sprintf("%s\n%s", alertDescription, listOutputGuidance), listTool.Invoke)
	if err != nil {
		return nil, err
	}
	listTool.InvokableTool = t

	return listTool, nil
}
