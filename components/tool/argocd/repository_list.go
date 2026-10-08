package argocd

import (
	"context"
	"fmt"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/disaster37/goargocdclient/api"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/filter"
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

// RepositoryListParams defines the parameters for listing ArgoCD repositories.
type RepositoryListParams struct {
	Instance string `json:"instance" validate:"required" jsonschema:"(required) The ArgoCD instance to connect to."`
	Filter   string `json:"filter,omitempty" jsonschema:"(optional) Filter to keep only matching repositories. Accepts either: a JSON object selector {\"<dot.path>\":\"<value>\", ...} — all keys must match (AND); use \"[]\" to match any array element; a value that is an array means IN; matching is type-coerced and case-insensitive (500 matches \"500\", true matches \"true\"); a bare key (no dots) matches any field with that name at any depth; or a Go RE2 regex on each repository JSON (RE2 does NOT support lookahead/lookbehind/backreferences). Invalid regex or selector JSON returns an error."`
}

// RepositoryListOutput is the structured output for a repository list.
type RepositoryListOutput struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Type   string `json:"type"`
	URL    string `json:"url"`
}

// RepositoryListTool is an eino tool for listing ArgoCD repositories.
type RepositoryListTool struct {
	*baseTool
	tool.InvokableTool
}

// Invoke returns matching repositories as JSON.
func (t *RepositoryListTool) Invoke(ctx context.Context, params *RepositoryListParams) (result string, err error) {
	if err := validateParams(params); err != nil {
		return "", err
	}

	m, err := filter.CompileMatcher(params.Filter)
	if err != nil {
		return "", errors.Wrap(err, "error when compiling filter")
	}

	c, err := t.client(params.Instance)
	if err != nil {
		return "", err
	}

	resp, err := c.Repository().List(&api.RepositoryQueryOptions{})
	if err != nil {
		return "", errors.Wrap(err, "failed to list repositories")
	}

	return filterMapMarshal(resp.Items, m, func(item *api.RepositoryModel) RepositoryListOutput {
		return RepositoryListOutput{
			Name:   item.Name,
			Status: item.ConnectionState.Status,
			Type:   item.Type,
			URL:    item.Repo,
		}
	})
}

// NewRepositoryListTool creates a new RepositoryListTool.
func NewRepositoryListTool(ctx context.Context, configs Configs) (*RepositoryListTool, error) {
	base, err := newBaseTool(ctx, configs)
	if err != nil {
		return nil, err
	}

	listTool := &RepositoryListTool{baseTool: base}
	t, err := utils.InferTool("argocd_repository_list", fmt.Sprintf("%s\n%s", repositoryListDescription, listOutputGuidance), listTool.Invoke)
	if err != nil {
		return nil, err
	}
	listTool.InvokableTool = t

	return listTool, nil
}
