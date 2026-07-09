package prometheus

import (
	"context"
	"fmt"
	"time"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/goccy/go-json"
	"github.com/prometheus/common/model"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/filter"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/marshal"
)

const metricQueryDescription = `
** General Purpose **
It executes an instant PromQL query against a Prometheus instance and returns a lightweight JSON array of results.

** Output **
It returns a JSON array of objects, where each object represents a single time series with the following fields:
- metric: the metric labels (including "__name__").
- value: an array of [timestamp, "value"] representing the sample.
`

type MetricQueryParams struct {
	Instance string `json:"instance" validate:"required" jsonschema:"(required) The Prometheus instance to query."`
	Query    string `json:"query" validate:"required,max=4096" jsonschema:"(required) The PromQL query to execute."`
	Filter   string `json:"filter,omitempty" jsonschema:"(optional) A Go RE2 regex applied on each result JSON. Keep only results that match. Example: 'node_cpu.*|node_memory.*'. Invalid regex returns an error."`
	Time     string `json:"time,omitempty" jsonschema:"(optional) The evaluation time in RFC3339 format. Defaults to now."`
	Limit    int    `json:"limit,omitempty" validate:"omitempty,min=1,max=50000" jsonschema:"(optional) Maximum number of result series to return. Default is no limit."`
}

type MetricQueryOutput struct {
	Metric model.Metric `json:"metric"`
	Value  any          `json:"value"`
}

type MetricQueryTool struct {
	*baseTool
	tool.InvokableTool
}

func (t *MetricQueryTool) Invoke(ctx context.Context, params *MetricQueryParams) (result string, err error) {
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

	var evalTime time.Time
	if params.Time != "" {
		evalTime, err = time.Parse(time.RFC3339, params.Time)
		if err != nil {
			return "", errors.Wrap(err, "invalid time format, must be RFC3339 (e.g. 2024-01-01T00:00:00Z)")
		}
	}

	value, _, err := c.Query(ctx, params.Query, evalTime)
	if err != nil {
		return "", errors.Wrap(err, "failed to execute instant query")
	}

	vec, ok := value.(model.Vector)
	if !ok {
		return "", errors.Errorf("expected vector result, got %T", value)
	}

	outputs := make([]json.RawMessage, 0, len(vec))
	for _, sample := range vec {
		output := MetricQueryOutput{
			Metric: sample.Metric,
			Value:  []any{sample.Timestamp.Time().UTC().Format(time.RFC3339), sample.Value.String()},
		}

		outputJSON := json.RawMessage(marshal.MustMarshal(output))
		if !filter.Match(outputJSON, re) {
			continue
		}
		outputs = append(outputs, outputJSON)

		if params.Limit > 0 && len(outputs) >= params.Limit {
			break
		}
	}

	return marshalOutputs(outputs)
}

func NewMetricQueryTool(ctx context.Context, configs Configs) (*MetricQueryTool, error) {
	base, err := newBaseTool(ctx, configs)
	if err != nil {
		return nil, err
	}

	queryTool := &MetricQueryTool{baseTool: base}
	t, err := utils.InferTool("prometheus_metric_query", fmt.Sprintf("%s\n%s", metricQueryDescription, listOutputGuidance), queryTool.Invoke)
	if err != nil {
		return nil, err
	}
	queryTool.InvokableTool = t

	return queryTool, nil
}
