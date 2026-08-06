package prometheus

import (
	"context"
	"fmt"
	"strconv"
	"regexp"
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

// MetricQueryParams defines the parameters for a Prometheus instant query.
type MetricQueryParams struct {
	Instance string `json:"instance" validate:"required" jsonschema:"(required) The Prometheus instance to query."`
	Query    string `json:"query" validate:"required,max=4096" jsonschema:"(required) The PromQL query to execute."`
	Filter   string `json:"filter,omitempty" jsonschema:"(optional) A Go RE2 regex applied on each result JSON. Keep only results that match. RE2 does NOT support lookahead (?=...)/(?!...), lookbehind (?<=...)/(?<!...), or backreferences — such patterns return an error. Example: 'node_cpu.*|node_memory.*'. Invalid regex returns an error."`
	Time     string `json:"time,omitempty" jsonschema:"(optional) The evaluation time in RFC3339 format. Defaults to now."`
	Limit    int    `json:"limit,omitempty" validate:"omitempty,min=1,max=50000" jsonschema:"(optional) Maximum number of result series to return. Default is no limit."`
}

// MetricQueryOutput is the structured output for a metric query.
type MetricQueryOutput struct {
	Metric model.Metric `json:"metric"`
	Value  any          `json:"value"`
}

// MetricQueryTool is an eino tool for Prometheus instant queries.
type MetricQueryTool struct {
	*baseTool
	tool.InvokableTool
}

// Invoke runs the instant query and returns results as JSON.
func (t *MetricQueryTool) Invoke(ctx context.Context, params *MetricQueryParams) (result string, err error) {
	if err := validateParams(params); err != nil {
		return "", err
	}

	// Reject subqueries with ranges greater than 7 days to prevent
	// excessive resource consumption on the Prometheus server.
	subqueryLongRange := regexp.MustCompile(`\[(\d+[smhwdy])\s*\]`)
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

// NewMetricQueryTool creates a new MetricQueryTool.
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
// parsePromQLDuration parses a single PromQL duration value (e.g. "1d", "2w",
// "24h") and returns a Go time.Duration. Supports standard Go suffixes plus
// PromQL-specific d (day) and w (week).
func parsePromQLDuration(s string) (time.Duration, error) {
	if s == "" {
		return 0, errors.New("empty duration")
	}
	// Standard Go duration suffixes: ns, us, ms, s, m, h.
	if d, err := time.ParseDuration(s); err == nil {
		return d, nil
	}
	// PromQL-specific: <number><unit> with no chaining.
	unit := s[len(s)-1]
	num, err := strconv.Atoi(s[:len(s)-1])
	if err != nil {
		return 0, errors.Wrapf(err, "invalid PromQL duration: %q", s)
	}
	switch unit {
	case 'd':
		return time.Duration(num) * 24 * time.Hour, nil
	case 'w':
		return time.Duration(num) * 7 * 24 * time.Hour, nil
	case 'y':
		return time.Duration(num) * 365 * 24 * time.Hour, nil
	default:
		return 0, errors.Errorf("unrecognized unit %q in PromQL duration: %q", string(unit), s)
	}
}
