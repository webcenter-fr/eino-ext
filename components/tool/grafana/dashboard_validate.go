package grafana

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

const dashboardValidateToolName = "grafana_dashboard_validate"

// maxQueriesPerPanel caps how many queries are executed per panel during
// validation. Excess queries are not executed and are noted in the reason.
const maxQueriesPerPanel = 20

const dashboardValidateDescription = `
** General Purpose **
Fetch a saved dashboard by UID and validate every panel by executing its
Prometheus/Loki queries. Returns a per-panel, per-query verdict so you can see
which panels have no data, which return too many series, and which errored.
Use this AFTER creating or updating a dashboard to confirm its panels work.

Uses INSTANT queries (anchored at 'time', default 'now') — sufficient to detect
no-data and cardinality. Non-Prometheus/Loki panels are reported as 'skipped'
with a reason; they are not executed.

** Output **
A JSON object with: uid, title, panelCount, validatedPanels, summary{}, panels[].
Each panel has: panelId, title, type, datasource, verdict, reason, queries[].
Each query has: refId, expr, resultType, seriesCount, verdict, error, sampleLabels[].
Verdicts: 'ok' | 'no-data' | 'too-many-series' | 'error' | 'skipped'.
`

// DashboardValidateParams defines the parameters for grafana_dashboard_validate.
type DashboardValidateParams struct {
	Instance          string `json:"instance" validate:"required" jsonschema:"(required) The Grafana instance to connect to."`
	UID               string `json:"uid" validate:"required,max=256" jsonschema:"(required) The UID of the saved dashboard to validate."`
	PanelID           *int   `json:"panelID,omitempty" validate:"omitempty,min=1" jsonschema:"(optional) Validate only this panel by id. If empty, validate all panels."`
	Time              string `json:"time,omitempty" validate:"omitempty,max=64" jsonschema:"(optional) Instant-query anchor time. 'now' (default), 'now-1h', RFC3339, or Unix seconds."`
	MaxSeriesPerPanel int    `json:"maxSeriesPerPanel,omitempty" validate:"omitempty,min=1,max=10000" jsonschema:"(optional) Panels with more than this many series are flagged 'too-many-series' (default 20)."`
	MaxPanels         int    `json:"maxPanels,omitempty" validate:"omitempty,min=1,max=500" jsonschema:"(optional) Cap on the number of panels to validate (default 50). Excess panels are skipped with a reason."`
	MaxSeriesSample   *int   `json:"maxSeriesSample,omitempty" validate:"omitempty,min=0,max=1000" jsonschema:"(optional) Number of sample label sets to include per query (default 5). 0 disables sample labels."`
}

// DashboardValidationOutput is the structured output of grafana_dashboard_validate.
type DashboardValidationOutput struct {
	UID             string                  `json:"uid"`
	Title           string                  `json:"title"`
	PanelCount      int                     `json:"panelCount"`
	ValidatedPanels int                     `json:"validatedPanels"`
	Summary         ValidationSummary       `json:"summary"`
	Panels          []PanelValidationResult `json:"panels"`
}

type ValidationSummary struct {
	OK            int `json:"ok"`
	NoData        int `json:"noData"`
	TooManySeries int `json:"tooManySeries"`
	Errors        int `json:"errors"`
	Skipped       int `json:"skipped"`
}

// add increments the counter for the given panel verdict.
func (s *ValidationSummary) add(verdict string) {
	switch verdict {
	case "ok":
		s.OK++
	case "no-data":
		s.NoData++
	case "too-many-series":
		s.TooManySeries++
	case "error":
		s.Errors++
	default:
		s.Skipped++
	}
}

// validated reports how many panels were actually executed (all non-skipped).
func (s *ValidationSummary) validated() int {
	return s.OK + s.NoData + s.TooManySeries + s.Errors
}

type PanelValidationResult struct {
	PanelID    int                     `json:"panelId"`
	Title      string                  `json:"title"`
	Type       string                  `json:"type"`
	DataSource *DataSourceRef          `json:"datasource,omitempty"`
	Verdict    string                  `json:"verdict"`
	Reason     string                  `json:"reason,omitempty"`
	Queries    []QueryValidationResult `json:"queries,omitempty"`
}

type QueryValidationResult struct {
	RefID        string              `json:"refId"`
	Expr         string              `json:"expr"`
	ResultType   string              `json:"resultType,omitempty"`
	SeriesCount  int                 `json:"seriesCount"`
	Verdict      string              `json:"verdict"`
	Error        string              `json:"error,omitempty"`
	SampleLabels []map[string]string `json:"sampleLabels,omitempty"`
}

// DataSourceRef is a minimal datasource reference extracted from a panel.
type DataSourceRef struct {
	UID  string `json:"uid,omitempty"`
	Type string `json:"type,omitempty"`
}

// DashboardValidateTool is an eino tool for validating a saved dashboard's panels.
type DashboardValidateTool struct {
	*baseTool
	tool.InvokableTool
}

