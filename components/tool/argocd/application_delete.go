package argocd

import (
	"context"
	"fmt"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/disaster37/goargocdclient/api"
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
	base, err := newBaseTool(configs)
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
