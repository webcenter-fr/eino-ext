package argocd

import (
	"context"
	"fmt"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/goccy/go-json"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/filter"
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
	*baseTool
	tool.InvokableTool
}

func (t *ProjectListTool) Invoke(ctx context.Context, params *ProjectListParams) (result string, err error) {
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

	resp, err := c.Project().List()
	if err != nil {
		return "", errors.Wrap(err, "failed to list projects")
	}

	outputs := make([]json.RawMessage, 0, len(resp.Items))
	for _, item := range resp.Items {
		output := ProjectListOutput{
			Name:        item.Name,
			Description: item.Spec.Description,
		}

		outputJSON := json.RawMessage(MustMarshal(output))
		if !filter.Match(outputJSON, re) {
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
	base, err := newBaseTool(configs)
	if err != nil {
		return nil, err
	}

	listTool := &ProjectListTool{baseTool: base}
	t, err := utils.InferTool("argocd_project_list", fmt.Sprintf("%s\n%s", projectListDescription, listOutputGuidance), listTool.Invoke)
	if err != nil {
		return nil, err
	}
	listTool.InvokableTool = t

	return listTool, nil
}
