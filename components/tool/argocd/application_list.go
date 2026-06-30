package argocd

import (
	"context"
	"fmt"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/goccy/go-json"
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
	Filter       string `json:"filter,omitempty" jsonschema:"(optional) Go RE2 regex applied on each application JSON output."`
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
	clients        map[string]*Client
	knownInstances []string

	tool.InvokableTool
}

func (t *ApplicationListTool) Invoke(ctx context.Context, params *ApplicationListParams) (result string, err error) {
	if err := validateParams(params); err != nil {
		return "", err
	}

	filter, err := CompileFilter(params.Filter)
	if err != nil {
		return "", errors.Wrap(err, "error when compile regex")
	}

	c, ok := t.clients[params.Instance]
	if !ok {
		return "", instanceNotFoundError(params.Instance, t.knownInstances)
	}

	resp, err := c.ListApplications(ctx, params.Selector, params.Project, params.AppNamespace)
	if err != nil {
		return "", err
	}

	outputs := make([]json.RawMessage, 0, len(resp.Items))
	for _, item := range resp.Items {
		output := ApplicationListOutput{
			Name:      item.Metadata.Name,
			Namespace: item.Metadata.Namespace,
			Project:   getProjectFromSpec(item.Spec),
		}
		if item.Status != nil {
			if item.Status.Health != nil {
				output.Health = item.Status.Health.Status
			}
			if item.Status.Sync != nil {
				output.SyncStatus = item.Status.Sync.Status
				output.Revision = item.Status.Sync.Revision
			}
		}

		outputJSON := json.RawMessage(MustMarshal(output))
		if !IsMatch(outputJSON, filter) {
			continue
		}
		outputs = append(outputs, outputJSON)
	}

	data, err := json.Marshal(outputs)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal output")
	}

	return string(data), nil
}

func getProjectFromSpec(spec map[string]any) string {
	if spec == nil {
		return "default"
	}
	if project, ok := spec["project"]; ok {
		if projectStr, ok := project.(string); ok {
			return projectStr
		}
	}
	return "default"
}

func NewApplicationListTool(ctx context.Context, configs Configs) (*ApplicationListTool, error) {
	clients, err := NewClients(configs)
	if err != nil {
		return nil, err
	}

	listTool := &ApplicationListTool{
		clients:        clients,
		knownInstances: configs.GetInstanceNames(),
	}

	t, err := utils.InferTool("argocd_application_list", fmt.Sprintf("%s\n%s", applicationListDescription, listOutputGuidance), listTool.Invoke)
	if err != nil {
		return nil, err
	}
	listTool.InvokableTool = t

	return listTool, nil
}
