package grafana

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"emperror.dev/errors"
	"github.com/goccy/go-json"
)

// parseQueryTime parses a query time string. Supports: "" (returns def),
// "now", "now-1h" / "now-2h30m" (Go-duration suffix), RFC3339, and Unix seconds
// (integer or float). Returns def for "".
func parseQueryTime(s string, def time.Time) (time.Time, error) {
	if s == "" {
		return def, nil
	}

	now := time.Now()
	if s == "now" {
		return now, nil
	}

	if strings.HasPrefix(s, "now-") {
		d, err := time.ParseDuration(strings.TrimPrefix(s, "now-"))
		if err != nil {
			return time.Time{}, errors.Wrapf(err, "invalid relative time %q; use 'now-1h', 'now-30m', etc.", s)
		}
		return now.Add(-d), nil
	}

	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}

	if secs, err := strconv.ParseFloat(s, 64); err == nil {
		whole, frac := math.Modf(secs)
		return time.Unix(int64(whole), int64(frac*1e9)), nil
	}

	return time.Time{}, errors.Errorf("invalid time %q; use 'now', 'now-1h', an RFC3339 timestamp, or Unix seconds", s)
}

// executeQuery is the shared single-query executor used by both tools.
// It resolves the datasource type (unless hint is non-empty), routes to the
// right proxy path/time format, executes, and returns a QueryResultOutput.
// seriesCount is the true total; series is truncated to maxSeries.
// An empty result is a normal result (SeriesCount=0), not an error.
func (b *baseTool) executeQuery(ctx context.Context, instance, dataSourceUID, dataSourceTypeHint, expr, queryType string, evalTime, start, end time.Time, stepSeconds, maxSeries int) (*QueryResultOutput, error) {
	client, err := b.client(instance)
	if err != nil {
		return nil, err
	}

	dataSourceType := dataSourceTypeHint
	if dataSourceType == "" {
		body, err := client.GetDataSource(ctx, dataSourceUID)
		if err != nil {
			if isHTTPStatus(err, http.StatusNotFound) {
				return nil, errors.Wrapf(err, "datasource with UID %q not found", dataSourceUID)
			}
			return nil, errors.Wrapf(err, "failed to get datasource %q", dataSourceUID)
		}

		var ds dataSource
		if err := json.Unmarshal(body, &ds); err != nil {
			return nil, errors.Wrap(err, "failed to unmarshal data source")
		}
		dataSourceType = ds.Type
	}

	var resp *proxyQueryResponse
	switch dataSourceType {
	case "prometheus":
		resp, err = client.QueryPrometheus(ctx, dataSourceUID, expr, queryType, evalTime, start, end, stepSeconds)
	case "loki":
		resp, err = client.QueryLoki(ctx, dataSourceUID, expr, queryType, evalTime, start, end, stepSeconds, 10, "backward")
	default:
		return nil, errors.Errorf("datasource %q is of unsupported type %q; only prometheus and loki are supported", dataSourceUID, dataSourceType)
	}
	if err != nil {
		if isHTTPStatus(err, http.StatusNotFound) {
			return nil, errors.Wrapf(err, "datasource with UID %q not found", dataSourceUID)
		}
		return nil, errors.Wrapf(err, "failed to query datasource %q", dataSourceUID)
	}

	allSeries, err := parseProxyQueryResult(resp)
	if err != nil {
		return nil, errors.Wrap(err, "failed to parse query result")
	}

	resultType := resp.Data.ResultType
	// An all-NaN vector/matrix (Prometheus) means the query matched series but
	// every value is NaN; treat it as no-data, mirroring mcp-grafana.
	if dataSourceType == "prometheus" && (resultType == "vector" || resultType == "matrix") && allSeriesNaN(allSeries) {
		allSeries = nil
	}

	series := allSeries
	truncated := false
	if maxSeries > 0 && len(allSeries) > maxSeries {
		series = allSeries[:maxSeries]
		truncated = true
	}

	output := &QueryResultOutput{
		DataSourceUID:  dataSourceUID,
		DataSourceType: dataSourceType,
		Expr:           expr,
		QueryType:      queryType,
		ResultType:     resultType,
		SeriesCount:    len(allSeries),
		Truncated:      truncated,
		Series:         series,
	}
	if len(allSeries) == 0 {
		output.Hints = emptyResultHints(dataSourceType, expr, evalTime)
	}
	return output, nil
}

