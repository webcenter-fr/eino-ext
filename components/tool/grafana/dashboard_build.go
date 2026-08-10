package grafana

import (
	"context"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/goccy/go-json"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/confirm"
)

const dashboardBuildDescription = `
** General Purpose **
It creates or updates a Grafana dashboard from a JSON dashboard model.
Returns the final URL of the dashboard.

** Safety **
Always use dryRun=true first to validate the dashboard model before saving.
After reviewing the dry-run result, set confirmed=true to actually save.

** Dashboard Protection **
Dashboards matching the instance's protected blocklist (by UID, title prefix,
folder, or tag) cannot be modified. If you attempt to update a protected
dashboard, the tool returns an error.

** Output **
It returns a JSON object with the saved dashboard's UID, URL, version, and status.
`

// DashboardBuildParams defines the parameters for building a Grafana dashboard.
type DashboardBuildParams struct {
	Instance  string `json:"instance" validate:"required" jsonschema:"(required) The Grafana instance to connect to."`
	Dashboard string `json:"dashboard" validate:"required" jsonschema:"(required) The full Grafana dashboard model as a JSON string. Must include 'title'. Include 'uid' to update an existing dashboard; omit 'uid' to create a new one."`
	FolderUID string `json:"folderUID,omitempty" jsonschema:"(optional) The UID of the folder to place the dashboard in. Omit for the root folder."`
	Message   string `json:"message,omitempty" jsonschema:"(optional) Commit message for the dashboard version."`
	Overwrite bool   `json:"overwrite,omitempty" jsonschema:"(optional) If true, overwrite an existing dashboard with the same UID without version checking."`
	DryRun    bool   `json:"dryRun,omitempty" jsonschema:"(optional) If true, validate the dashboard model without saving. Show the result to the user and ask for confirmation."`
	Confirmed bool   `json:"confirmed,omitempty" jsonschema:"(optional) Must be true to actually save. Set this after the user has approved the dry-run result."`
}

// DashboardBuildOutput is the structured output for a dashboard build result.
type DashboardBuildOutput struct {
	UID     string `json:"uid"`
	URL     string `json:"url"`
	Status  string `json:"status"`
	Version int    `json:"version"`
	Slug    string `json:"slug"`
}

// DashboardBuildTool is an eino tool for creating/updating Grafana dashboards.
type DashboardBuildTool struct {
	*baseTool
	tool.InvokableTool
}

// Invoke creates or updates a Grafana dashboard.
func (t *DashboardBuildTool) Invoke(ctx context.Context, params *DashboardBuildParams) (result string, err error) {
	if err := validateParams(params); err != nil {
		return "", err
	}

	if err := confirm.RequireConfirmation(params.DryRun, params.Confirmed); err != nil {
		return "", err
	}

	c, err := t.client(params.Instance)
	if err != nil {
		return "", err
	}

	var dashboardModel map[string]any
	if err := json.Unmarshal([]byte(params.Dashboard), &dashboardModel); err != nil {
		return "", errors.Wrap(err, "invalid dashboard JSON")
	}

	uid, _ := dashboardModel["uid"].(string)

	title, _ := dashboardModel["title"].(string)
	if title == "" {
		return "", errors.Errorf("dashboard model must include a title")
	}

	if uid != "" {
		if err := t.checkProtected(ctx, params.Instance, uid); err != nil {
			return "", err
		}
	}

	// Defense-in-depth: also evaluate the NEW dashboard model (and target
	// folder) against the blocklist. This prevents creating a new dashboard
	// — or renaming an existing one — so that it matches protected criteria,
	// which would otherwise bypass the existing-dashboard check above.
	if err := t.checkProtectedModel(params.Instance, dashboardModel, params.FolderUID); err != nil {
		return "", err
	}

	if params.DryRun {
		dryRunPreview := map[string]any{
			"dryRun":    true,
			"dashboard": dashboardModel,
			"folderUID": params.FolderUID,
			"overwrite": params.Overwrite,
		}
		data, err := json.Marshal(dryRunPreview)
		if err != nil {
			return "", errors.Wrap(err, "failed to marshal dry-run preview")
		}
		return string(data), nil
	}

	req := saveDashboardRequest{
		Dashboard: dashboardModel,
		FolderUID: params.FolderUID,
		Message:   params.Message,
		Overwrite: params.Overwrite,
	}

	payload, err := json.Marshal(req)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal save request")
	}

	body, err := c.SaveDashboard(ctx, payload)
	if err != nil {
		return "", errors.Wrap(err, "failed to save dashboard")
	}

	var resp saveDashboardResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", errors.Wrap(err, "failed to unmarshal save response")
	}

	output := DashboardBuildOutput{
		UID:     resp.UID,
		URL:     c.baseURL + resp.URL,
		Status:  resp.Status,
		Version: resp.Version,
		Slug:    resp.Slug,
	}

	data, err := json.Marshal(output)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal output")
	}

	return string(data), nil
}

// NewDashboardBuildTool creates a new DashboardBuildTool.
func NewDashboardBuildTool(ctx context.Context, configs Configs) (*DashboardBuildTool, error) {
	base, err := newBaseTool(ctx, configs)
	if err != nil {
		return nil, err
	}

	buildTool := &DashboardBuildTool{baseTool: base}
	t, err := utils.InferTool("grafana_dashboard_build", dashboardBuildDescription, buildTool.Invoke)
	if err != nil {
		return nil, err
	}
	buildTool.InvokableTool = t

	return buildTool, nil
}
