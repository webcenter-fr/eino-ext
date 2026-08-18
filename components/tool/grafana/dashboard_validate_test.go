package grafana

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/goccy/go-json"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func intPtr(v int) *int { return &v }

func TestNewDashboardValidateToolRequiresConfigs(t *testing.T) {
	_, err := NewDashboardValidateTool(context.Background(), Configs{})
	assert.Error(t, err)
}

func TestDashboardValidateToolConstructor(t *testing.T) {
	tool, err := NewDashboardValidateTool(context.Background(), Configs{"t": {URL: "http://localhost"}})
	require.NoError(t, err)
	require.NotNil(t, tool)

	info, err := tool.Info(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "grafana_dashboard_validate", info.Name)
}

func (t *ToolTestSuite) TestDashboardValidate() {
	ctx := context.Background()
	tool, err := NewDashboardValidateTool(ctx, t.configs)
	assert.NoError(t.T(), err)

	_, err = tool.Info(ctx)
	assert.NoError(t.T(), err)

	t.Run("validate all panels", func() {
		result, err := tool.Invoke(ctx, &DashboardValidateParams{Instance: "test", UID: "validate-dash"})
		assert.NoError(t.T(), err)

		var out DashboardValidationOutput
		assert.NoError(t.T(), json.Unmarshal([]byte(result), &out))
		assert.Equal(t.T(), 5, out.PanelCount)
		assert.Equal(t.T(), 3, out.ValidatedPanels)
		assert.Equal(t.T(), 2, out.Summary.OK)
		assert.Equal(t.T(), 1, out.Summary.NoData)
		assert.Equal(t.T(), 0, out.Summary.TooManySeries)
		assert.Equal(t.T(), 0, out.Summary.Errors)
		assert.Equal(t.T(), 2, out.Summary.Skipped)

		require.Len(t.T(), out.Panels, 5)
		verdicts := make([]string, len(out.Panels))
		for i, p := range out.Panels {
			verdicts[i] = p.Verdict
		}
		assert.Equal(t.T(), []string{"ok", "no-data", "ok", "skipped", "skipped"}, verdicts)

		// The skipped panels carry a reason.
		assert.Equal(t.T(), "unsupported datasource type: mysql", out.Panels[3].Reason)
		assert.Equal(t.T(), "panel has no datasource configured", out.Panels[4].Reason)
	})

	t.Run("panelID set validates only that panel", func() {
		result, err := tool.Invoke(ctx, &DashboardValidateParams{Instance: "test", UID: "validate-dash", PanelID: intPtr(2)})
		assert.NoError(t.T(), err)

		var out DashboardValidationOutput
		assert.NoError(t.T(), json.Unmarshal([]byte(result), &out))
		assert.Equal(t.T(), 1, out.PanelCount)
		require.Len(t.T(), out.Panels, 1)
		assert.Equal(t.T(), 2, out.Panels[0].PanelID)
		assert.Equal(t.T(), "no-data", out.Panels[0].Verdict)
	})

	t.Run("maxPanels caps validation", func() {
		result, err := tool.Invoke(ctx, &DashboardValidateParams{Instance: "test", UID: "validate-dash", MaxPanels: 2})
		assert.NoError(t.T(), err)

		var out DashboardValidationOutput
		assert.NoError(t.T(), json.Unmarshal([]byte(result), &out))
		assert.Equal(t.T(), 5, out.PanelCount)
		require.Len(t.T(), out.Panels, 5)
		assert.Equal(t.T(), "ok", out.Panels[0].Verdict)
		assert.Equal(t.T(), "no-data", out.Panels[1].Verdict)
		for _, p := range out.Panels[2:] {
			assert.Equal(t.T(), "skipped", p.Verdict)
			assert.Equal(t.T(), "panel limit reached", p.Reason)
		}
		assert.Equal(t.T(), 3, out.Summary.Skipped)
	})

	t.Run("v2 dashboard elements", func() {
		result, err := tool.Invoke(ctx, &DashboardValidateParams{Instance: "test", UID: "v2-dash"})
		assert.NoError(t.T(), err)

		var out DashboardValidationOutput
		assert.NoError(t.T(), json.Unmarshal([]byte(result), &out))
		assert.Equal(t.T(), 1, out.PanelCount)
		require.Len(t.T(), out.Panels, 1)
		assert.Equal(t.T(), 1, out.Panels[0].PanelID)
		assert.Equal(t.T(), "V2 CPU", out.Panels[0].Title)
		assert.Equal(t.T(), "ok", out.Panels[0].Verdict)
		require.Len(t.T(), out.Panels[0].Queries, 1)
		assert.Equal(t.T(), "up", out.Panels[0].Queries[0].Expr)
		assert.Equal(t.T(), "vector", out.Panels[0].Queries[0].ResultType)
	})

	t.Run("dashboard not found", func() {
		_, err := tool.Invoke(ctx, &DashboardValidateParams{Instance: "test", UID: "nonexistent"})
		assert.Error(t.T(), err)
		assert.Contains(t.T(), err.Error(), "not found")
	})

	t.Run("panelID not found", func() {
		_, err := tool.Invoke(ctx, &DashboardValidateParams{Instance: "test", UID: "validate-dash", PanelID: intPtr(999)})
		assert.Error(t.T(), err)
		assert.Contains(t.T(), err.Error(), "panel with ID 999 not found")
	})
}

