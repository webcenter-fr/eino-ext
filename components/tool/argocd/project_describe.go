package argocd

import (
	"context"
	"fmt"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/goccy/go-json"
)

const projectDescribeDescription = `
** General Purpose **
It gets the details of a specific ArgoCD project.

** Output **
It returns a JSON object representing the ArgoCD project.
`

type ProjectDescribeParams struct {
	Instance            string   `json:"instance" validate:"required" jsonschema:"(required) The ArgoCD instance to connect to."`
	Name                string   `json:"name" validate:"required" jsonschema:"(required) The project name."`
	ExcludeFieldsOutput []string `json:"excludeFieldsOutput,omitempty" validate:"omitempty,dive,oneof=metadata spec" jsonschema:"(optional) Fields to exclude: 'metadata', 'spec'."`
}

type ProjectDescribeOutput struct {
	Metadata *ObjectMeta    `json:"metadata,omitempty"`
	Spec     map[string]any `json:"spec,omitempty"`
}

type ProjectDescribeTool struct {
	clients        map[string]*Client
	knownInstances []string

	tool.InvokableTool
}

func (t *ProjectDescribeTool) Invoke(ctx context.Context, params *ProjectDescribeParams) (result string, err error) {
	if err := validateParams(params); err != nil {
		return "", err
	}

	c, ok := t.clients[params.Instance]
	if !ok {
		return "", instanceNotFoundError(params.Instance, t.knownInstances)
	}

	project, err := c.GetProject(ctx, params.Name)
	if err != nil {
		return "", err
	}

	output := &ProjectDescribeOutput{
		Metadata: &project.Metadata,
		Spec:     project.Spec,
	}

	for _, excludeField := range params.ExcludeFieldsOutput {
		switch excludeField {
		case "metadata":
			output.Metadata = nil
		case "spec":
			output.Spec = nil
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

func NewProjectDescribeTool(ctx context.Context, configs Configs) (*ProjectDescribeTool, error) {
	clients, err := NewClients(configs)
	if err != nil {
		return nil, err
	}

	describeTool := &ProjectDescribeTool{
		clients:        clients,
		knownInstances: configs.GetInstanceNames(),
	}

	t, err := utils.InferTool("argocd_project_describe", fmt.Sprintf("%s\n%s", projectDescribeDescription, describeOutputGuidance), describeTool.Invoke)
	if err != nil {
		return nil, err
	}
	describeTool.InvokableTool = t

	return describeTool, nil
}