// allSeriesNaN reports whether every series in a vector/matrix result carries a
// NaN value (and at least one value exists). Empty series (neither Value nor
// Sample) are ignored.
func allSeriesNaN(series []SeriesSummary) bool {
	hasValue := false
	for _, s := range series {
		if s.Value != nil {
			hasValue = true
			if !math.IsNaN(*s.Value) {
				return false
			}
		} else if s.Sample != nil {
			hasValue = true
			if !math.IsNaN(s.Sample.Value) {
				return false
			}
		}
	}
	return hasValue
}

// emptyResultHints returns LLM-facing hints for an empty query result.
func emptyResultHints(dataSourceType, expr string, evalTime time.Time) []string {
	hints := []string{
		"The query returned no data. This is a normal result, NOT an error — it means no series matched at the evaluation time.",
		fmt.Sprintf("Evaluation time: %s.", evalTime.UTC().Format(time.RFC3339)),
		"Check that the metric/stream name and label names exist and are spelled correctly.",
		"Try widening the time range (e.g. time='now-6h', or a range query with a longer start).",
	}
	if dataSourceType == "loki" {
		hints = append(hints, `For LogQL, verify the stream selector's label matchers (e.g. {app="checkout"}) and the log line filter.`)
	} else {
		hints = append(hints, "For PromQL, verify the metric name and label matchers; consider removing filters to see whether any series exist.")
	}
	return hints
}

// panelQuery is an intermediate extracted query.
type panelQuery struct {
	RefID      string
	Expr       string
	DataSource DataSourceRef
}

// collectPanels returns all panels from a dashboard model, handling v1
// (top-level "panels" with row nesting, falling back to legacy "rows[].panels")
// and v2 ("elements" map of {kind:"Panel", spec:{...}}).
func collectPanels(model map[string]any) []map[string]any {
	var panels []map[string]any

	if raw, ok := model["panels"].([]any); ok {
		for _, item := range raw {
			pm, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if isRowPanel(pm) {
				if nested, ok := pm["panels"].([]any); ok {
					panels = append(panels, flattenPanels(nested)...)
				}
				continue
			}
			panels = append(panels, pm)
		}
		if len(panels) > 0 {
			return panels
		}
	}

	// Legacy dashboards: rows[].panels.
	if rows, ok := model["rows"].([]any); ok {
		for _, item := range rows {
			if row, ok := item.(map[string]any); ok {
				if nested, ok := row["panels"].([]any); ok {
					panels = append(panels, flattenPanels(nested)...)
				}
			}
		}
		if len(panels) > 0 {
			return panels
		}
	}

	// v2 dashboards: elements map of {kind, spec}.
	if elements, ok := model["elements"].(map[string]any); ok {
		keys := make([]string, 0, len(elements))
		for k := range elements {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			elem, ok := elements[k].(map[string]any)
			if !ok {
				continue
			}
			if kind, _ := elem["kind"].(string); kind == "Panel" {
				if spec, ok := elem["spec"].(map[string]any); ok {
					panels = append(panels, spec)
				}
			}
		}
	}

	return panels
}

// flattenPanels extracts the map[string]any panel entries from a raw JSON
// array, dropping any non-object entries.
func flattenPanels(items []any) []map[string]any {
	panels := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if p, ok := item.(map[string]any); ok {
			panels = append(panels, p)
		}
	}
	return panels
}

// isRowPanel reports whether a v1 panel entry is a "row" container.
func isRowPanel(panel map[string]any) bool {
	t, _ := panel["type"].(string)
	return t == "row"
}