func TestDashboardValidateTooManySeries(t *testing.T) {
	var series strings.Builder
	series.WriteString(`{"status":"success","data":{"resultType":"vector","result":[`)
	for i := 0; i < 25; i++ {
		if i > 0 {
			series.WriteString(",")
		}
		fmt.Fprintf(&series, `{"metric":{"__name__":"m","i":"%d"},"value":[1700000000,"1"]}`, i)
	}
	series.WriteString(`]}}`)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/dashboards/uid/many-dash", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"dashboard":{"uid":"many-dash","title":"Many","panels":[
			{"id":1,"title":"Many Series","type":"timeseries",
			 "datasource":{"uid":"ds-prom","type":"prometheus"},
			 "targets":[{"refId":"A","expr":"many","datasource":{"uid":"ds-prom","type":"prometheus"}}]}
		]},"meta":{}}`))
	})
	mux.HandleFunc("/api/datasources/proxy/uid/ds-prom/api/v1/query", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(series.String()))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	tool, err := NewDashboardValidateTool(context.Background(), Configs{"test": {URL: server.URL}})
	require.NoError(t, err)

	result, err := tool.Invoke(context.Background(), &DashboardValidateParams{
		Instance:          "test",
		UID:               "many-dash",
		MaxSeriesPerPanel: 20,
	})
	require.NoError(t, err)

	var out DashboardValidationOutput
	require.NoError(t, json.Unmarshal([]byte(result), &out))
	require.Len(t, out.Panels, 1)
	assert.Equal(t, "too-many-series", out.Panels[0].Verdict)
	assert.Equal(t, 1, out.Summary.TooManySeries)
	require.Len(t, out.Panels[0].Queries, 1)
	assert.Equal(t, 25, out.Panels[0].Queries[0].SeriesCount)
	assert.Equal(t, "too-many-series", out.Panels[0].Queries[0].Verdict)
}

func TestDashboardValidateErrorQuery(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/dashboards/uid/err-dash", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"dashboard":{"uid":"err-dash","title":"Err","panels":[
			{"id":1,"title":"Broken","type":"timeseries",
			 "datasource":{"uid":"ds-prom","type":"prometheus"},
			 "targets":[{"refId":"A","expr":"bad","datasource":{"uid":"ds-prom","type":"prometheus"}}]}
		]},"meta":{}}`))
	})
	mux.HandleFunc("/api/datasources/proxy/uid/ds-prom/api/v1/query", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"error","errorType":"bad_data","error":"parse error"}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	tool, err := NewDashboardValidateTool(context.Background(), Configs{"test": {URL: server.URL}})
	require.NoError(t, err)

	result, err := tool.Invoke(context.Background(), &DashboardValidateParams{Instance: "test", UID: "err-dash"})
	require.NoError(t, err)

	var out DashboardValidationOutput
	require.NoError(t, json.Unmarshal([]byte(result), &out))
	require.Len(t, out.Panels, 1)
	assert.Equal(t, "error", out.Panels[0].Verdict)
	assert.Equal(t, 1, out.Summary.Errors)
	require.Len(t, out.Panels[0].Queries, 1)
	assert.Equal(t, "error", out.Panels[0].Queries[0].Verdict)
	assert.Contains(t, out.Panels[0].Queries[0].Error, "bad_data")
}

