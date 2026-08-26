package grafana

import (
	"context"
	"net/http"
	"regexp"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/goccy/go-json"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/confirm"
)

var uidRegexp = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,40}$`)

func validateUID(uid string) error {
	if uid != "" && !uidRegexp.MatchString(uid) {
		return errors.Errorf("invalid dashboard UID %q: must match [a-zA-Z0-9_-]{1,40}", uid)
	}
	return nil
}

const dashboardWriteDescription = `
** General Purpose **
A single tool that creates, updates, or deletes a Grafana dashboard. The
required 'operation' param selects the action:

- create: POST a new dashboard model to /api/dashboards/db.
- update: POST an updated dashboard model to /api/dashboards/db (Grafana upsert
  semantics; include a 'uid' to target an existing dashboard).
- delete: DELETE /api/dashboards/uid/:uid.

** Targeted Updates with 'changes' **
For update operations, use the 'changes' parameter to specify only the fields
you want to modify. The tool will auto-fetch the existing dashboard by UID, deep-
merge your changes on top, and save the result. This eliminates the need to
provide the entire dashboard model for small targeted updates like modifying a
single template variable.

Example — update a template variable:
{
  "operation": "update",
  "uid": "3ce913db-b3ec-48a3-8867-8ef48cf2e337",
  "changes": {
    "templating": {
      "list": [
        {"name": "cluster", "current": {"value": "logmanagement2-rec"}, "includeAll": true, "allValue": ".+"}
      ]
    }
  }
}

** Safety **
This is a write tool. Always use dryRun=true first to preview the resolved
payload before saving/deleting. After reviewing, set confirmed=true to execute.

Do NOT include 'version' or 'id' in the dashboard model — the tool resolves the
current version automatically at execute time. Set overwrite=true only when the
user explicitly asks to force the save and discard any concurrent modifications;
otherwise leave it false.

** Dashboard Protection **
Dashboards matching the instance's protected blocklist (by UID, title prefix,
folder, or tag) cannot be modified or deleted.

