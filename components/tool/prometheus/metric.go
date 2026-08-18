package prometheus

import (
	"context"
	"fmt"
	"regexp"
	"strings"
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

const metricDescription = `
** General Purpose **
It executes a PromQL query against a Prometheus instance. The required 'mode'
param selects the query type:

- instant: single-point evaluation at a point in time.
- range: evaluation over a time window (start/end/step).

** Output **
It returns a JSON array of objects.

For instant mode, each object represents a single time series with:
- metric: the metric labels (including "__name__").
- value: an array of [timestamp, "value"] representing the sample.

For range mode, each object represents a single time series with:
- metric: the metric labels (including "__name__").
- values: an array of [timestamp, "value"] arrays representing the time series
  data points.

** Mode-specific fields **
- instant mode: 'time' (evaluation time in RFC3339, defaults to now).
- both modes: 'limit' (maximum number of result series, 1-50000).
- range mode: 'start', 'end' (RFC3339), and 'step' (Go duration, >= 15s) are
  required; 'maxSamples' (1-10000, default 100) keeps the most recent N samples
  per series.

** Subquery limit **
PromQL subqueries with a range greater than 7 days are rejected (in both modes)
to prevent excessive resource consumption on the Prometheus server.
`

// subqueryLongRange matches PromQL subquery range selectors such as `[8d]`
// and the `range:step` form `[8d:1h]`. Capture group 1 is always the range
// part (the step, group 2, is irrelevant to the >7 day check). Compiled once
// at package init; reused across Invoke calls.
var subqueryLongRange = regexp.MustCompile(`\[(\d+[smhwdy])(?::(\d+[smhwdy]))?\s*\]`)

// MetricParams defines the parameters for a Prometheus metric query. The
// 'mode' field selects instant vs range; mode-specific fields are documented
// per field.
type MetricParams struct {
	Instance string `json:"instance" validate:"required" jsonschema:"(required) The Prometheus instance to query."`
	Mode     string `json:"mode" validate:"required,oneof=instant range" jsonschema:"(required) Query mode: 'instant' (single-point evaluation) or 'range' (time-window series)."`
	Query    string `json:"query" validate:"required,max=4096" jsonschema:"(required) The PromQL query to execute."`
	Filter   string `json:"filter,omitempty" jsonschema:"(optional) Go RE2 regex applied on each result JSON. Keep only results that match. RE2 does NOT support lookahead/lookbehind/backreferences — such patterns return an error."`
	// instant-mode fields
	Time  string `json:"time,omitempty" jsonschema:"(optional, instant mode) Evaluation time in RFC3339. Defaults to now. Ignored in range mode."`
	Limit int    `json:"limit,omitempty" validate:"omitempty,min=1,max=50000" jsonschema:"(optional) Max result series (1-50000). Applies to both instant and range modes."`
	// range-mode fields
	Start      string `json:"start,omitempty" jsonschema:"(optional, range mode) Start time in RFC3339. Required in range mode."`
	End        string `json:"end,omitempty" jsonschema:"(optional, range mode) End time in RFC3339. Required in range mode."`
	Step       string `json:"step,omitempty" jsonschema:"(optional, range mode) Resolution step as a Go duration (e.g. '15s', '1m'). Required in range mode; must be >= 15s."`
	MaxSamples int    `json:"maxSamples,omitempty" validate:"omitempty,min=1,max=10000" jsonschema:"(optional, range mode) Max samples per series (1-10000, default 100). Ignored in instant mode."`
}

// MetricInstantOutput is the structured output for an instant metric query.
type MetricInstantOutput struct {
	Metric model.Metric `json:"metric"`
	Value  any          `json:"value"` // [timestamp, "value"]
}

// MetricRangeOutput is the structured output for a range metric query.
type MetricRangeOutput struct {
	Metric model.Metric       `json:"metric"`
	Values []model.SamplePair `json:"values"`
}

// MetricTool is an eino tool for Prometheus metric queries (instant and range).
type MetricTool struct {
	*baseTool
	tool.InvokableTool
}

// Invoke runs the query (instant or range, per params.Mode) and returns
// results as JSON.
func (t *MetricTool) Invoke(ctx context.Context, params *MetricParams) (result string, err error) {
	if err := validateParams(params); err != nil {
		return "", err
	}

	// Reject subqueries with ranges greater than 7 days to prevent
	// excessive resource consumption on the Prometheus server.
	match := subqueryLongRange.FindAllStringSubmatch(params.Query, -1)
	for _, m := range match {
		if len(m) >= 2 {
			d, parseErr := parsePromQLDuration(m[1])
			if parseErr == nil && d > 7*24*time.Hour {
				return "", errors.Errorf("subquery range %q exceeds 7 day limit", m[1])
			}
		}
	}

	re, err := filter.Compile(params.Filter)
	if err != nil {
		return "", errors.Wrap(err, "error when compile regex")
	}

	c, err := t.client(params.Instance)
	if err != nil {
		return "", err
	}

	switch params.Mode {
	case "instant":
		var evalTime time.Time
		if params.Time != "" {
			evalTime, err = parseRFC3339(params.Time)
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
			output := MetricInstantOutput{
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

	case "range":
		if params.Start == "" || params.End == "" || params.Step == "" {
			missing := make([]string, 0, 3)
			if params.Start == "" {
				missing = append(missing, "start")
			}
			if params.End == "" {
				missing = append(missing, "end")
			}
			if params.Step == "" {
				missing = append(missing, "step")
			}
			return "", errors.Errorf("parameter(s) %s required in range mode (mode='range'); provide them as RFC3339 timestamps (start, end) and a Go duration (step, e.g. '15s') and retry", strings.Join(missing, ", "))
		}
		if params.MaxSamples == 0 {
			params.MaxSamples = 100
		}

		start, err := parseRFC3339(params.Start)
		if err != nil {
			return "", errors.Wrap(err, "parameter 'start' is not a valid RFC3339 timestamp (e.g. 2024-01-01T00:00:00Z); fix it and retry")
		}

		end, err := parseRFC3339(params.End)
		if err != nil {
			return "", errors.Wrap(err, "parameter 'end' is not a valid RFC3339 timestamp (e.g. 2024-01-01T00:00:00Z); fix it and retry")
		}

		step, err := time.ParseDuration(params.Step)
		if err != nil {
			return "", errors.Wrap(err, "parameter 'step' is not a valid Go duration string (e.g. '15s', '1m', '1h'); fix it and retry")
		}
		if step < 15*time.Second {
			return "", errors.New("parameter 'step' must be at least 15 seconds (e.g. '15s') to avoid excessive load; increase it and retry")
		}
		if end.Sub(start) > 7*24*time.Hour {
			return "", errors.New("the time window (end - start) must not exceed 7 days to avoid excessive load; narrow the range and retry")
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

			if params.Limit > 0 && len(outputs) >= params.Limit {
				break
			}
		}

		return marshalOutputs(outputs)

	default:
		return "", errors.Errorf("unsupported mode %q", params.Mode)
	}
}

// NewMetricTool creates a new MetricTool.
func NewMetricTool(ctx context.Context, configs Configs) (*MetricTool, error) {
	base, err := newBaseTool(ctx, configs)
	if err != nil {
		return nil, err
	}

	metricTool := &MetricTool{baseTool: base}
	t, err := utils.InferTool("prometheus_metric", fmt.Sprintf("%s\n%s", metricDescription, listOutputGuidance), metricTool.Invoke)
	if err != nil {
		return nil, err
	}
	metricTool.InvokableTool = t

	return metricTool, nil
}
