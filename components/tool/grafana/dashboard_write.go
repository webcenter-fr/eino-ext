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
	Dashboard string `json:"dashboard,omitempty" validate:"omitempty,max=1048576" jsonschema:"(optional, create/update) The full Grafana dashboard model as a JSON string. Must include 'title'. Include 'uid' to target an existing dashboard (update). Do NOT include 'version' or 'id' — they are resolved by the tool. Ignored for delete."`
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
		if params.Dashboard == "" {
			return "", errors.Errorf("parameter 'dashboard' is required for operation %q; provide the full dashboard model JSON (including 'title') and retry", params.Operation)
		}

		var dashboardModel map[string]any
		if err := json.Unmarshal([]byte(params.Dashboard), &dashboardModel); err != nil {
			return "", errors.Wrap(err, "parameter 'dashboard' is not valid JSON; fix the dashboard model and retry")
		}

		title, _ := dashboardModel["title"].(string)
		if title == "" {
			return "", errors.New("parameter 'dashboard' is missing a 'title' field; add a non-empty 'title' to the dashboard model and retry")
		}

		// Determine the target uid: for update prefer params.UID, else the
		// model's uid; for create use the model's uid (may be empty).
		uid, _ := dashboardModel["uid"].(string)
		if params.Operation == "update" && params.UID != "" {
			uid = params.UID
			// The update target is specified via params.UID. Grafana's save
			// endpoint keys on the dashboard model's "uid" field, so inject it
			// into the model; otherwise the POST would create a NEW dashboard
			// instead of updating the intended one.
			dashboardModel["uid"] = params.UID
		}

		if err := validateUID(uid); err != nil {
			return "", err
		}

		var existingDR *dashboardResponse
		if uid != "" {
			dr, err := t.fetchDashboard(ctx, params.Instance, uid)
			if err != nil {
				// A 404 means the dashboard does not exist yet; treat as not protected.
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
