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

const projectDescribeDescription = `
** General Purpose **
It gets the details of a specific ArgoCD project.

** Output **
It returns a JSON object representing the ArgoCD project.
`

type ProjectDescribeParams struct {
	Instance            string   `json:"instance" validate:"required" jsonschema:"(required) The ArgoCD instance to connect to."`
	Name                string   `json:"name" validate:"required" jsonschema:"(required) The project name."`
	ExcludeFieldsOutput []string `json:"excludeFieldsOutput,omitempty" validate:"omitempty,dive,oneof=metadata spec status" jsonschema:"(optional) Fields to exclude: 'metadata', 'spec', 'status'."`
}

type ProjectDescribeOutput struct {
	Metadata *api.ObjectMeta    `json:"metadata,omitempty"`
	Spec     *api.ProjectSpec   `json:"spec,omitempty"`
	Status   *api.ProjectStatus `json:"status,omitempty"`
}

type ProjectDescribeTool struct {
	*baseTool
	tool.InvokableTool
}

func (t *ProjectDescribeTool) Invoke(ctx context.Context, params *ProjectDescribeParams) (result string, err error) {
	if err := validateParams(params); err != nil {
		return "", err
	}

	c, err := t.client(params.Instance)
	if err != nil {
		return "", err
	}

	project, err := c.Project().Get(params.Name)
	if err != nil {
		return "", errors.Wrap(err, "failed to get project")
	}

	output := &ProjectDescribeOutput{
		Metadata: &project.ObjectMeta,
		Spec:     &project.Spec,
		Status:   &project.Status,
	}

	if err := applyExcludes(params.ExcludeFieldsOutput, map[string]func(){
		"metadata": func() { output.Metadata = nil },
		"spec":     func() { output.Spec = nil },
		"status":   func() { output.Status = nil },
	}); err != nil {
		return "", err
	}

	data, err := json.Marshal(output)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal output")
	}

	return string(data), nil
}

func NewProjectDescribeTool(ctx context.Context, configs Configs) (*ProjectDescribeTool, error) {
	base, err := newBaseTool(ctx, configs)
	if err != nil {
		return nil, err
	}

	describeTool := &ProjectDescribeTool{baseTool: base}
	t, err := utils.InferTool("argocd_project_describe", fmt.Sprintf("%s\n%s", projectDescribeDescription, describeOutputGuidance), describeTool.Invoke)
	if err != nil {
		return nil, err
	}
	describeTool.InvokableTool = t

	return describeTool, nil
}
