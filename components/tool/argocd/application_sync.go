package argocd

import (
	"context"
	"fmt"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/disaster37/goargocdclient/api"
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
	Prune        bool   `json:"prune,omitempty" jsonschema:"(optional) Delete resources no longer in git. Be careful with this option, it can delete resources in your cluster."`
}

type ApplicationSyncTool struct {
	clients        map[string]api.API
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

	if err := c.Application().Sync(params.Name, &api.SyncOptions{
		Revision:     params.Revision,
		Prune:        params.Prune,
		DryRun:       params.DryRun,
		Project:      params.Project,
		AppNamespace: params.AppNamespace,
	}); err != nil {
		return "", errors.Wrap(err, "application sync failed")
	}

	return fmt.Sprintf(`{"message": "Application %q sync successfully"}`, params.Name), nil
}

func NewApplicationSyncTool(ctx context.Context, configs Configs) (*ApplicationSyncTool, error) {
	clients, err := BuildClients(configs)
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
