package grafana

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/goccy/go-json"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewQueryToolRequiresConfigs(t *testing.T) {
	_, err := NewQueryTool(context.Background(), Configs{})
	assert.Error(t, err)
}

func TestQueryToolConstructor(t *testing.T) {
	tool, err := NewQueryTool(context.Background(), Configs{"t": {URL: "http://localhost"}})
	require.NoError(t, err)
	require.NotNil(t, tool)

	info, err := tool.Info(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "grafana_query", info.Name)
}

func (t *ToolTestSuite) TestQuery() {
	ctx := context.Background()
	tool, err := NewQueryTool(ctx, t.configs)
	assert.NoError(t.T(), err)

	_, err = tool.Info(ctx)
	assert.NoError(t.T(), err)

	tests := []struct {
		name    string
		params  *QueryParams
		wantErr bool
		errText string
		check   func(t *testing.T, out QueryResultOutput)
	}{
		{
			name:   "instant prometheus vector",
			params: &QueryParams{Instance: "test", DataSourceUID: "ds-prom", Expr: "up"},
			check: func(t *testing.T, out QueryResultOutput) {
				assert.Equal(t, "prometheus", out.DataSourceType)
				assert.Equal(t, "vector", out.ResultType)
				assert.Equal(t, 3, out.SeriesCount)
				assert.False(t, out.Truncated)
				assert.Len(t, out.Series, 3)
			},
		},
		{
			name:   "maxSeries truncation",
			params: &QueryParams{Instance: "test", DataSourceUID: "ds-prom", Expr: "up", MaxSeries: 2},
			check: func(t *testing.T, out QueryResultOutput) {
				assert.Equal(t, 3, out.SeriesCount)
				assert.True(t, out.Truncated)
				assert.Len(t, out.Series, 2)
			},
		},
		{
			name:   "empty prometheus vector",
			params: &QueryParams{Instance: "test", DataSourceUID: "ds-prom", Expr: "empty"},
			check: func(t *testing.T, out QueryResultOutput) {
				assert.Equal(t, 0, out.SeriesCount)
				assert.NotEmpty(t, out.Hints)
			},
		},
		{
			name:   "range prometheus matrix",
			params: &QueryParams{Instance: "test", DataSourceUID: "ds-prom", Expr: "up", QueryType: "range", Time: "now", Start: "now-1h", StepSeconds: 60},
			check: func(t *testing.T, out QueryResultOutput) {
				assert.Equal(t, "matrix", out.ResultType)
				assert.Equal(t, 1, out.SeriesCount)
				require.Len(t, out.Series, 1)
				require.NotNil(t, out.Series[0].Sample)
				assert.Equal(t, float64(2), out.Series[0].Sample.Value)
			},
		},
		{
			name:   "loki instant metric query",
			params: &QueryParams{Instance: "test", DataSourceUID: "ds-loki", Expr: `{app="checkout"}`},
			check: func(t *testing.T, out QueryResultOutput) {
				assert.Equal(t, "loki", out.DataSourceType)
				assert.Equal(t, "vector", out.ResultType)
				assert.Equal(t, 1, out.SeriesCount)
			},
		},
		{
			name:   "loki range streams",
			params: &QueryParams{Instance: "test", DataSourceUID: "ds-loki", Expr: `{app="checkout"}`, QueryType: "range", Time: "now", Start: "now-1h", StepSeconds: 60},
			check: func(t *testing.T, out QueryResultOutput) {
				assert.Equal(t, "streams", out.ResultType)
				assert.Equal(t, 1, out.SeriesCount)
				require.Len(t, out.Series, 1)
				assert.Equal(t, "first log line", out.Series[0].Line)
			},
		},
		{
			name:    "unsupported datasource type",
			params:  &QueryParams{Instance: "test", DataSourceUID: "ds-mysql", Expr: "SELECT 1"},
			wantErr: true,
			errText: "unsupported type",
		},
		{
			name:    "unknown datasource uid",
			params:  &QueryParams{Instance: "test", DataSourceUID: "nonexistent", Expr: "up"},
			wantErr: true,
			errText: "not found",
		},
		{
			name:    "bad promql",
			params:  &QueryParams{Instance: "test", DataSourceUID: "ds-prom", Expr: "bad"},
			wantErr: true,
			errText: "bad_data",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func() {
			result, err := tool.Invoke(ctx, tt.params)
			if tt.wantErr {
				assert.Error(t.T(), err)
				if tt.errText != "" {
					assert.Contains(t.T(), err.Error(), tt.errText)
				}
				return
			}
			assert.NoError(t.T(), err)

			var out QueryResultOutput
			assert.NoError(t.T(), json.Unmarshal([]byte(result), &out))
			if tt.check != nil {
				tt.check(t.T(), out)
			}
		})
	}
}

