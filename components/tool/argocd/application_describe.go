package argocd

import (
	"context"
	"fmt"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/disaster37/goargocdclient/api"
	"github.com/goccy/go-json"
)

const applicationDescribeDescription = `
** General Purpose **
It gets the details of a specific ArgoCD application.

** Output **
It returns a JSON object representing the ArgoCD application.
`

type ApplicationDescribeParams struct {
	Instance            string   `json:"instance" validate:"required" jsonschema:"(required) The ArgoCD instance to connect to."`
	Name                string   `json:"name" validate:"required" jsonschema:"(required) The application name."`
	AppNamespace        string   `json:"appNamespace,omitempty" jsonschema:"(optional) Application namespace."`
	Project             string   `json:"project,omitempty" jsonschema:"(optional) Application project."`
	ExcludeFieldsOutput []string `json:"excludeFieldsOutput,omitempty" validate:"omitempty,dive,oneof=metadata spec status" jsonschema:"(optional) Fields to exclude: 'metadata', 'spec', 'status'."`
}

type ApplicationDescribeOutput struct {
	Metadata *api.ObjectMeta        `json:"metadata,omitempty"`
	Spec     *api.ApplicationSpec   `json:"spec,omitempty"`
	Status   *api.ApplicationStatus `json:"status,omitempty"`
}

type ApplicationDescribeTool struct {
	clients        map[string]api.API
	knownInstances []string

	tool.InvokableTool
}

func (t *ApplicationDescribeTool) Invoke(ctx context.Context, params *ApplicationDescribeParams) (result string, err error) {
	if err := validateParams(params); err != nil {
		return "", err
	}

	c, ok := t.clients[params.Instance]
	if !ok {
		return "", instanceNotFoundError(params.Instance, t.knownInstances)
	}

	app, err := c.Application().Get(params.Name, &api.ApplicationGetOptions{
		AppNamespace: params.AppNamespace,
		Project:      []string{params.Project},
	})
	if err != nil {
		return "", errors.Wrap(err, "failed to get application")
	}

	output := &ApplicationDescribeOutput{
		Metadata: &app.ObjectMeta,
		Spec:     &app.Spec,
		Status:   &app.Status,
	}

	for _, excludeField := range params.ExcludeFieldsOutput {
		switch excludeField {
		case "metadata":
			output.Metadata = nil
		case "spec":
			output.Spec = nil
		case "status":
			output.Status = nil
		default:
			return "", errors.Errorf("invalid exclude field: %s", excludeField)
		}
	}

	data, err := json.Marshal(output)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal output")
	}

	return string(data), nil
}

func NewApplicationDescribeTool(ctx context.Context, configs Configs) (*ApplicationDescribeTool, error) {
	clients, err := BuildClients(configs)
	if err != nil {
		return nil, err
	}

	describeTool := &ApplicationDescribeTool{
		clients:        clients,
		knownInstances: configs.GetInstanceNames(),
	}

	t, err := utils.InferTool("argocd_application_describe", fmt.Sprintf("%s\n%s", applicationDescribeDescription, describeOutputGuidance), describeTool.Invoke)
	if err != nil {
		return nil, err
	}
	describeTool.InvokableTool = t

	return describeTool, nil
}
