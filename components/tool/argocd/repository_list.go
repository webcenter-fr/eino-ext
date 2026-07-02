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

const repositoryListDescription = `
** General Purpose **
It lists all ArgoCD repositories accessible to the configured instance.

** Output **
It returns a JSON array of objects, where each object represents a repository with the following fields:
- name: the name of the repository.
- type: the type of the repository.
- url: the URL of the repository.
- status: the status of the repository.
`

type RepositoryListParams struct {
	Instance string `json:"instance" validate:"required" jsonschema:"(required) The ArgoCD instance to connect to."`
	Filter   string `json:"filter,omitempty" jsonschema:"(optional) Go RE2 regex on each repository JSON."`
}

type RepositoryListOutput struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Type   string `json:"type"`
	Url    string `json:"url"`
}

type RepositoryListTool struct {
	clients        map[string]api.API
	knownInstances []string

	tool.InvokableTool
}

func (t *RepositoryListTool) Invoke(ctx context.Context, params *RepositoryListParams) (result string, err error) {
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

	resp, err := c.Repository().List(&api.RepositoryQueryOptions{})
	if err != nil {
		return "", errors.Wrap(err, "failed to list repositories")
	}

	outputs := make([]json.RawMessage, 0, len(resp.Items))
	for _, item := range resp.Items {
		output := RepositoryListOutput{
			Name:   item.Name,
			Status: item.ConnectionState.Status,
			Type:   item.Type,
			Url:    item.Repo,
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

func NewRepositoryListTool(ctx context.Context, configs Configs) (*RepositoryListTool, error) {
	clients, err := BuildClients(configs)
	if err != nil {
		return nil, err
	}

	listTool := &RepositoryListTool{
		clients:        clients,
		knownInstances: configs.GetInstanceNames(),
	}

	t, err := utils.InferTool("argocd_repository_list", fmt.Sprintf("%s\n%s", repositoryListDescription, listOutputGuidance), listTool.Invoke)
	if err != nil {
		return nil, err
	}
	listTool.InvokableTool = t

	return listTool, nil
}