// extractPanelQueries extracts the queries from a single panel. Returns one
// entry per target with a non-empty expression. Probes expr/query/expression/
// rawSql/rawSQL/rawQuery for the query string (first non-empty wins). Reads
// datasource {uid,type} from the target, then the panel-level datasource.
func extractPanelQueries(panel map[string]any) []panelQuery {
	panelDS := extractDataSourceRef(panel["datasource"])

	// v1 panels: targets[].
	if targets, ok := panel["targets"].([]any); ok {
		var queries []panelQuery
		for _, item := range targets {
			tm, ok := item.(map[string]any)
			if !ok {
				continue
			}
			expr := firstNonEmptyString(tm, "expr", "query", "expression", "rawSql", "rawSQL", "rawQuery")
			if expr == "" {
				continue
			}
			ds := extractDataSourceRef(tm["datasource"])
			if ds.UID == "" && ds.Type == "" {
				ds = panelDS
			}
			queries = append(queries, panelQuery{
				RefID:      firstNonEmptyString(tm, "refId", "refID"),
				Expr:       expr,
				DataSource: ds,
			})
		}
		return queries
	}

	// v2 panels: data.spec.queries[].spec.query.spec with datasource.name as
	// UID and query.group as type.
	if data, ok := panel["data"].(map[string]any); ok {
		if dataSpec, ok := data["spec"].(map[string]any); ok {
			if v2Queries, ok := dataSpec["queries"].([]any); ok {
				var queries []panelQuery
				for _, item := range v2Queries {
					qm, ok := item.(map[string]any)
					if !ok {
						continue
					}
					qSpec, _ := qm["spec"].(map[string]any)
					query, _ := qSpec["query"].(map[string]any)
					querySpec, _ := query["spec"].(map[string]any)

					expr := firstNonEmptyString(querySpec, "expr", "query", "expression")
					if expr == "" {
						continue
					}

					ds := DataSourceRef{}
					if d, ok := qSpec["datasource"].(map[string]any); ok {
						ds.UID, _ = d["name"].(string)
					}
					ds.Type, _ = query["group"].(string)

					queries = append(queries, panelQuery{
						RefID:      firstNonEmptyString(qm, "refId", "refID"),
						Expr:       expr,
						DataSource: ds,
					})
				}
				return queries
			}
		}
	}

	return nil
}

// extractDataSourceRef extracts a minimal {uid, type} datasource reference from
// a panel/target "datasource" value. Legacy string names carry no type/uid and
// yield an empty reference.
func extractDataSourceRef(v any) DataSourceRef {
	d, ok := v.(map[string]any)
	if !ok {
		return DataSourceRef{}
	}
	return DataSourceRef{
		UID:  firstNonEmptyString(d, "uid"),
		Type: firstNonEmptyString(d, "type"),
	}
}

// firstNonEmptyString returns the first non-empty string among the given keys.
func firstNonEmptyString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := m[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

// panelID returns the numeric "id" of a panel (0 when absent/non-numeric).
func panelID(panel map[string]any) int {
	switch v := panel["id"].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case json.Number:
		n, _ := v.Int64()
		return int(n)
	}
	return 0
}

// panelTitle returns the "title" of a panel ("" when absent).
func panelTitle(panel map[string]any) string {
	return firstNonEmptyString(panel, "title")
}

// panelType returns the "type" of a panel ("" when absent).
func panelType(panel map[string]any) string {
	return firstNonEmptyString(panel, "type")
}

// panelDataSource returns the panel-level datasource reference, or nil when the
// panel has none configured.
func panelDataSource(panel map[string]any) *DataSourceRef {
	ds := extractDataSourceRef(panel["datasource"])
	if ds.UID == "" && ds.Type == "" {
		return nil
	}
	return &ds
}

// queryVerdict returns the verdict string for a series count vs. threshold.
func queryVerdict(seriesCount, maxSeriesPerPanel int, err error) (verdict string, errMsg string) {
	if err != nil {
		return "error", err.Error()
	}
	if seriesCount == 0 {
		return "no-data", ""
	}
	if seriesCount > maxSeriesPerPanel {
		return "too-many-series", ""
	}
	return "ok", ""
}

// panelVerdict rolls up per-query verdicts to a single panel verdict.
// Precedence (worst first): error > no-data > too-many-series > skipped > ok.
func panelVerdict(queryVerdicts []string) string {
	worst := "ok"
	for _, v := range queryVerdicts {
		if verdictRank(v) > verdictRank(worst) {
			worst = v
		}
	}
	return worst
}

// verdictRank orders verdicts from best (ok) to worst (error).
func verdictRank(verdict string) int {
	switch verdict {
	case "error":
		return 4
	case "no-data":
		return 3
	case "too-many-series":
		return 2
	case "skipped":
		return 1
	default:
		return 0
	}
}
