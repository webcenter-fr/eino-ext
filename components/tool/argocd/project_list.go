package argocd

import (
	"context"
	"fmt"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/goccy/go-json"
)

const projectListDescription = `
** General Purpose **
It lists all ArgoCD projects accessible to the configured instance.

** Output **
It returns a JSON array of objects, where each object represents a project with the following fields:
- name: the name of the project.
- description: the description of the project.
`

type ProjectListParams struct {
	Instance string `json:"instance" validate:"required" jsonschema:"(required) The ArgoCD instance to connect to."`
	Filter   string `json:"filter,omitempty" jsonschema:"(optional) Go RE2 regex on each project JSON."`
}

type ProjectListOutput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type ProjectListTool struct {
	clients        map[string]*Client
	knownInstances []string

	tool.InvokableTool
}

func (t *ProjectListTool) Invoke(ctx context.Context, params *ProjectListParams) (result string, err error) {
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

	resp, err := c.ListProjects(ctx, "")
	if err != nil {
		return "", err
	}

	outputs := make([]json.RawMessage, 0, len(resp.Items))
	for _, item := range resp.Items {
		output := ProjectListOutput{
			Name: item.Metadata.Name,
		}
		if desc, ok := item.Spec["description"]; ok {
			if descStr, ok := desc.(string); ok {
				output.Description = descStr
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

func NewProjectListTool(ctx context.Context, configs Configs) (*ProjectListTool, error) {
	clients, err := NewClients(configs)
	if err != nil {
		return nil, err
	}

	listTool := &ProjectListTool{
		clients:        clients,
		knownInstances: configs.GetInstanceNames(),
	}

	t, err := utils.InferTool("argocd_project_list", fmt.Sprintf("%s\n%s", projectListDescription, listOutputGuidance), listTool.Invoke)
	if err != nil {
		return nil, err
	}
	listTool.InvokableTool = t

	return listTool, nil
}