func TestQueryParamsValidation(t *testing.T) {
	tool, err := NewQueryTool(context.Background(), Configs{"test": {URL: "http://localhost"}})
	require.NoError(t, err)

	tests := []struct {
		name   string
		params *QueryParams
	}{
		{"missing instance", &QueryParams{DataSourceUID: "ds", Expr: "up"}},
		{"missing datasourceUID", &QueryParams{Instance: "test", Expr: "up"}},
		{"missing expr", &QueryParams{Instance: "test", DataSourceUID: "ds"}},
		{"bad queryType", &QueryParams{Instance: "test", DataSourceUID: "ds", Expr: "up", QueryType: "bogus"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tool.Invoke(context.Background(), tt.params)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "invalid parameters")
		})
	}
}

// TestQueryPathEscape verifies that a datasource UID containing path-traversal
// characters is URL-escaped on the wire so it cannot reach a different API
// endpoint.
func TestQueryPathEscape(t *testing.T) {
	var capturedURI string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedURI = r.RequestURI
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
	}))
	defer server.Close()

	c := &grafanaClient{
		baseURL:    server.URL,
		httpClient: &http.Client{},
		timeout:    testTimeout,
	}

	_, err := c.QueryPrometheus(context.Background(), "foo/bar", "up", "instant", time.Now(), time.Time{}, time.Time{}, 0)
	assert.NoError(t, err)
	assert.Contains(t, capturedURI, "/api/datasources/proxy/uid/foo%2Fbar/api/v1/query",
		"uid must be path-escaped on the wire to prevent endpoint traversal")
	assert.NotContains(t, capturedURI, "/api/datasources/proxy/uid/foo/bar")
}

func TestCollectPanels(t *testing.T) {
	t.Run("v1 top-level panels", func(t *testing.T) {
		model := map[string]any{
			"panels": []any{
				map[string]any{"id": float64(1), "type": "timeseries"},
			},
		}
		panels := collectPanels(model)
		require.Len(t, panels, 1)
		assert.Equal(t, 1, panelID(panels[0]))
	})

	t.Run("row panel flattens nested panels", func(t *testing.T) {
		model := map[string]any{
			"panels": []any{
				map[string]any{
					"id": float64(1), "type": "row",
					"panels": []any{
						map[string]any{"id": float64(2), "type": "timeseries"},
					},
				},
			},
		}
		panels := collectPanels(model)
		require.Len(t, panels, 1)
		assert.Equal(t, 2, panelID(panels[0]))
	})

	t.Run("row panel without panels key does not panic", func(t *testing.T) {
		model := map[string]any{
			"panels": []any{
				map[string]any{"id": float64(1), "type": "row"},
			},
		}
		assert.NotPanics(t, func() { _ = collectPanels(model) })
	})

	t.Run("row panel with null panels does not panic", func(t *testing.T) {
		model := map[string]any{
			"panels": []any{
				map[string]any{"id": float64(1), "type": "row", "panels": nil},
			},
		}
		assert.NotPanics(t, func() { _ = collectPanels(model) })
	})

	t.Run("legacy rows panels", func(t *testing.T) {
		model := map[string]any{
			"rows": []any{
				map[string]any{
					"panels": []any{
						map[string]any{"id": float64(2), "type": "timeseries"},
					},
				},
			},
		}
		panels := collectPanels(model)
		require.Len(t, panels, 1)
		assert.Equal(t, 2, panelID(panels[0]))
	})

	t.Run("legacy row without panels does not panic", func(t *testing.T) {
		model := map[string]any{
			"rows": []any{
				map[string]any{"id": float64(1)},
			},
		}
		assert.NotPanics(t, func() { _ = collectPanels(model) })
	})

	t.Run("v2 elements sorted", func(t *testing.T) {
		model := map[string]any{
			"elements": map[string]any{
				"panel-c": map[string]any{"kind": "Panel", "spec": map[string]any{"id": float64(3), "type": "timeseries"}},
				"panel-a": map[string]any{"kind": "Panel", "spec": map[string]any{"id": float64(1), "type": "timeseries"}},
				"panel-b": map[string]any{"kind": "Panel", "spec": map[string]any{"id": float64(2), "type": "timeseries"}},
			},
		}
		panels := collectPanels(model)
		require.Len(t, panels, 3)
		ids := make([]int, len(panels))
		for i, p := range panels {
			ids[i] = panelID(p)
		}
		assert.Equal(t, []int{1, 2, 3}, ids)
	})
}

