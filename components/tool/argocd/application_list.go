package argocd

import (
	"context"
	"fmt"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/disaster37/goargocdclient/api"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/filter"
)

const applicationListDescription = `
** General Purpose **
It lists all the ArgoCD applications accessible to the configured instance.

** Output **
It returns a JSON array of objects, where each object represents an application with the following fields:
- name: the name of the application.
- namespace: the namespace of the application.
- project: the project the application belongs to.
- health: the health status of the application (Healthy, Degraded, Progressing, etc.).
- syncStatus: the sync status of the application (Synced, OutOfSync, Unknown).
- revision: the revision the app is currently synced to.
`

type ApplicationListParams struct {
	Instance     string `json:"instance" validate:"required" jsonschema:"(required) The ArgoCD instance to connect to."`
	Project      string `json:"project,omitempty" jsonschema:"(optional) Filter by project name."`
	Selector     string `json:"selector,omitempty" jsonschema:"(optional) Label selector (e.g. 'app=nginx,env=prod')."`
	AppNamespace string `json:"appNamespace,omitempty" jsonschema:"(optional) Application namespace filter."`
	Filter       string `json:"filter,omitempty" jsonschema:"(optional) Go RE2 regex applied on each application JSON output. RE2 does NOT support lookahead (?=...)/(?!...), lookbehind (?<=...)/(?<!...), or backreferences — such patterns return an error. Invalid regex returns an error."`
}

type ApplicationListOutput struct {
	Name       string `json:"name"`
	Namespace  string `json:"namespace"`
	Project    string `json:"project"`
	Health     string `json:"health"`
	SyncStatus string `json:"syncStatus"`
	Revision   string `json:"revision"`
}

type ApplicationListTool struct {
	*baseTool
	tool.InvokableTool
}

func (t *ApplicationListTool) Invoke(ctx context.Context, params *ApplicationListParams) (result string, err error) {
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

	resp, err := c.Application().List(&api.ApplicationListOptions{
		Selector:     params.Selector,
		Project:      projectFilter(params.Project),
		AppNamespace: params.AppNamespace,
	})
	if err != nil {
		return "", errors.Wrap(err, "failed to list applications")
	}

	return filterMapMarshal(resp.Items, re, func(item *api.ApplicationModel) ApplicationListOutput {
		return ApplicationListOutput{
			Name:       item.Name,
			Namespace:  item.Namespace,
			Project:    item.Spec.Project,
			Health:     string(item.Status.Health.Status),
			SyncStatus: string(item.Status.Sync.Status),
			Revision:   item.Status.Sync.Revision,
		}
	})
}

func NewApplicationListTool(ctx context.Context, configs Configs) (*ApplicationListTool, error) {
	base, err := newBaseTool(ctx, configs)
	if err != nil {
		return nil, err
	}

	listTool := &ApplicationListTool{baseTool: base}
	t, err := utils.InferTool("argocd_application_list", fmt.Sprintf("%s\n%s", applicationListDescription, listOutputGuidance), listTool.Invoke)
	if err != nil {
		return nil, err
	}
	listTool.InvokableTool = t

	return listTool, nil
}
