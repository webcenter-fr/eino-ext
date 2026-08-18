package prometheus

import (
	"context"
	"testing"
	"time"

	"github.com/goccy/go-json"
	"github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockMetricAPI embeds v1.API and implements only Query/QueryRange.
type mockMetricAPI struct {
	v1.API
	queryValue model.Value
	queryErr   error
	rangeValue model.Value
	rangeErr   error
}

func (m *mockMetricAPI) Query(ctx context.Context, query string, ts time.Time, opts ...v1.Option) (model.Value, v1.Warnings, error) {
	return m.queryValue, nil, m.queryErr
}

func (m *mockMetricAPI) QueryRange(ctx context.Context, query string, r v1.Range, opts ...v1.Option) (model.Value, v1.Warnings, error) {
	return m.rangeValue, nil, m.rangeErr
}

func newMetricToolWithMock(mock v1.API) *MetricTool {
	return &MetricTool{
		baseTool: &baseTool{
			clients:        map[string]v1.API{"prod": mock},
			knownInstances: []string{"prod"},
		},
	}
}

func instantVector() model.Vector {
	return model.Vector{
		&model.Sample{
			Metric:    model.Metric{"__name__": "up", "job": "node"},
			Value:     model.SampleValue(1.5),
			Timestamp: model.TimeFromUnix(1700000000),
		},
		&model.Sample{
			Metric:    model.Metric{"__name__": "up", "job": "kubelet"},
			Value:     model.SampleValue(0),
			Timestamp: model.TimeFromUnix(1700000000),
		},
	}
}

func rangeMatrix() model.Matrix {
	ts := model.TimeFromUnix(1700000000)
	return model.Matrix{
		&model.SampleStream{
			Metric: model.Metric{"__name__": "up", "job": "node"},
			Values: []model.SamplePair{
				{Timestamp: ts, Value: model.SampleValue(1)},
				{Timestamp: ts.Add(15000), Value: model.SampleValue(2)},
				{Timestamp: ts.Add(30000), Value: model.SampleValue(3)},
			},
		},
	}
}

func rangeMatrixMulti() model.Matrix {
	ts := model.TimeFromUnix(1700000000)
	return model.Matrix{
		&model.SampleStream{
			Metric: model.Metric{"__name__": "up", "job": "node"},
			Values: []model.SamplePair{{Timestamp: ts, Value: model.SampleValue(1)}},
		},
		&model.SampleStream{
			Metric: model.Metric{"__name__": "up", "job": "kubelet"},
			Values: []model.SamplePair{{Timestamp: ts, Value: model.SampleValue(2)}},
		},
		&model.SampleStream{
			Metric: model.Metric{"__name__": "up", "job": "api"},
			Values: []model.SamplePair{{Timestamp: ts, Value: model.SampleValue(3)}},
		},
	}
}

func TestMetricToolInstant(t *testing.T) {
	t.Run("happy path returns instant outputs", func(t *testing.T) {
		tool := newMetricToolWithMock(&mockMetricAPI{queryValue: instantVector()})
		result, err := tool.Invoke(context.Background(), &MetricParams{
			Instance: "prod",
			Mode:     "instant",
			Query:    "up",
		})
		require.NoError(t, err)

		var outputs []MetricInstantOutput
		require.NoError(t, json.Unmarshal([]byte(result), &outputs))
		require.Len(t, outputs, 2)
		assert.Equal(t, model.Metric{"__name__": "up", "job": "node"}, outputs[0].Metric)
	})

	t.Run("filter and limit", func(t *testing.T) {
		tool := newMetricToolWithMock(&mockMetricAPI{queryValue: instantVector()})
		result, err := tool.Invoke(context.Background(), &MetricParams{
			Instance: "prod",
			Mode:     "instant",
			Query:    "up",
			Filter:   "node",
			Limit:    1,
		})
		require.NoError(t, err)

		var outputs []MetricInstantOutput
		require.NoError(t, json.Unmarshal([]byte(result), &outputs))
		require.Len(t, outputs, 1)
		assert.Equal(t, model.LabelValue("node"), outputs[0].Metric["job"])
	})

	t.Run("invalid time returns error", func(t *testing.T) {
		tool := newMetricToolWithMock(&mockMetricAPI{queryValue: instantVector()})
		_, err := tool.Invoke(context.Background(), &MetricParams{
			Instance: "prod",
			Mode:     "instant",
			Query:    "up",
			Time:     "not-a-time",
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid time format")
	})
}

func TestMetricToolRange(t *testing.T) {
	t.Run("happy path returns range outputs", func(t *testing.T) {
		tool := newMetricToolWithMock(&mockMetricAPI{rangeValue: rangeMatrix()})
		result, err := tool.Invoke(context.Background(), &MetricParams{
			Instance: "prod",
			Mode:     "range",
			Query:    "up",
			Start:    "2023-11-14T00:00:00Z",
			End:      "2023-11-14T01:00:00Z",
			Step:     "15s",
		})
		require.NoError(t, err)

		var outputs []MetricRangeOutput
		require.NoError(t, json.Unmarshal([]byte(result), &outputs))
		require.Len(t, outputs, 1)
		assert.Len(t, outputs[0].Values, 3)
	})

	t.Run("maxSamples tail truncation", func(t *testing.T) {
		tool := newMetricToolWithMock(&mockMetricAPI{rangeValue: rangeMatrix()})
		result, err := tool.Invoke(context.Background(), &MetricParams{
			Instance:   "prod",
			Mode:       "range",
			Query:      "up",
			Start:      "2023-11-14T00:00:00Z",
			End:        "2023-11-14T01:00:00Z",
			Step:       "15s",
			MaxSamples: 2,
		})
		require.NoError(t, err)

		var outputs []MetricRangeOutput
		require.NoError(t, json.Unmarshal([]byte(result), &outputs))
		require.Len(t, outputs, 1)
		require.Len(t, outputs[0].Values, 2)
		assert.Equal(t, model.SampleValue(2), outputs[0].Values[0].Value)
		assert.Equal(t, model.SampleValue(3), outputs[0].Values[1].Value)
	})

	t.Run("range limit caps number of series", func(t *testing.T) {
		tool := newMetricToolWithMock(&mockMetricAPI{rangeValue: rangeMatrixMulti()})
		result, err := tool.Invoke(context.Background(), &MetricParams{
			Instance: "prod",
			Mode:     "range",
			Query:    "up",
			Start:    "2023-11-14T00:00:00Z",
			End:      "2023-11-14T01:00:00Z",
			Step:     "15s",
			Limit:    2,
		})
		require.NoError(t, err)

		var outputs []MetricRangeOutput
		require.NoError(t, json.Unmarshal([]byte(result), &outputs))
		require.Len(t, outputs, 2)
	})

	t.Run("step below 15s returns error", func(t *testing.T) {
		tool := newMetricToolWithMock(&mockMetricAPI{})
		_, err := tool.Invoke(context.Background(), &MetricParams{
			Instance: "prod",
			Mode:     "range",
			Query:    "up",
			Start:    "2023-11-14T00:00:00Z",
			End:      "2023-11-14T01:00:00Z",
			Step:     "5s",
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "step' must be at least 15 seconds")
	})

	t.Run("window over 7 days returns error", func(t *testing.T) {
		tool := newMetricToolWithMock(&mockMetricAPI{})
		_, err := tool.Invoke(context.Background(), &MetricParams{
			Instance: "prod",
			Mode:     "range",
			Query:    "up",
			Start:    "2023-11-01T00:00:00Z",
			End:      "2023-11-14T00:00:00Z",
			Step:     "1m",
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "must not exceed 7 days")
	})

	t.Run("missing start returns code-level error", func(t *testing.T) {
		tool := newMetricToolWithMock(&mockMetricAPI{})
		_, err := tool.Invoke(context.Background(), &MetricParams{
			Instance: "prod",
			Mode:     "range",
			Query:    "up",
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "required in range mode")
	})
}

func TestMetricToolSubqueryLimit(t *testing.T) {
	for _, tc := range []struct {
		query   string
		allowed bool
	}{
		{query: "rate(http_requests_total[8d])", allowed: false},
		{query: "rate(http_requests_total[8d:1h])", allowed: false},
		{query: "rate(http_requests_total[1d:1m])", allowed: true},
	} {
		for _, mode := range []string{"instant", "range"} {
			t.Run(mode+"/"+tc.query, func(t *testing.T) {
				tool := newMetricToolWithMock(&mockMetricAPI{
					queryValue: instantVector(),
					rangeValue: rangeMatrix(),
				})
				params := &MetricParams{
					Instance: "prod",
					Mode:     mode,
					Query:    tc.query,
				}
				if mode == "range" {
					params.Start = "2023-11-14T00:00:00Z"
					params.End = "2023-11-14T01:00:00Z"
					params.Step = "15s"
				}
				_, err := tool.Invoke(context.Background(), params)
				if tc.allowed {
					assert.NoError(t, err)
				} else {
					assert.Error(t, err)
					assert.Contains(t, err.Error(), "exceeds 7 day limit")
				}
			})
		}
	}
}

func TestMetricToolFractionalSeconds(t *testing.T) {
	t.Run("instant accepts fractional seconds", func(t *testing.T) {
		tool := newMetricToolWithMock(&mockMetricAPI{queryValue: instantVector()})
		_, err := tool.Invoke(context.Background(), &MetricParams{
			Instance: "prod",
			Mode:     "instant",
			Query:    "up",
			Time:     "2024-01-01T00:00:00.123Z",
		})
		assert.NoError(t, err)
	})

	t.Run("range accepts fractional seconds", func(t *testing.T) {
		tool := newMetricToolWithMock(&mockMetricAPI{rangeValue: rangeMatrix()})
		_, err := tool.Invoke(context.Background(), &MetricParams{
			Instance: "prod",
			Mode:     "range",
			Query:    "up",
			Start:    "2024-01-01T00:00:00.123Z",
			End:      "2024-01-01T01:00:00.123Z",
			Step:     "15s",
		})
		assert.NoError(t, err)
	})
}

func TestMetricToolValidation(t *testing.T) {
	t.Run("invalid mode", func(t *testing.T) {
		tool := newMetricToolWithMock(&mockMetricAPI{})
		_, err := tool.Invoke(context.Background(), &MetricParams{
			Instance: "prod",
			Mode:     "bogus",
			Query:    "up",
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid parameters")
	})

	t.Run("missing query", func(t *testing.T) {
		tool := newMetricToolWithMock(&mockMetricAPI{})
		_, err := tool.Invoke(context.Background(), &MetricParams{
			Instance: "prod",
			Mode:     "instant",
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid parameters")
	})

	t.Run("unknown instance", func(t *testing.T) {
		tool := newMetricToolWithMock(&mockMetricAPI{})
		_, err := tool.Invoke(context.Background(), &MetricParams{
			Instance: "nope",
			Mode:     "instant",
			Query:    "up",
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "nope")
	})

	t.Run("invalid filter regex", func(t *testing.T) {
		tool := newMetricToolWithMock(&mockMetricAPI{queryValue: instantVector()})
		_, err := tool.Invoke(context.Background(), &MetricParams{
			Instance: "prod",
			Mode:     "instant",
			Query:    "up",
			Filter:   "(?=...)",
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "compile regex")
	})
}

func TestMetricToolConstructor(t *testing.T) {
	tool, err := NewMetricTool(context.Background(), Configs{})
	require.NoError(t, err)
	require.NotNil(t, tool)

	info, err := tool.Info(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "prometheus_metric", info.Name)
}