func TestAllSeriesNaN(t *testing.T) {
	nan := math.NaN()
	one := 1.0

	tests := []struct {
		name   string
		series []SeriesSummary
		want   bool
	}{
		{"empty", nil, false},
		{"all NaN vector", []SeriesSummary{{Value: &nan}}, true},
		{"vector with non-NaN", []SeriesSummary{{Value: &nan}, {Value: &one}}, false},
		{"NaN plus empty series ignored", []SeriesSummary{{Value: &nan}, {}}, true},
		{"only empty series", []SeriesSummary{{}, {}}, false},
		{"all NaN matrix", []SeriesSummary{{Sample: &MetricSample{Value: nan}}, {Sample: &MetricSample{Value: nan}}}, true},
		{"matrix NaN plus empty", []SeriesSummary{{Sample: &MetricSample{Value: nan}}, {}}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, allSeriesNaN(tt.series))
		})
	}
}

// TestQueryInstantTimeEncoding verifies the time-unit contract for instant
// queries: Prometheus uses Unix seconds, Loki uses a nanosecond Unix epoch.
func TestQueryInstantTimeEncoding(t *testing.T) {
	capture := func(uid, pathPrefix string) string {
		var capturedURI string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			capturedURI = r.RequestURI
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
		}))
		defer server.Close()

		c := &grafanaClient{baseURL: server.URL, httpClient: &http.Client{}, timeout: testTimeout}
		now := time.Unix(1700000000, 123000000)
		if pathPrefix == "/loki/api/v1" {
			_, _ = c.QueryLoki(context.Background(), uid, `{app="x"}`, "instant", now, time.Time{}, time.Time{}, 0, 0, "")
		} else {
			_, _ = c.QueryPrometheus(context.Background(), uid, "up", "instant", now, time.Time{}, time.Time{}, 0)
		}
		return capturedURI
	}

	prom := capture("ds-prom", "/api/v1")
	assert.Contains(t, prom, "time=1700000000", "prometheus instant time must be Unix seconds")

	loki := capture("ds-loki", "/loki/api/v1")
	assert.Contains(t, loki, "time=1700000000123000000", "loki instant time must be Unix nanoseconds")
}

func TestParseQueryTime(t *testing.T) {
	t.Run("empty returns default", func(t *testing.T) {
		def := time.Now().Add(-time.Hour)
		got, err := parseQueryTime("", def)
		assert.NoError(t, err)
		assert.Equal(t, def, got)
	})

	t.Run("now", func(t *testing.T) {
		got, err := parseQueryTime("now", time.Time{})
		assert.NoError(t, err)
		assert.WithinDuration(t, time.Now(), got, 2*time.Second)
	})

	t.Run("now-1h resolves to about 1h ago", func(t *testing.T) {
		got, err := parseQueryTime("now-1h", time.Time{})
		assert.NoError(t, err)
		assert.WithinDuration(t, time.Now().Add(-time.Hour), got, 2*time.Second)
	})

	t.Run("rfc3339", func(t *testing.T) {
		want := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
		got, err := parseQueryTime("2024-01-02T03:04:05Z", time.Time{})
		assert.NoError(t, err)
		assert.Equal(t, want, got)
	})

	t.Run("unix seconds", func(t *testing.T) {
		got, err := parseQueryTime("1700000000", time.Time{})
		assert.NoError(t, err)
		assert.Equal(t, time.Unix(1700000000, 0), got)
	})

	t.Run("invalid", func(t *testing.T) {
		_, err := parseQueryTime("not-a-time", time.Time{})
		assert.Error(t, err)
	})

	t.Run("bad relative duration", func(t *testing.T) {
		_, err := parseQueryTime("now-xyz", time.Time{})
		assert.Error(t, err)
		assert.True(t, strings.Contains(err.Error(), "invalid relative time"))
	})
}