** Output **
create/update returns a JSON object with the saved dashboard's UID, URL,
version, and status. delete returns a JSON object with the deleted dashboard's
title and message.
`

// DashboardWriteParams defines the parameters for creating, updating, or
// deleting a Grafana dashboard. The 'operation' field selects the action.
type DashboardWriteParams struct {
	Instance  string `json:"instance" validate:"required" jsonschema:"(required) The Grafana instance to connect to."`
	Operation string `json:"operation" validate:"required,oneof=create update delete" jsonschema:"(required) Operation: 'create', 'update', or 'delete'."`
	Dashboard string `json:"dashboard,omitempty" validate:"omitempty,max=1048576" jsonschema:"(optional, create/update) The full Grafana dashboard model as a JSON string. Must include 'title'. Include 'uid' to target an existing dashboard (update). Do NOT include 'version' or 'id' — they are resolved by the tool. For update, may be omitted when 'changes' is provided and 'uid' is set — the tool will auto-fetch the existing dashboard and apply changes on top."`
	Changes   string `json:"changes,omitempty" validate:"omitempty,max=1048576" jsonschema:"(optional, update only) A partial dashboard model as a JSON object containing only the fields to change. When provided for update, the tool deep-merges these changes into the existing dashboard (auto-fetched by UID, or into the provided 'dashboard' model). This avoids having to supply the full dashboard model for small targeted updates like modifying a single template variable. Ignored for create and delete."`
	UID       string `json:"uid,omitempty" validate:"omitempty,max=256" jsonschema:"(optional, delete/update by UID) For delete: the dashboard UID to delete. For update: may be provided here instead of inside the dashboard model. Ignored for create."`
	FolderUID string `json:"folderUID,omitempty" validate:"omitempty,max=256" jsonschema:"(optional, create/update) Folder UID to place the dashboard in."`
	Message   string `json:"message,omitempty" validate:"omitempty,max=1024" jsonschema:"(optional, create/update) Commit message for the version."`
	Overwrite bool   `json:"overwrite,omitempty" jsonschema:"(optional, create/update) Force the save, discarding any concurrent modification. Leave false unless the user explicitly asked to force it; the tool resolves the current version automatically."`
	DryRun    bool   `json:"dryRun,omitempty" jsonschema:"(optional) Preview without saving/deleting."`
	Confirmed bool   `json:"confirmed,omitempty" jsonschema:"(optional) Must be true to execute."`
}

// DashboardSaveOutput is the structured output for a dashboard save result
// (renamed from DashboardBuildOutput).
type DashboardSaveOutput struct {
	UID     string `json:"uid"`
	URL     string `json:"url"`
	Status  string `json:"status"`
	Version int    `json:"version"`
	Slug    string `json:"slug"`
}

// DashboardDeleteOutput is the structured output for a dashboard delete result.
type DashboardDeleteOutput struct {
	UID     string `json:"uid"`
	Title   string `json:"title"`
	Message string `json:"message"`
	Status  string `json:"status"`
}

// DashboardWriteTool is an eino tool for writing Grafana dashboards.
type DashboardWriteTool struct {
	*baseTool
	tool.InvokableTool
}

// Invoke creates, updates, or deletes a Grafana dashboard.
func (t *DashboardWriteTool) Invoke(ctx context.Context, params *DashboardWriteParams) (result string, err error) {
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

	switch params.Operation {
	case "create", "update":
		if params.Dashboard == "" && params.Changes == "" {
			return "", errors.Errorf("parameter 'dashboard' is required for operation %q; provide the full dashboard model JSON (including 'title') and retry", params.Operation)
		}
		if params.Changes != "" && params.Operation == "create" {
			return "", errors.New("parameter 'changes' is not supported for create; use 'dashboard' to provide the full dashboard model, or switch to update")
		}

		var changesModel map[string]any
		if params.Changes != "" {
			if err := json.Unmarshal([]byte(params.Changes), &changesModel); err != nil {
				return "", errors.Wrap(err, "parameter 'changes' is not valid JSON; fix the changes model and retry")
			}
		}

		var dashboardModel map[string]any
		if params.Dashboard != "" {
			if err := json.Unmarshal([]byte(params.Dashboard), &dashboardModel); err != nil {
				return "", errors.Wrap(err, "parameter 'dashboard' is not valid JSON; fix the dashboard model and retry")
			}
		}

		// Resolve the target UID.
		var uid string
		if params.UID != "" {
			uid = params.UID
		} else if dashboardModel != nil {
			uid, _ = dashboardModel["uid"].(string)
		}
		if err := validateUID(uid); err != nil {
			return "", err
		}

		var existingDR *dashboardResponse
		if uid != "" {
			dr, err := t.fetchDashboard(ctx, params.Instance, uid)
			if err != nil {
				if !isHTTPStatus(err, http.StatusNotFound) {
					return "", err
				}
			} else {
				existingDR = &dr
				if err := t.checkProtectedDashboard(params.Instance, uid, dr); err != nil {
					return "", err
				}
			}
		}

		// Auto-fetch the existing dashboard when changes are provided without a
		// full dashboard model. This enables targeted updates (e.g. modifying a
		// single template variable) without requiring the caller to supply the
		// entire dashboard JSON.
		if len(changesModel) > 0 && dashboardModel == nil {
			if uid == "" {
				return "", errors.New("parameter 'uid' is required when 'changes' is provided without 'dashboard'; set 'uid' to identify the target dashboard")
			}
			if existingDR == nil {
				return "", errors.Errorf("dashboard with UID %q not found; cannot apply changes to a nonexistent dashboard", uid)
			}
			dashboardModel = existingDR.Dashboard
		}

		// Deep-merge changes into the dashboard model.
		if len(changesModel) > 0 {
			deepMergeJSON(dashboardModel, changesModel)
		}

		// Inject params.UID into the model for update operations. Grafana's
		// save endpoint keys on the dashboard model's "uid" field, so without
		// this the POST would create a NEW dashboard instead of updating the
		// intended one.
		if params.Operation == "update" && uid != "" {
			dashboardModel["uid"] = uid
		}

		title, _ := dashboardModel["title"].(string)
		if title == "" {
			return "", errors.New("the resolved dashboard model is missing a 'title' field; ensure the dashboard or changes include a non-empty 'title'")
		}

		// Defense-in-depth: also evaluate the NEW dashboard model (and target
		// folder) against the blocklist. This prevents creating a new dashboard
		// — or renaming an existing one — so that it matches protected criteria.
		if err := t.checkProtectedModel(params.Instance, dashboardModel, params.FolderUID); err != nil {
			return "", err
		}

		// Always strip stale numeric id: Grafana upserts on uid; a stale id
		// inherited by copying another dashboard's JSON can retarget the write.
		// This must run regardless of overwrite mode.
		delete(dashboardModel, "id")

		if params.DryRun {
			preview := map[string]any{
				"dryRun":    true,
				"operation": params.Operation,
				"dashboard": dashboardModel,
				"folderUid": params.FolderUID,
				"overwrite": params.Overwrite,
			}
			if uid != "" && !params.Overwrite {
				preview["versionResolvedAtExecute"] = true
			}
			return marshalJSON(preview, "failed to marshal dry-run preview")
		}

		if uid != "" && !params.Overwrite {
			if existingDR == nil {
				// Dashboard did not exist at protection-check time, or was deleted
				// between protection check and now. Treat as fresh create: strip
				// any inherited version.
				delete(dashboardModel, "version")
			} else {
				// Inject the current version so Grafana's optimistic-concurrency
				// check passes. This overwrites any stale version in the model.
				dashboardModel["version"] = existingDR.Meta.Version
			}
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
			if errors.Is(err, ErrVersionMismatch) {
				submittedVersion := dashboardModel["version"]
				return "", errors.Wrapf(err,
					"dashboard %q was modified concurrently: the tool submitted version %v, "+
						"which Grafana rejected. Re-read the dashboard, re-apply your change on "+
						"top of the newer model, and retry. Set overwrite=true only to "+
						"deliberately discard the concurrent change",
					uid, submittedVersion)
			}
			return "", errors.Wrap(err, "failed to save dashboard")
		}

		var resp saveDashboardResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			return "", errors.Wrap(err, "failed to unmarshal save response")
		}

		return marshalJSON(DashboardSaveOutput{
			UID:     resp.UID,
			URL:     c.baseURL + resp.URL,
			Status:  resp.Status,
			Version: resp.Version,
			Slug:    resp.Slug,
		}, "failed to marshal output")

	case "delete":
		if params.UID == "" {
			return "", errors.New("parameter 'uid' is required for delete; provide the dashboard UID to delete and retry")
		}

		if err := validateUID(params.UID); err != nil {
			return "", err
		}

		// Fetch the existing dashboard first to (a) confirm it exists and
		// (b) blocklist-check it before deleting. A 404 is surfaced as a clear
		// not-found error rather than a silent "deleted" success.
		dr, err := t.fetchDashboard(ctx, params.Instance, params.UID)
		if err != nil {
			if isHTTPStatus(err, http.StatusNotFound) {
				return "", errors.Wrapf(err, "dashboard with UID %q not found", params.UID)
			}
			return "", err
		}
		if err := t.checkProtectedDashboard(params.Instance, params.UID, dr); err != nil {
			return "", err
		}

		if params.DryRun {
			return marshalJSON(map[string]any{
				"dryRun":    true,
				"operation": "delete",
				"uid":       params.UID,
			}, "failed to marshal dry-run preview")
		}

		body, err := c.DeleteDashboard(ctx, params.UID)
		if err != nil {
			return "", errors.Wrap(err, "failed to delete dashboard")
		}

		var resp deleteDashboardResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			return "", errors.Wrap(err, "failed to unmarshal delete response")
		}

		return marshalJSON(DashboardDeleteOutput{
			UID:     params.UID,
			Title:   resp.Title,
			Message: resp.Message,
			Status:  "success",
		}, "failed to marshal output")

	default:
		return "", errors.Errorf("unsupported operation %q", params.Operation)
	}
}

// NewDashboardWriteTool creates a new DashboardWriteTool.
func NewDashboardWriteTool(ctx context.Context, configs Configs) (*DashboardWriteTool, error) {
	base, err := newBaseTool(ctx, configs)
	if err != nil {
		return nil, err
	}

	writeTool := &DashboardWriteTool{baseTool: base}
	t, err := utils.InferTool(dashboardWriteToolName, dashboardWriteDescription, writeTool.Invoke)
	if err != nil {
		return nil, err
	}
	writeTool.InvokableTool = t

	return writeTool, nil
}
