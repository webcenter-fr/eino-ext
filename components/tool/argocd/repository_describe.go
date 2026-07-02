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

type RepositoryDescribeParams struct {
	Instance            string   `json:"instance" validate:"required" jsonschema:"(required) The ArgoCD instance to connect to."`
	Name                string   `json:"name" validate:"required" jsonschema:"(required) The repository name."`
	ExcludeFieldsOutput []string `json:"excludeFieldsOutput,omitempty" validate:"omitempty,dive,oneof=metadata spec connexionState" jsonschema:"(optional) Fields to exclude: 'metadata', 'spec', 'connexionState'."`
}

type RepositoryDescribeOutput struct {
	Metadata        *api.ObjectMeta               `json:"metadata,omitempty"`
	Spec            *RepositoryDescribeOutputSpec `json:"spec,omitempty"`
	ConnectionState *api.ConnectionState          `json:"connexionState,omitempty"`
}

type RepositoryDescribeOutputSpec struct {
	EnableLFS bool   `json:"enableLFS,omitempty"`
	EnableOCI bool   `json:"enableOCI,omitempty"`
	Insecure  bool   `json:"insecure,omitempty"`
	Project   string `json:"project,omitempty"`
	Type      string `json:"type,omitempty"`
	Repo      string `json:"repo,omitempty"`
}

type RepositoryDescribeTool struct {
	clients        map[string]api.API
	knownInstances []string

	tool.InvokableTool
}

func (t *RepositoryDescribeTool) Invoke(ctx context.Context, params *RepositoryDescribeParams) (result string, err error) {
	if err := validateParams(params); err != nil {
		return "", err
	}

	c, ok := t.clients[params.Instance]
	if !ok {
		return "", instanceNotFoundError(params.Instance, t.knownInstances)
	}

	repository, err := c.Repository().Get(params.Name, &api.RepositoryQueryOptions{})
	if err != nil {
		return "", err
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

	for _, excludeField := range params.ExcludeFieldsOutput {
		switch excludeField {
		case "metadata":
			output.Metadata = nil
		case "spec":
			output.Spec = nil
		case "connexionState":
			output.ConnectionState = nil
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

func NewRepositoryDescribeTool(ctx context.Context, configs Configs) (*RepositoryDescribeTool, error) {
	clients, err := BuildClients(configs)
	if err != nil {
		return nil, err
	}

	describeTool := &RepositoryDescribeTool{
		clients:        clients,
		knownInstances: configs.GetInstanceNames(),
	}

	t, err := utils.InferTool("argocd_repository_describe", fmt.Sprintf("%s\n%s", repositoryDescribeDescription, describeOutputGuidance), describeTool.Invoke)
	if err != nil {
		return nil, err
	}
	describeTool.InvokableTool = t

	return describeTool, nil
}
