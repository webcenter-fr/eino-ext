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

const repoSearchDescription = `
** General Purpose **
It searches for GitHub repositories using a query.

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

// RepoSearchParams defines the parameters for searching GitHub repositories.
type RepoSearchParams struct {
	Instance string `json:"instance" validate:"required" jsonschema:"(required) The GitHub instance to connect to."`
	Query    string `json:"query" validate:"required" jsonschema:"(required) Search query (GitHub search syntax)."`
	PerPage  int    `json:"perPage,omitempty" jsonschema:"(optional) Results per page. Defaults to 30, max 100."`
	MaxPages int    `json:"maxPages,omitempty" jsonschema:"(optional) Maximum number of pages to fetch. Defaults to 0, which loops over all pages."`
	Filter   string `json:"filter,omitempty" jsonschema:"(optional) Filter to keep only matching repositories. Accepts either: a JSON object selector {\"<dot.path>\":\"<value>\", ...} — all keys must match (AND); use \"[]\" to match any array element; a value that is an array means IN; matching is type-coerced and case-insensitive (500 matches \"500\", true matches \"true\"); a bare key (no dots) matches any field with that name at any depth; or a Go RE2 regex on each repository JSON output (RE2 does NOT support lookahead/lookbehind/backreferences). Invalid regex or selector JSON returns an error."`
}

// RepoSearchOutput is the structured output for a repo search.
type RepoSearchOutput struct {
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

// RepoSearchTool is an eino tool for searching GitHub repositories.
type RepoSearchTool struct {
	*baseTool
	tool.InvokableTool
}

// Invoke searches GitHub repositories and returns results as JSON.
func (t *RepoSearchTool) Invoke(ctx context.Context, params *RepoSearchParams) (result string, err error) {
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

	perPage := params.PerPage
	if perPage <= 0 || perPage > 100 {
		perPage = 30
	}

	opts := &github.SearchOptions{
		ListOptions: github.ListOptions{
			PerPage: perPage,
		},
	}

	repos, err := paginateList(func(page int) ([]*github.Repository, *github.Response, error) {
		opts.Page = page
		result_, resp, err := c.Search.Repositories(ctx, params.Query, opts)
		if err != nil {
			return nil, nil, err
		}
		return result_.Repositories, resp, nil
	}, params.MaxPages)
	if err != nil {
		return "", errors.Wrap(err, "failed to search repositories")
	}

	return filterMapMarshal(repos, m, func(item *github.Repository) RepoSearchOutput {
		return RepoSearchOutput{
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

// NewRepoSearchTool creates a new RepoSearchTool.
func NewRepoSearchTool(ctx context.Context, configs Configs) (*RepoSearchTool, error) {
	base, err := newBaseTool(ctx, configs)
	if err != nil {
		return nil, err
	}
	return newRepoSearchTool(ctx, base)
}

func newRepoSearchTool(ctx context.Context, base *baseTool) (*RepoSearchTool, error) {
	searchTool := &RepoSearchTool{baseTool: base}
	t, err := utils.InferTool("github_repo_search", fmt.Sprintf("%s\n%s", repoSearchDescription, listOutputGuidance), searchTool.Invoke, utils.WithSchemaModifier(base.instanceSchemaModifier()))
	if err != nil {
		return nil, err
	}
	searchTool.InvokableTool = t

	return searchTool, nil
}
