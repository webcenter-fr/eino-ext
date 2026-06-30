package argocd

import (
	"context"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/goccy/go-json"
)

const applicationSyncDescription = `
** General Purpose **
It triggers a sync for an ArgoCD application.

** Output **
It returns the application status after sync initiation.
`

type ApplicationSyncParams struct {
	Instance     string `json:"instance" validate:"required" jsonschema:"(required) The ArgoCD instance to connect to."`
	Name         string `json:"name" validate:"required" jsonschema:"(required) The application name."`
	AppNamespace string `json:"appNamespace,omitempty" jsonschema:"(optional) Application namespace."`
	Project      string `json:"project,omitempty" jsonschema:"(optional) Application project."`
	Revision     string `json:"revision,omitempty" jsonschema:"(optional) Target revision to sync to."`
	DryRun       bool   `json:"dryRun,omitempty" jsonschema:"(optional) Simulate sync without applying changes."`
	Prune        bool   `json:"prune,omitempty" jsonschema:"(optional) Delete resources no longer in git."`
}

type ApplicationSyncTool struct {
	clients        map[string]*Client
	knownInstances []string

	tool.InvokableTool
}

func (t *ApplicationSyncTool) Invoke(ctx context.Context, params *ApplicationSyncParams) (result string, err error) {
	if err := validateParams(params); err != nil {
		return "", err
	}

	c, ok := t.clients[params.Instance]
	if !ok {
		return "", instanceNotFoundError(params.Instance, t.knownInstances)
	}

	req := &SyncRequest{
		Revision: params.Revision,
		Prune:    params.Prune,
		DryRun:   params.DryRun,
	}

	app, err := c.SyncApplication(ctx, params.Name, params.AppNamespace, params.Project, req)
	if err != nil {
		return "", err
	}

	data, err := json.Marshal(app)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal output")
	}

	return string(data), nil
}

func NewApplicationSyncTool(ctx context.Context, configs Configs) (*ApplicationSyncTool, error) {
	clients, err := NewClients(configs)
	if err != nil {
		return nil, err
	}

	syncTool := &ApplicationSyncTool{
		clients:        clients,
		knownInstances: configs.GetInstanceNames(),
	}

	t, err := utils.InferTool("argocd_application_sync", applicationSyncDescription, syncTool.Invoke)
	if err != nil {
		return nil, err
	}
	syncTool.InvokableTool = t

	return syncTool, nil
}
