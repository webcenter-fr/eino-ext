package grafana

import (
	"context"
	"fmt"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/goccy/go-json"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/filter"
)

const dashboardDescription = `
** General Purpose **
It reads Grafana dashboards. Set 'uid' to describe (return) a single dashboard
by UID; leave 'uid' empty to search (list) dashboards by title query, tags,
folder, and type.

** Output **
- describe mode (uid set): a single JSON object with the dashboard model and
  metadata. Use 'excludeFieldsOutput' to trim large sections.
- search mode (uid empty): a JSON array of objects, each with:
  - uid: the dashboard UID.
  - title: the dashboard title.
  - url: the full URL to access the dashboard in Grafana.
  - type: the resource type ("dash-db" for dashboards, "dash-folder" for folders).
  - tags: the dashboard tags.
  - folderTitle: the title of the folder containing the dashboard.
  - folderUid: the UID of the folder containing the dashboard.
`

// DashboardParams defines the parameters for reading Grafana dashboards. When
// UID is set the tool describes a single dashboard; otherwise it searches.
type DashboardParams struct {
	Instance string `json:"instance" validate:"required" jsonschema:"(required) The Grafana instance to connect to."`
	UID      string `json:"uid,omitempty" jsonschema:"(optional) If set, return the full dashboard with this UID (describe mode, single object). If empty, search dashboards (list mode, array)."`
	// search-mode fields (ignored when UID is set)
	Query      string             `json:"query,omitempty" jsonschema:"(optional, search mode) Title search query."`
	Type       string             `json:"type,omitempty" validate:"omitempty,oneof=dash-db dash-folder" jsonschema:"(optional, search mode) Filter by type."`
	Tags       []string           `json:"tags,omitempty" jsonschema:"(optional, search mode) Filter by tags (ALL must match)."`
	FolderUIDs []string           `json:"folderUIDs,omitempty" jsonschema:"(optional, search mode) Filter by folder UIDs."`
	Sort       string             `json:"sort,omitempty" validate:"omitempty,oneof=alpha_asc alpha_desc created_asc created_desc updated_asc updated_desc" jsonschema:"(optional, search mode) Sort order."`
	Filter     string             `json:"filter,omitempty" jsonschema:"(optional, search mode) Go RE2 regex on each dashboard search output JSON."`
	Paginate   *DashboardPaginate `json:"paginate,omitempty" jsonschema:"(optional, search mode) Pagination."`
	// describe-mode fields (ignored when UID is empty)
	ExcludeFieldsOutput []string `json:"excludeFieldsOutput,omitempty" validate:"omitempty,dive,oneof=meta panels templating time annotations schemaVersion version" jsonschema:"(optional, describe mode) Fields to exclude from the dashboard output."`
}

// DashboardPaginate defines pagination parameters for dashboard search
// (renamed from DashboardSearchPaginate).
type DashboardPaginate struct {
	PageSize int `json:"pageSize,omitempty" validate:"omitempty,min=1,max=5000"`
	Page     int `json:"page,omitempty" validate:"omitempty,min=1"`
}

// DashboardSearchOutput is the structured output for a dashboard search result.
type DashboardSearchOutput struct {
	UID         string   `json:"uid"`
	Title       string   `json:"title"`
	URL         string   `json:"url"`
	Type        string   `json:"type"`
	Tags        []string `json:"tags"`
	FolderTitle string   `json:"folderTitle"`
	FolderUID   string   `json:"folderUid"`
}

// DashboardDescribeOutput is the structured output for a dashboard describe.
type DashboardDescribeOutput struct {
	Dashboard map[string]any `json:"dashboard,omitempty"`
	Meta      *dashboardMeta `json:"meta,omitempty"`
}

// DashboardTool is an eino tool for reading Grafana dashboards (search/describe).
type DashboardTool struct {
	*baseTool
	tool.InvokableTool
}

// Invoke reads dashboards: describe a single dashboard when UID is set,
// otherwise search.
func (t *DashboardTool) Invoke(ctx context.Context, params *DashboardParams) (result string, err error) {
	if params.Paginate != nil {
		if params.Paginate.PageSize == 0 {
			params.Paginate.PageSize = 100
		}
		if params.Paginate.Page == 0 {
			params.Paginate.Page = 1
		}
	}

	if err := validateParams(params); err != nil {
		return "", err
	}

	c, err := t.client(params.Instance)
	if err != nil {
		return "", err
	}

	if params.UID != "" {
		// describe mode
		body, err := c.GetDashboard(ctx, params.UID)
		if err != nil {
			return "", errors.Wrap(err, "failed to get dashboard")
		}

		var dr dashboardResponse
		if err := json.Unmarshal(body, &dr); err != nil {
			return "", errors.Wrap(err, "failed to unmarshal dashboard response")
		}

		output := &DashboardDescribeOutput{
			Dashboard: dr.Dashboard,
			Meta:      &dr.Meta,
		}

		if err := applyExcludes(params.ExcludeFieldsOutput, map[string]func(){
			"meta":          func() { output.Meta = nil },
			"panels":        func() { delete(output.Dashboard, "panels") },
			"templating":    func() { delete(output.Dashboard, "templating") },
			"time":          func() { delete(output.Dashboard, "time") },
			"annotations":   func() { delete(output.Dashboard, "annotations") },
			"schemaVersion": func() { delete(output.Dashboard, "schemaVersion") },
			"version":       func() { delete(output.Dashboard, "version") },
		}); err != nil {
			return "", err
		}

		return marshalJSON(output, "failed to marshal output")
	}

	// search mode
	re, err := filter.Compile(params.Filter)
	if err != nil {
		return "", errors.Wrap(err, "error when compile regex")
	}

	sp := &searchParams{
		Query:      params.Query,
		Type:       params.Type,
		Tags:       params.Tags,
		FolderUIDs: params.FolderUIDs,
		Sort:       params.Sort,
	}
	if params.Paginate != nil {
		sp.Limit = params.Paginate.PageSize
		sp.Page = params.Paginate.Page
	}

	body, err := c.SearchDashboards(ctx, sp)
	if err != nil {
		return "", errors.Wrap(err, "failed to search dashboards")
	}

	var hits []searchHit
	if err := json.Unmarshal(body, &hits); err != nil {
		return "", errors.Wrap(err, "failed to unmarshal search results")
	}

	return filterMapMarshal(hits, re, func(item searchHit) DashboardSearchOutput {
		return DashboardSearchOutput{
			UID:         item.UID,
			Title:       item.Title,
			URL:         c.baseURL + item.URL,
			Type:        item.Type,
			Tags:        item.Tags,
			FolderTitle: item.FolderTitle,
			FolderUID:   item.FolderUID,
		}
	})
}

// NewDashboardTool creates a new DashboardTool.
func NewDashboardTool(ctx context.Context, configs Configs) (*DashboardTool, error) {
	base, err := newBaseTool(ctx, configs)
	if err != nil {
		return nil, err
	}

	dashboardTool := &DashboardTool{baseTool: base}
	t, err := utils.InferTool("grafana_dashboard", fmt.Sprintf("%s\n%s", dashboardDescription, dashboardSearchOutputGuidance), dashboardTool.Invoke)
	if err != nil {
		return nil, err
	}
	dashboardTool.InvokableTool = t

	return dashboardTool, nil
}
