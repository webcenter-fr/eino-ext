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

const repositoryDescribeDescription = `
** General Purpose **
It gets the details of a specific ArgoCD repository.

** Output **
It returns a JSON object representing the ArgoCD repository.
`

// RepositoryDescribeParams defines the parameters for describing an ArgoCD repository.
type RepositoryDescribeParams struct {
	Instance            string   `json:"instance" validate:"required" jsonschema:"(required) The ArgoCD instance to connect to."`
	Name                string   `json:"name" validate:"required" jsonschema:"(required) The repository name."`
	ExcludeFieldsOutput []string `json:"excludeFieldsOutput,omitempty" validate:"omitempty,dive,oneof=metadata spec connexionState" jsonschema:"(optional) Fields to exclude: 'metadata', 'spec', 'connexionState'."`
}

// RepositoryDescribeOutput is the structured output for a repository describe.
type RepositoryDescribeOutput struct {
	Metadata        *api.ObjectMeta               `json:"metadata,omitempty"`
	Spec            *RepositoryDescribeOutputSpec `json:"spec,omitempty"`
	ConnectionState *api.ConnectionState          `json:"connexionState,omitempty"`
}

// RepositoryDescribeOutputSpec holds the spec portion of a repository describe output.
type RepositoryDescribeOutputSpec struct {
	EnableLFS bool   `json:"enableLFS,omitempty"`
	EnableOCI bool   `json:"enableOCI,omitempty"`
	Insecure  bool   `json:"insecure,omitempty"`
	Project   string `json:"project,omitempty"`
	Type      string `json:"type,omitempty"`
	Repo      string `json:"repo,omitempty"`
}

// RepositoryDescribeTool is an eino tool for describing ArgoCD repositories.
type RepositoryDescribeTool struct {
	*baseTool
	tool.InvokableTool
}

// Invoke returns repository details as JSON.
func (t *RepositoryDescribeTool) Invoke(ctx context.Context, params *RepositoryDescribeParams) (result string, err error) {
	if err := validateParams(params); err != nil {
		return "", err
	}

	c, err := t.client(params.Instance)
	if err != nil {
		return "", err
	}

	repository, err := c.Repository().Get(params.Name, &api.RepositoryQueryOptions{})
	if err != nil {
		return "", errors.Wrap(err, "failed to get repository")
	}
	// Direct Name field shadows embedded ObjectMeta.Name, copy it over
	repository.ObjectMeta.Name = repository.Name

	output := &RepositoryDescribeOutput{
		Metadata: &repository.ObjectMeta,
		Spec: &RepositoryDescribeOutputSpec{
			EnableLFS: repository.EnableLFS,
			EnableOCI: repository.EnableOCI,
			Insecure:  repository.Insecure,
			Project:   repository.Project,
			Type:      repository.Type,
			Repo:      repository.Repo,
		},
		ConnectionState: &repository.ConnectionState,
	}

	if err := applyExcludes(params.ExcludeFieldsOutput, map[string]func(){
		"metadata":       func() { output.Metadata = nil },
		"spec":           func() { output.Spec = nil },
		"connexionState": func() { output.ConnectionState = nil },
	}); err != nil {
		return "", err
	}

	data, err := json.Marshal(output)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal output")
	}

	return string(data), nil
}

// NewRepositoryDescribeTool creates a new RepositoryDescribeTool.
func NewRepositoryDescribeTool(ctx context.Context, configs Configs) (*RepositoryDescribeTool, error) {
	base, err := newBaseTool(ctx, configs)
	if err != nil {
		return nil, err
	}

	describeTool := &RepositoryDescribeTool{baseTool: base}
	t, err := utils.InferTool("argocd_repository_describe", fmt.Sprintf("%s\n%s", repositoryDescribeDescription, describeOutputGuidance), describeTool.Invoke)
	if err != nil {
		return nil, err
	}
	describeTool.InvokableTool = t

	return describeTool, nil
}
