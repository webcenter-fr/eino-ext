package grafana

import (
	"context"
	"fmt"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/goccy/go-json"
)

const dashboardDescribeDescription = `
** General Purpose **
It gets the full details of a specific Grafana dashboard by its UID.

** Output **
It returns a JSON object with the dashboard model and metadata.
`

// DashboardDescribeParams defines the parameters for describing a Grafana dashboard.
type DashboardDescribeParams struct {
	Instance            string   `json:"instance" validate:"required" jsonschema:"(required) The Grafana instance to connect to."`
	UID                 string   `json:"uid" validate:"required" jsonschema:"(required) The dashboard UID."`
	ExcludeFieldsOutput []string `json:"excludeFieldsOutput,omitempty" validate:"omitempty,dive,oneof=meta panels templating time annotations schemaVersion version" jsonschema:"(optional) Fields to exclude from the dashboard output: 'meta', 'panels', 'templating', 'time', 'annotations', 'schemaVersion', 'version'."`
}

// DashboardDescribeOutput is the structured output for a dashboard describe.
type DashboardDescribeOutput struct {
	Dashboard map[string]any `json:"dashboard,omitempty"`
	Meta      *dashboardMeta `json:"meta,omitempty"`
}

// DashboardDescribeTool is an eino tool for describing Grafana dashboards.
type DashboardDescribeTool struct {
	*baseTool
	tool.InvokableTool
}

// Invoke returns the full details of a Grafana dashboard.
func (t *DashboardDescribeTool) Invoke(ctx context.Context, params *DashboardDescribeParams) (result string, err error) {
	if err := validateParams(params); err != nil {
		return "", err
	}

	c, err := t.client(params.Instance)
	if err != nil {
		return "", err
	}

	body, err := c.GetDashboard(ctx, params.UID)
	if err != nil {
		return "", errors.Wrap(err, "failed to get dashboard")
	}

	var dr dashboardResponse
	if err := json.Unmarshal(body, &dr); err != nil {
		return "", errors.Wrap(err, "failed to unmarshal dashboard response")
	}

	output := &DashboardDescribeOutput{
		Dashboard: dr.Dashboard,
		Meta:      &dr.Meta,
	}

	if err := applyExcludes(params.ExcludeFieldsOutput, map[string]func(){
		"meta":          func() { output.Meta = nil },
		"panels":        func() { delete(output.Dashboard, "panels") },
		"templating":    func() { delete(output.Dashboard, "templating") },
		"time":          func() { delete(output.Dashboard, "time") },
		"annotations":   func() { delete(output.Dashboard, "annotations") },
		"schemaVersion": func() { delete(output.Dashboard, "schemaVersion") },
		"version":       func() { delete(output.Dashboard, "version") },
	}); err != nil {
		return "", err
	}

	data, err := json.Marshal(output)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal output")
	}

	return string(data), nil
}

// NewDashboardDescribeTool creates a new DashboardDescribeTool.
func NewDashboardDescribeTool(ctx context.Context, configs Configs) (*DashboardDescribeTool, error) {
	base, err := newBaseTool(ctx, configs)
	if err != nil {
		return nil, err
	}

	describeTool := &DashboardDescribeTool{baseTool: base}
	t, err := utils.InferTool("grafana_dashboard_describe", fmt.Sprintf("%s\n%s", dashboardDescribeDescription, dashboardDescribeOutputGuidance), describeTool.Invoke)
	if err != nil {
		return nil, err
	}
	describeTool.InvokableTool = t

	return describeTool, nil
}