func TestDashboardValidatePathEscape(t *testing.T) {
	var capturedURI string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedURI = r.RequestURI
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"dashboard":{"uid":"foo/bar","title":"X","panels":[]},"meta":{}}`))
	}))
	defer server.Close()

	tool, err := NewDashboardValidateTool(context.Background(), Configs{"test": {URL: server.URL}})
	require.NoError(t, err)

	_, err = tool.Invoke(context.Background(), &DashboardValidateParams{Instance: "test", UID: "foo/bar"})
	assert.NoError(t, err)
	assert.Contains(t, capturedURI, "/api/dashboards/uid/foo%2Fbar",
		"dashboard uid must be path-escaped on the wire to prevent endpoint traversal")
	assert.NotContains(t, capturedURI, "/api/dashboards/uid/foo/bar")
}

func TestDashboardValidateMaxSeriesSample(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/dashboards/uid/sample-dash", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"dashboard":{"uid":"sample-dash","title":"Sample","panels":[
			{"id":1,"title":"CPU","type":"timeseries",
			 "datasource":{"uid":"ds-prom","type":"prometheus"},
			 "targets":[{"refId":"A","expr":"up","datasource":{"uid":"ds-prom","type":"prometheus"}}]}
		]},"meta":{}}`))
	})
	mux.HandleFunc("/api/datasources/proxy/uid/ds-prom/api/v1/query", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[
			{"metric":{"__name__":"up","instance":"a"},"value":[1700000000,"1"]},
			{"metric":{"__name__":"up","instance":"b"},"value":[1700000000,"1"]},
			{"metric":{"__name__":"up","instance":"c"},"value":[1700000000,"1"]}
		]}}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	tool, err := NewDashboardValidateTool(context.Background(), Configs{"test": {URL: server.URL}})
	require.NoError(t, err)

	t.Run("default yields sample labels", func(t *testing.T) {
		result, err := tool.Invoke(context.Background(), &DashboardValidateParams{Instance: "test", UID: "sample-dash"})
		require.NoError(t, err)

		var out DashboardValidationOutput
		require.NoError(t, json.Unmarshal([]byte(result), &out))
		require.Len(t, out.Panels, 1)
		require.Len(t, out.Panels[0].Queries, 1)
		assert.Equal(t, 3, out.Panels[0].Queries[0].SeriesCount)
		assert.Equal(t, "ok", out.Panels[0].Queries[0].Verdict)
		assert.Len(t, out.Panels[0].Queries[0].SampleLabels, 3)
	})

	t.Run("explicit zero disables sample labels", func(t *testing.T) {
		result, err := tool.Invoke(context.Background(), &DashboardValidateParams{Instance: "test", UID: "sample-dash", MaxSeriesSample: intPtr(0)})
		require.NoError(t, err)

		var out DashboardValidationOutput
		require.NoError(t, json.Unmarshal([]byte(result), &out))
		require.Len(t, out.Panels, 1)
		require.Len(t, out.Panels[0].Queries, 1)
		assert.Equal(t, 3, out.Panels[0].Queries[0].SeriesCount)
		assert.Equal(t, "ok", out.Panels[0].Queries[0].Verdict)
		assert.Empty(t, out.Panels[0].Queries[0].SampleLabels)
	})
}

func TestValidatePanelQueryCap(t *testing.T) {
	tool := &DashboardValidateTool{baseTool: &baseTool{}}

	var targets []any
	for i := 0; i < maxQueriesPerPanel+5; i++ {
		targets = append(targets, map[string]any{
			"refId": fmt.Sprintf("A%d", i),
			"expr":  fmt.Sprintf("up%d", i),
		})
	}
	panel := map[string]any{
		"id":      float64(1),
		"title":   "Cap",
		"type":    "timeseries",
		"targets": targets,
	}

	result := tool.validatePanel(context.Background(), "test", panel, &DashboardValidateParams{MaxSeriesSample: intPtr(5)}, time.Now())

	assert.Len(t, result.Queries, maxQueriesPerPanel)
	assert.Equal(t, "skipped", result.Verdict)
	assert.Contains(t, result.Reason, "5 additional queries not validated (panel query limit reached)")
}

func TestValidatePanelExprTooLong(t *testing.T) {
	tool := &DashboardValidateTool{baseTool: &baseTool{}}
	longExpr := strings.Repeat("x", maxQueryExprLen+1)
	panel := map[string]any{
		"id":         float64(1),
		"title":      "Long",
		"type":       "timeseries",
		"datasource": map[string]any{"uid": "ds-prom", "type": "prometheus"},
		"targets": []any{
			map[string]any{"refId": "A", "expr": longExpr, "datasource": map[string]any{"uid": "ds-prom", "type": "prometheus"}},
		},
	}

	result := tool.validatePanel(context.Background(), "test", panel, &DashboardValidateParams{MaxSeriesSample: intPtr(5)}, time.Now())

	require.Len(t, result.Queries, 1)
	assert.Equal(t, "skipped", result.Queries[0].Verdict)
	assert.Contains(t, result.Queries[0].Error, "query expression too long")
	assert.Equal(t, "skipped", result.Verdict)
}

func TestValidatePanelMissingUID(t *testing.T) {
	tool := &DashboardValidateTool{baseTool: &baseTool{}}
	panel := map[string]any{
		"id":    float64(1),
		"title": "NoUID",
		"type":  "timeseries",
		"targets": []any{
			map[string]any{"refId": "A", "expr": "up", "datasource": map[string]any{"type": "prometheus"}},
		},
	}

	result := tool.validatePanel(context.Background(), "test", panel, &DashboardValidateParams{MaxSeriesSample: intPtr(5)}, time.Now())

	require.Len(t, result.Queries, 1)
	assert.Equal(t, "skipped", result.Queries[0].Verdict)
	assert.Equal(t, "panel datasource has no UID configured", result.Queries[0].Error)
}
