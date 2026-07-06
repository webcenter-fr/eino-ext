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

const metricRangeDescription = `
** General Purpose **
It executes a range PromQL query over a time window and returns a lightweight JSON array of results.

** Output **
It returns a JSON array of objects, where each object represents a single time series with the following fields:
- metric: the metric labels (including "__name__").
- values: an array of [timestamp, "value"] arrays representing the time series data points.

** How maxSamples works **
maxSamples limits the number of samples returned per time series, keeping the most recent N
samples (the tail of the time window). For example, with maxSamples=100, only the last 100
data points are returned per series.
`

type MetricRangeParams struct {
	Instance   string `json:"instance" validate:"required" jsonschema:"(required) The Prometheus instance to query."`
	Query      string `json:"query" validate:"required" jsonschema:"(required) The PromQL query to execute."`
	Start      string `json:"start" validate:"required" jsonschema:"(required) The start time in RFC3339 format (e.g. 2024-01-01T00:00:00Z)."`
	End        string `json:"end" validate:"required" jsonschema:"(required) The end time in RFC3339 format (e.g. 2024-01-01T01:00:00Z)."`
	Step       string `json:"step" validate:"required" jsonschema:"(required) The query resolution step width as a duration string (e.g. '15s', '1m', '1h')."`
	Filter     string `json:"filter,omitempty" jsonschema:"(optional) A Go RE2 regex applied on each result JSON. Keep only results that match. Invalid regex returns an error."`
	MaxSamples int    `json:"maxSamples,omitempty" validate:"omitempty,min=1,max=10000" jsonschema:"(optional) Maximum number of samples per time series. Defaults to 100."`
}

type MetricRangeOutput struct {
	Metric model.Metric       `json:"metric"`
	Values []model.SamplePair `json:"values"`
}

type MetricRangeTool struct {
	*baseTool
	tool.InvokableTool
}

func (t *MetricRangeTool) Invoke(ctx context.Context, params *MetricRangeParams) (result string, err error) {
	if params.MaxSamples == 0 {
		params.MaxSamples = 100
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

	start, err := time.Parse(time.RFC3339, params.Start)
	if err != nil {
		return "", errors.Wrap(err, "invalid start time format, must be RFC3339")
	}

	end, err := time.Parse(time.RFC3339, params.End)
	if err != nil {
		return "", errors.Wrap(err, "invalid end time format, must be RFC3339")
	}

	step, err := time.ParseDuration(params.Step)
	if err != nil {
		return "", errors.Wrap(err, "invalid step format, must be a Go duration string (e.g. '15s', '1m', '1h')")
	}

	value, _, err := c.QueryRange(ctx, params.Query, promapi.Range{
		Start: start,
		End:   end,
		Step:  step,
	})
	if err != nil {
		return "", errors.Wrap(err, "failed to execute range query")
	}

	matrix, ok := value.(model.Matrix)
	if !ok {
		return "", errors.Errorf("expected matrix result, got %T", value)
	}

	outputs := make([]json.RawMessage, 0, len(matrix))
	for _, ss := range matrix {
		values := ss.Values
		// Keep only the most recent maxSamples points (the tail of the time window).
		if len(values) > params.MaxSamples {
			values = values[len(values)-params.MaxSamples:]
		}

		output := MetricRangeOutput{
			Metric: ss.Metric,
			Values: values,
		}

		outputJSON := json.RawMessage(marshal.MustMarshal(output))
		if !filter.Match(outputJSON, re) {
			continue
		}
		outputs = append(outputs, outputJSON)
	}

	return marshalOutputs(outputs)
}

func NewMetricRangeTool(ctx context.Context, configs Configs) (*MetricRangeTool, error) {
	base, err := newBaseTool(configs)
	if err != nil {
		return nil, err
	}

	rangeTool := &MetricRangeTool{baseTool: base}
	t, err := utils.InferTool("prometheus_metric_range", fmt.Sprintf("%s\n%s", metricRangeDescription, listOutputGuidance), rangeTool.Invoke)
	if err != nil {
		return nil, err
	}
	rangeTool.InvokableTool = t

	return rangeTool, nil
}
