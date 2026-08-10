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

const dashboardSearchDescription = `
** General Purpose **
It searches for Grafana dashboards by title query, tags, folder, and type.
Returns matching dashboards with their URLs for direct access.

** Output **
It returns a JSON array of objects, where each object represents a dashboard with the following fields:
- uid: the dashboard UID.
- title: the dashboard title.
- url: the full URL to access the dashboard in Grafana.
- type: the resource type ("dash-db" for dashboards, "dash-folder" for folders).
- tags: the dashboard tags.
- folderTitle: the title of the folder containing the dashboard.
- folderUid: the UID of the folder containing the dashboard.
`

// DashboardSearchParams defines the parameters for searching Grafana dashboards.
type DashboardSearchParams struct {
	Instance   string                    `json:"instance" validate:"required" jsonschema:"(required) The Grafana instance to connect to."`
	Query      string                    `json:"query,omitempty" jsonschema:"(optional) Title search query. Matches dashboard titles containing this string."`
	Type       string                    `json:"type,omitempty" validate:"omitempty,oneof=dash-db dash-folder" jsonschema:"(optional) Filter by type: 'dash-db' for dashboards or 'dash-folder' for folders."`
	Tags       []string                  `json:"tags,omitempty" validate:"omitempty" jsonschema:"(optional) Filter by tags. A dashboard must have ALL specified tags to match."`
	FolderUIDs []string                  `json:"folderUIDs,omitempty" validate:"omitempty" jsonschema:"(optional) Filter by folder UIDs. Only dashboards in the specified folders are returned."`
	Sort       string                    `json:"sort,omitempty" validate:"omitempty,oneof=alpha_asc alpha_desc created_asc created_desc updated_asc updated_desc" jsonschema:"(optional) Sort order for results."`
	Filter     string                    `json:"filter,omitempty" jsonschema:"(optional) A Go RE2 regex applied on each dashboard JSON output. Keep only dashboards that match the pattern. RE2 does NOT support lookahead (?=...)/(?!...), lookbehind (?<=...)/(?<!...), or backreferences — such patterns return an error. Example: 'prod|staging'. Invalid regex returns an error."`
	Paginate   *DashboardSearchPaginate `json:"paginate,omitempty" jsonschema:"(optional) Pagination parameters."`
}

// DashboardSearchPaginate defines pagination parameters for dashboard search.
type DashboardSearchPaginate struct {
	PageSize int `json:"pageSize,omitempty" validate:"omitempty,min=1,max=5000" jsonschema:"(optional) Number of results per page. Default is 100, max 5000."`
	Page     int `json:"page,omitempty" validate:"omitempty,min=1" jsonschema:"(optional) Page number (1-based). Default is 1."`
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

// DashboardSearchTool is an eino tool for searching Grafana dashboards.
type DashboardSearchTool struct {
	*baseTool
	tool.InvokableTool
}

// Invoke searches for Grafana dashboards matching the given parameters.
func (t *DashboardSearchTool) Invoke(ctx context.Context, params *DashboardSearchParams) (result string, err error) {
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

	re, err := filter.Compile(params.Filter)
	if err != nil {
		return "", errors.Wrap(err, "error when compile regex")
	}

	c, err := t.client(params.Instance)
	if err != nil {
		return "", err
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

// NewDashboardSearchTool creates a new DashboardSearchTool.
func NewDashboardSearchTool(ctx context.Context, configs Configs) (*DashboardSearchTool, error) {
	base, err := newBaseTool(ctx, configs)
	if err != nil {
		return nil, err
	}

	searchTool := &DashboardSearchTool{baseTool: base}
	t, err := utils.InferTool("grafana_dashboard_search", fmt.Sprintf("%s\n%s", dashboardSearchDescription, dashboardSearchOutputGuidance), searchTool.Invoke)
	if err != nil {
		return nil, err
	}
	searchTool.InvokableTool = t

	return searchTool, nil
}
