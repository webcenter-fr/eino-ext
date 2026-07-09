package argocd

import (
	"context"
	"fmt"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/disaster37/goargocdclient/api"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/confirm"
	"github.com/goccy/go-json"
)

const applicationDeleteDescription = `
** General Purpose **
It deletes an ArgoCD application.

** Output **
It returns a confirmation message.
`

type ApplicationDeleteParams struct {
	Instance     string `json:"instance" validate:"required" jsonschema:"(required) The ArgoCD instance to connect to."`
	Name         string `json:"name" validate:"required" jsonschema:"(required) The application name."`
	AppNamespace string `json:"appNamespace,omitempty" jsonschema:"(optional) Application namespace."`
	Project      string `json:"project,omitempty" jsonschema:"(optional) Application project."`
	Cascade      *bool  `json:"cascade,omitempty" jsonschema:"(optional) Also delete application resources. Defaults to true."`
	DryRun       bool   `json:"dryRun,omitempty" jsonschema:"(optional) If true, fetch the application and return what would be deleted without actually deleting it. Show the result to the user and ask for confirmation."`
	Confirmed    bool   `json:"confirmed,omitempty" jsonschema:"(optional) Must be true to actually execute the deletion. Set this after the user has approved the dry-run result."`
}

type ApplicationDeleteTool struct {
	*baseTool
	tool.InvokableTool
}

func (t *ApplicationDeleteTool) Invoke(ctx context.Context, params *ApplicationDeleteParams) (result string, err error) {
	if err := validateParams(params); err != nil {
		return "", err
	}

	c, err := t.client(params.Instance)
	if err != nil {
		return "", err
	}

	if params.DryRun {
		// Dry-run: fetch the application and return what would be deleted.
		var projectFilter []string
		if params.Project != "" {
			projectFilter = []string{params.Project}
		}
		app, fetchErr := c.Application().Get(params.Name, &api.ApplicationGetOptions{
			AppNamespace: params.AppNamespace,
			Project:      projectFilter,
		})
		if fetchErr != nil {
			return "", errors.Wrap(fetchErr, "failed to fetch application for dry-run")
		}
		data, marshalErr := json.Marshal(app)
		if marshalErr != nil {
			return "", errors.Wrap(marshalErr, "failed to marshal output")
		}
		return fmt.Sprintf(`{"dryRun": true, "wouldDelete": %s}`, string(data)), nil
	}

	if err := confirm.RequireConfirmation(params.DryRun, params.Confirmed); err != nil {
		return "", err
	}

	if err := c.Application().Delete(params.Name, &api.ApplicationDeleteOptions{
		AppNamespace: params.AppNamespace,
		Project:      params.Project,
		Cascade:      params.Cascade,
	}); err != nil {
		return "", errors.Wrap(err, "failed to delete application")
	}

	return fmt.Sprintf(`{"message": "Application %q deleted successfully"}`, params.Name), nil
}

func NewApplicationDeleteTool(ctx context.Context, configs Configs) (*ApplicationDeleteTool, error) {
	base, err := newBaseTool(ctx, configs)
	if err != nil {
		return nil, err
	}

	deleteTool := &ApplicationDeleteTool{baseTool: base}
	t, err := utils.InferTool("argocd_application_delete", applicationDeleteDescription, deleteTool.Invoke)
	if err != nil {
		return nil, err
	}
	deleteTool.InvokableTool = t

	return deleteTool, nil
}
