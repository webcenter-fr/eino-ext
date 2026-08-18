package grafana

import (
	"context"
	"fmt"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

const queryToolName = "grafana_query"

// maxQueryExprLen caps the length of a query expression in the validate path.
// It must match the validate:"...,max=8192" tag on QueryParams.Expr.
const maxQueryExprLen = 8192

const queryDescription = `
** General Purpose **
Execute a single PromQL (Prometheus) or LogQL (Loki) query against a Grafana
datasource by UID, via Grafana's datasource proxy. Returns a cardinality-focused
summary: the result type, the number of series/streams, the label set of each
series (up to maxSeries), and a sample value or log line. Use this to check
whether a query returns data and whether it returns one series or many.

The datasource type (prometheus or loki) is resolved automatically from the
datasource UID; you do not need to specify it. Non-Prometheus/Loki datasources
return an error.

** Output **
A JSON object with: datasourceUid, datasourceType, expr, queryType, resultType,
seriesCount, truncated, series[], hints[]. An empty result (seriesCount=0) is a
normal result, NOT an error; use it to detect "no data". A large seriesCount
means the query is too broad — narrow it by adding label filters.
`

// QueryParams defines the parameters for grafana_query.
type QueryParams struct {
	Instance      string `json:"instance" validate:"required" jsonschema:"(required) The Grafana instance to connect to."`
	DataSourceUID string `json:"datasourceUID" validate:"required,max=256" jsonschema:"(required) The UID of the Prometheus or Loki datasource to query."`
	Expr          string `json:"expr" validate:"required,max=8192" jsonschema:"(required) The PromQL or LogQL expression to execute."`
	QueryType     string `json:"queryType,omitempty" validate:"omitempty,oneof=instant range" jsonschema:"(optional) 'instant' (default) or 'range'. Use 'instant' to check cardinality / no-data; use 'range' only when you need a time series."`
	// instant-mode: anchor time. range-mode: end time.
	Time        string `json:"time,omitempty" validate:"omitempty,max=64" jsonschema:"(optional) Query anchor time. 'now' (default), 'now-1h', RFC3339, or Unix seconds. For instant this is the evaluation time; for range this is the end."`
	Start       string `json:"start,omitempty" validate:"omitempty,max=64" jsonschema:"(optional, range mode) Start time. 'now-1h', RFC3339, or Unix seconds. Defaults to time-1h when queryType=range."`
	StepSeconds int    `json:"stepSeconds,omitempty" validate:"omitempty,min=1,max=86400" jsonschema:"(optional, range mode) Step size in seconds. Required for range queries (default 60)."`
	MaxSeries   int    `json:"maxSeries,omitempty" validate:"omitempty,min=1,max=1000" jsonschema:"(optional) Cap on the number of series returned in 'series' (default 20). seriesCount reflects the true total; 'series' is truncated to this many."`
}

// QueryResultOutput is the structured output of grafana_query.
type QueryResultOutput struct {
	DataSourceUID  string          `json:"datasourceUid"`
	DataSourceType string          `json:"datasourceType"` // "prometheus" | "loki"
	Expr           string          `json:"expr"`
	QueryType      string          `json:"queryType"`  // "instant" | "range"
	ResultType     string          `json:"resultType"` // "vector" | "matrix" | "streams" | "scalar" | "string"
	SeriesCount    int             `json:"seriesCount"`
	Truncated      bool            `json:"truncated"`
	Series         []SeriesSummary `json:"series,omitempty"`
	Hints          []string        `json:"hints,omitempty"`
}

// SeriesSummary is a single series/stream summary.
type SeriesSummary struct {
	Labels map[string]string `json:"labels"`
	// instant vector / scalar: the single value.
	Value *float64 `json:"value,omitempty"`
	// range matrix: the last value in the series.
	Sample *MetricSample `json:"sample,omitempty"`
	// streams (Loki log queries): one sample log line.
	Line string `json:"line,omitempty"`
}

// MetricSample is a single timestamped value (used for range matrix samples).
type MetricSample struct {
	Timestamp string  `json:"timestamp"`
	Value     float64 `json:"value"`
}

// QueryTool is an eino tool for executing PromQL/LogQL queries via Grafana.
type QueryTool struct {
	*baseTool
	tool.InvokableTool
}

// Invoke executes a single PromQL/LogQL query and returns a cardinality-focused
// summary.
func (t *QueryTool) Invoke(ctx context.Context, params *QueryParams) (string, error) {
	if params.QueryType == "" {
		params.QueryType = "instant"
	}
	if params.Time == "" {
		params.Time = "now"
	}
	if params.MaxSeries == 0 {
		params.MaxSeries = 20
	}
	if params.QueryType == "range" && params.StepSeconds == 0 {
		params.StepSeconds = 60
	}

	if err := validateParams(params); err != nil {
		return "", err
	}

	evalTime, err := parseQueryTime(params.Time, time.Now())
	if err != nil {
		return "", err
	}

	var start, end time.Time
	if params.QueryType == "range" {
		end = evalTime
		start, err = parseQueryTime(params.Start, evalTime.Add(-time.Hour))
		if err != nil {
			return "", err
		}
	}

	output, err := t.executeQuery(ctx, params.Instance, params.DataSourceUID, "", params.Expr, params.QueryType, evalTime, start, end, params.StepSeconds, params.MaxSeries)
	if err != nil {
		return "", err
	}

	return marshalJSON(output, "failed to marshal output")
}

// NewQueryTool creates a new QueryTool.
func NewQueryTool(ctx context.Context, configs Configs) (*QueryTool, error) {
	base, err := newBaseTool(ctx, configs)
	if err != nil {
		return nil, err
	}

	queryTool := &QueryTool{baseTool: base}
	t, err := utils.InferTool(queryToolName, fmt.Sprintf("%s\n%s", queryDescription, queryOutputGuidance), queryTool.Invoke)
	if err != nil {
		return nil, err
	}
	queryTool.InvokableTool = t

	return queryTool, nil
}