// Invoke fetches a saved dashboard and validates its panels' queries.
func (t *DashboardValidateTool) Invoke(ctx context.Context, params *DashboardValidateParams) (string, error) {
	if params.Time == "" {
		params.Time = "now"
	}
	if params.MaxSeriesPerPanel == 0 {
		params.MaxSeriesPerPanel = 20
	}
	if params.MaxPanels == 0 {
		params.MaxPanels = 50
	}
	if params.MaxSeriesSample == nil {
		sample := 5
		params.MaxSeriesSample = &sample
	}

	if err := validateParams(params); err != nil {
		return "", err
	}

	evalTime, err := parseQueryTime(params.Time, time.Now())
	if err != nil {
		return "", err
	}

	dr, err := t.fetchDashboard(ctx, params.Instance, params.UID)
	if err != nil {
		if isHTTPStatus(err, http.StatusNotFound) {
			return "", errors.Wrapf(err, "dashboard with UID %q not found", params.UID)
		}
		return "", err
	}

	panels := collectPanels(dr.Dashboard)

	if params.PanelID != nil {
		var target map[string]any
		for _, p := range panels {
			if panelID(p) == *params.PanelID {
				target = p
				break
			}
		}
		if target == nil {
			return "", errors.Errorf("panel with ID %d not found in dashboard %q", *params.PanelID, params.UID)
		}
		panels = []map[string]any{target}
	}

	output := &DashboardValidationOutput{
		UID:        params.UID,
		Title:      dashboardTitle(dr.Dashboard),
		PanelCount: len(panels),
	}

	limit := len(panels)
	if limit > params.MaxPanels {
		limit = params.MaxPanels
	}

	for _, panel := range panels[:limit] {
		pvr := t.validatePanel(ctx, params.Instance, panel, params, evalTime)
		output.Panels = append(output.Panels, pvr)
		output.Summary.add(pvr.Verdict)
	}

	// Panels beyond maxPanels are skipped with a reason.
	for _, panel := range panels[limit:] {
		output.Panels = append(output.Panels, PanelValidationResult{
			PanelID: panelID(panel),
			Title:   panelTitle(panel),
			Type:    panelType(panel),
			Verdict: "skipped",
			Reason:  "panel limit reached",
		})
		output.Summary.add("skipped")
	}

	output.ValidatedPanels = output.Summary.validated()

	return marshalJSON(output, "failed to marshal output")
}

// validatePanel validates a single panel by executing each extracted query.
func (t *DashboardValidateTool) validatePanel(ctx context.Context, instance string, panel map[string]any, params *DashboardValidateParams, evalTime time.Time) PanelValidationResult {
	result := PanelValidationResult{
		PanelID:    panelID(panel),
		Title:      panelTitle(panel),
		Type:       panelType(panel),
		DataSource: panelDataSource(panel),
	}

	queries := extractPanelQueries(panel)
	if len(queries) == 0 {
		result.Verdict = "skipped"
		result.Reason = "panel has no queries"
		return result
	}

	excess := 0
	if len(queries) > maxQueriesPerPanel {
		excess = len(queries) - maxQueriesPerPanel
		queries = queries[:maxQueriesPerPanel]
	}

	maxSample := *params.MaxSeriesSample

	var skippedReason string
	queryVerdicts := make([]string, 0, len(queries))
	for _, q := range queries {
		qr := QueryValidationResult{RefID: q.RefID, Expr: q.Expr}

		switch {
		case q.DataSource.UID == "" && q.DataSource.Type == "":
			qr.Verdict = "skipped"
			qr.Error = "panel has no datasource configured"
		case q.DataSource.UID == "":
			qr.Verdict = "skipped"
			qr.Error = "panel datasource has no UID configured"
		case q.DataSource.Type != "prometheus" && q.DataSource.Type != "loki":
			qr.Verdict = "skipped"
			qr.Error = fmt.Sprintf("unsupported datasource type: %s", q.DataSource.Type)
		case len(q.Expr) > maxQueryExprLen:
			qr.Verdict = "skipped"
			qr.Error = fmt.Sprintf("query expression too long (%d bytes, max %d)", len(q.Expr), maxQueryExprLen)
		default:
			out, err := t.executeQuery(ctx, instance, q.DataSource.UID, q.DataSource.Type, q.Expr, "instant", evalTime, time.Time{}, time.Time{}, 0, maxSample)
			if err != nil {
				qr.Verdict, qr.Error = queryVerdict(0, params.MaxSeriesPerPanel, err)
			} else {
				qr.Verdict, qr.Error = queryVerdict(out.SeriesCount, params.MaxSeriesPerPanel, nil)
				qr.ResultType = out.ResultType
				qr.SeriesCount = out.SeriesCount
				if maxSample > 0 {
					for _, s := range out.Series {
						qr.SampleLabels = append(qr.SampleLabels, s.Labels)
					}
				}
			}
		}
		if qr.Verdict == "skipped" && skippedReason == "" {
			skippedReason = qr.Error
		}

		queryVerdicts = append(queryVerdicts, qr.Verdict)
		result.Queries = append(result.Queries, qr)
	}

	result.Verdict = panelVerdict(queryVerdicts)
	if result.Verdict == "skipped" {
		result.Reason = skippedReason
	}
	if excess > 0 {
		note := fmt.Sprintf("%d additional queries not validated (panel query limit reached)", excess)
		if result.Reason != "" {
			result.Reason += "; " + note
		} else {
			result.Reason = note
		}
	}
	return result
}

// NewDashboardValidateTool creates a new DashboardValidateTool.
func NewDashboardValidateTool(ctx context.Context, configs Configs) (*DashboardValidateTool, error) {
	base, err := newBaseTool(ctx, configs)
	if err != nil {
		return nil, err
	}

	validateTool := &DashboardValidateTool{baseTool: base}
	t, err := utils.InferTool(dashboardValidateToolName, fmt.Sprintf("%s\n%s", dashboardValidateDescription, dashboardValidateOutputGuidance), validateTool.Invoke)
	if err != nil {
		return nil, err
	}
	validateTool.InvokableTool = t

	return validateTool, nil
}
