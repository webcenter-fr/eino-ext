package github

import (
	"context"
	"fmt"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/google/go-github/v71/github"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/filter"
)

const orgRepoListDescription = `
** General Purpose **
It lists repositories in a GitHub organization.

** Output **
It returns a JSON array of objects, where each object represents a repository with the following fields:
- name: the repository name.
- fullName: the full repository name (org/repo).
- description: the repository description.
- language: the primary language.
- private: whether the repository is private.
- stars: star count.
- openIssues: open issues count.
- defaultBranch: the default branch name.
- htmlURL: the repository URL on GitHub.
`

type OrgRepoListParams struct {
	Instance string `json:"instance" validate:"required" jsonschema:"(required) The GitHub instance to connect to."`
	Org      string `json:"org" validate:"required" jsonschema:"(required) Organization name."`
	Type     string `json:"type,omitempty" jsonschema:"(optional) Repository type: all, public, private, forks, sources, member. Defaults to all."`
	PerPage  int    `json:"perPage,omitempty" jsonschema:"(optional) Results per page. Defaults to 30, max 100."`
	Filter   string `json:"filter,omitempty" jsonschema:"(optional) Go RE2 regex applied on each repository JSON output."`
}

type OrgRepoListOutput struct {
	Name          string `json:"name"`
	FullName      string `json:"fullName"`
	Description   string `json:"description"`
	Language      string `json:"language"`
	Private       bool   `json:"private"`
	Stars         int    `json:"stars"`
	OpenIssues    int    `json:"openIssues"`
	DefaultBranch string `json:"defaultBranch"`
	HTMLURL       string `json:"htmlURL"`
}

type OrgRepoListTool struct {
	*baseTool
	tool.InvokableTool
}

func (t *OrgRepoListTool) Invoke(ctx context.Context, params *OrgRepoListParams) (result string, err error) {
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

	repoType := params.Type
	if repoType == "" {
		repoType = "all"
	}

	perPage := params.PerPage
	if perPage <= 0 || perPage > 100 {
		perPage = 30
	}

	opts := &github.RepositoryListByOrgOptions{
		Type: repoType,
		ListOptions: github.ListOptions{
			PerPage: perPage,
		},
	}

	repos, _, err := c.Repositories.ListByOrg(ctx, params.Org, opts)
	if err != nil {
		return "", errors.Wrap(err, "failed to list organization repositories")
	}

	return filterMapMarshal(repos, re, func(item *github.Repository) OrgRepoListOutput {
		return OrgRepoListOutput{
			Name:          item.GetName(),
			FullName:      item.GetFullName(),
			Description:   item.GetDescription(),
			Language:      item.GetLanguage(),
			Private:       item.GetPrivate(),
			Stars:         item.GetStargazersCount(),
			OpenIssues:    item.GetOpenIssuesCount(),
			DefaultBranch: item.GetDefaultBranch(),
			HTMLURL:       item.GetHTMLURL(),
		}
	})
}

func NewOrgRepoListTool(ctx context.Context, configs Configs) (*OrgRepoListTool, error) {
	base, err := newBaseTool(ctx, configs)
	if err != nil {
		return nil, err
	}
	return newOrgRepoListTool(ctx, base)
}

func newOrgRepoListTool(ctx context.Context, base *baseTool) (*OrgRepoListTool, error) {
	listTool := &OrgRepoListTool{baseTool: base}
	t, err := utils.InferTool("github_org_repo_list", fmt.Sprintf("%s\n%s", orgRepoListDescription, listOutputGuidance), listTool.Invoke)
	if err != nil {
		return nil, err
	}
	listTool.InvokableTool = t

	return listTool, nil
}
