package argocd

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
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
}

type ApplicationDeleteTool struct {
	clients        map[string]*Client
	knownInstances []string

	tool.InvokableTool
}

func (t *ApplicationDeleteTool) Invoke(ctx context.Context, params *ApplicationDeleteParams) (result string, err error) {
	if err := validateParams(params); err != nil {
		return "", err
	}

	c, ok := t.clients[params.Instance]
	if !ok {
		return "", instanceNotFoundError(params.Instance, t.knownInstances)
	}

	if err := c.DeleteApplication(ctx, params.Name, params.AppNamespace, params.Project, params.Cascade); err != nil {
		return "", err
	}

	output := fmt.Sprintf(`{"message": "Application %q deleted successfully"}`, params.Name)
	return output, nil
}

func NewApplicationDeleteTool(ctx context.Context, configs Configs) (*ApplicationDeleteTool, error) {
	clients, err := NewClients(configs)
	if err != nil {
		return nil, err
	}

	deleteTool := &ApplicationDeleteTool{
		clients:        clients,
		knownInstances: configs.GetInstanceNames(),
	}

	t, err := utils.InferTool("argocd_application_delete", applicationDeleteDescription, deleteTool.Invoke)
	if err != nil {
		return nil, err
	}
	deleteTool.InvokableTool = t

	return deleteTool, nil
}
