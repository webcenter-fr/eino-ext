package prometheus

import (
	"context"
	"fmt"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/goccy/go-json"
	promapi "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/filter"
)

const alertListDescription = `
** General Purpose **
It lists all current alerts from a Prometheus instance with lightweight output.

** Output **
It returns a JSON array of objects, where each object represents an alert with the following fields:
- labels: the alert labels (e.g. alertname, severity, instance).
- annotations: summary and description annotations.
- state: the alert state (firing, pending, or inactive).
- activeAt: when the alert started firing.
- value: the alert evaluation value.
`

type AlertListParams struct {
	Instance string             `json:"instance" validate:"required" jsonschema:"(required) The Prometheus instance to query."`
	Filter   string             `json:"filter,omitempty" jsonschema:"(optional) A Go RE2 regex applied on each alert JSON. Keep only alerts that match. Invalid regex returns an error."`
	State    string             `json:"state,omitempty" validate:"omitempty,oneof=firing pending inactive" jsonschema:"(optional) Filter by alert state: 'firing', 'pending', or 'inactive'."`
	Paginate *AlertListPaginate `json:"paginate,omitempty" jsonschema:"(optional) Pagination parameters."`
}

type AlertListPaginate struct {
	PageSize      int    `json:"pageSize,omitempty" validate:"omitempty,min=1,max=500" jsonschema:"(optional) The number of alerts to return per page. Default is 20."`
	PaginateToken string `json:"paginateToken,omitempty" jsonschema:"(optional) The token to retrieve the next page of results. This token is returned when there are more results available than can fit in a single page."`
}

type AlertListOutput struct {
	Labels      model.LabelSet `json:"labels"`
	Annotations model.LabelSet `json:"annotations"`
	State       string         `json:"state"`
	ActiveAt    string         `json:"activeAt"`
	Value       string         `json:"value"`
}

type AlertListTool struct {
	*baseTool
	tool.InvokableTool
}

type alertPaginateToken struct {
	PaginateToken int `json:"paginateToken"`
}

func (t *AlertListTool) Invoke(ctx context.Context, params *AlertListParams) (result string, err error) {
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

	alertsResult, err := c.Alerts(ctx)
	if err != nil {
		return "", errors.Wrap(err, "failed to list alerts")
	}

	// Pre-filter by state if specified
	alerts := alertsResult.Alerts
	if params.State != "" {
		filtered := make([]promapi.Alert, 0, len(alerts))
		for _, a := range alerts {
			if string(a.State) == params.State {
				filtered = append(filtered, a)
			}
		}
		alerts = filtered
	}

	// Apply pagination
	startIdx := 0
	if params.Paginate != nil && params.Paginate.PaginateToken != "" {
		var tok alertPaginateToken
		if err := json.Unmarshal([]byte(params.Paginate.PaginateToken), &tok); err != nil {
			return "", errors.Wrap(err, "invalid paginate token")
		}
		startIdx = tok.PaginateToken
	}

	endIdx := len(alerts)
	if params.Paginate != nil {
		if startIdx+params.Paginate.PageSize < endIdx {
			endIdx = startIdx + params.Paginate.PageSize
		}
	}

	outputs := make([]json.RawMessage, 0, endIdx-startIdx)
	for _, a := range alerts[startIdx:endIdx] {
		// Build lightweight annotations (summary + description only)
		annotations := model.LabelSet{}
		if summary, ok := a.Annotations["summary"]; ok {
			annotations["summary"] = summary
		}
		if description, ok := a.Annotations["description"]; ok {
			annotations["description"] = description
		}

		output := AlertListOutput{
			Labels:      a.Labels,
			Annotations: annotations,
			State:       string(a.State),
			ActiveAt:    a.ActiveAt.Format("2006-01-02T15:04:05Z"),
			Value:       a.Value,
		}

		outputJSON := json.RawMessage(MustMarshal(output))
		if !filter.Match(outputJSON, re) {
			continue
		}
		outputs = append(outputs, outputJSON)
	}

	if params.Paginate != nil && endIdx < len(alerts) {
		tokenData := MustMarshal(alertPaginateToken{PaginateToken: endIdx})
		outputs = append(outputs, json.RawMessage(tokenData))
	}

	data, err := json.Marshal(outputs)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal output")
	}

	return string(data), nil
}

func NewAlertListTool(ctx context.Context, configs Configs) (*AlertListTool, error) {
	base, err := newBaseTool(configs)
	if err != nil {
		return nil, err
	}

	listTool := &AlertListTool{baseTool: base}
	t, err := utils.InferTool("prometheus_alert_list", fmt.Sprintf("%s\n%s", alertListDescription, listOutputGuidance), listTool.Invoke)
	if err != nil {
		return nil, err
	}
	listTool.InvokableTool = t

	return listTool, nil
}
