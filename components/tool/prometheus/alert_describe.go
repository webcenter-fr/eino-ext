// Package prometheus provides eino tools for Prometheus monitoring and alerting.
package prometheus

import (
	"context"
	"fmt"
	"time"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/goccy/go-json"
	promapi "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/filter"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/marshal"
)

const alertDescribeDescription = `
** General Purpose **
It retrieves the full details of alerts matching a label-based regex filter from a Prometheus instance.

** Output **
It returns a JSON array of alert objects with all labels and annotations for matching alerts.
`

// AlertDescribeParams defines the parameters for describing a Prometheus alert.
type AlertDescribeParams struct {
	Instance string `json:"instance" validate:"required" jsonschema:"(required) The Prometheus instance to query."`
	Filter   string `json:"filter" validate:"required" jsonschema:"(required) A Go RE2 regex applied on alert label JSON. Only matching alerts are returned. RE2 does NOT support lookahead (?=...)/(?!...), lookbehind (?<=...)/(?<!...), or backreferences — such patterns return an error. Example: 'HighCPU|HighMemory'."`
	State    string `json:"state,omitempty" validate:"omitempty,oneof=firing pending inactive" jsonschema:"(optional) Filter by alert state: 'firing', 'pending', or 'inactive'."`
}

// alertDescribeOutput provides JSON-tagged fields for the promapi.Alert struct
// whose Labels and Annotations fields lack lowercase JSON tags upstream.
type alertDescribeOutput struct {
	ActiveAt    time.Time       `json:"activeAt"`
	Annotations model.LabelSet  `json:"annotations"`
	Labels      model.LabelSet  `json:"labels"`
	State       promapi.AlertState `json:"state"`
	Value       string          `json:"value"`
}

func toAlertDescribeOutput(a promapi.Alert) alertDescribeOutput {
	return alertDescribeOutput{
		ActiveAt:    a.ActiveAt,
		Annotations: a.Annotations,
		Labels:      a.Labels,
		State:       a.State,
		Value:       a.Value,
	}
}

// AlertDescribeTool is an eino tool for describing Prometheus alerts.
type AlertDescribeTool struct {
	*baseTool
	tool.InvokableTool
}

// Invoke returns alert details as JSON.
func (t *AlertDescribeTool) Invoke(ctx context.Context, params *AlertDescribeParams) (result string, err error) {
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

	outputs := make([]json.RawMessage, 0)
	for _, a := range alertsResult.Alerts {
		// Filter by state if specified
		if params.State != "" && string(a.State) != params.State {
			continue
		}

		// Convert labels to JSON for regex matching
		labelsJSON, err := json.Marshal(a.Labels)
		if err != nil {
			return "", errors.Wrap(err, "failed to marshal alert labels")
		}

		// Match regex against label JSON
		if !filter.Match(labelsJSON, re) {
			continue
		}

		// Return full alert data with properly-cased JSON keys
		outputJSON := json.RawMessage(marshal.MustMarshal(toAlertDescribeOutput(a)))
		outputs = append(outputs, outputJSON)
	}

	return marshalOutputs(outputs)
}

// NewAlertDescribeTool creates a new AlertDescribeTool.
func NewAlertDescribeTool(ctx context.Context, configs Configs) (*AlertDescribeTool, error) {
	base, err := newBaseTool(ctx, configs)
	if err != nil {
		return nil, err
	}

	describeTool := &AlertDescribeTool{baseTool: base}
	t, err := utils.InferTool("prometheus_alert_describe", fmt.Sprintf("%s\n%s", alertDescribeDescription, describeOutputGuidance), describeTool.Invoke)
	if err != nil {
		return nil, err
	}
	describeTool.InvokableTool = t

	return describeTool, nil
}
