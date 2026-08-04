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

const prListDescription = `
** General Purpose **
It lists pull requests in a GitHub repository.

** Output **
It returns a JSON array of objects, where each object represents a pull request with the following fields:
- number: the PR number.
- title: the PR title.
- state: the PR state (open/closed).
- author: the PR author login.
- baseBranch: the base branch.
- headBranch: the head branch.
- draft: whether the PR is a draft.
- createdAt: when the PR was created.
- updatedAt: when the PR was last updated.
- htmlURL: the PR URL on GitHub.
`

type PRListParams struct {
	Instance string `json:"instance" validate:"required" jsonschema:"(required) The GitHub instance to connect to."`
	Owner    string `json:"owner" validate:"required" jsonschema:"(required) Repository owner."`
	Repo     string `json:"repo" validate:"required" jsonschema:"(required) Repository name."`
	State    string `json:"state,omitempty" jsonschema:"(optional) Filter by state: open, closed, all. Defaults to open."`
	Head     string `json:"head,omitempty" jsonschema:"(optional) Filter by head user/org and branch (format: user:ref-name)."`
	Base     string `json:"base,omitempty" jsonschema:"(optional) Filter by base branch name."`
	PerPage  int    `json:"perPage,omitempty" jsonschema:"(optional) Results per page. Defaults to 30, max 100."`
	MaxPages int    `json:"maxPages,omitempty" jsonschema:"(optional) Maximum number of pages to fetch. Defaults to 0, which loops over all pages."`
	Filter   string `json:"filter,omitempty" jsonschema:"(optional) Go RE2 regex applied on each PR JSON output."`
}

type PRListOutput struct {
	Number     int    `json:"number"`
	Title      string `json:"title"`
	State      string `json:"state"`
	Author     string `json:"author"`
	BaseBranch string `json:"baseBranch"`
	HeadBranch string `json:"headBranch"`
	Draft      bool   `json:"draft"`
	CreatedAt  string `json:"createdAt"`
	UpdatedAt  string `json:"updatedAt"`
	HTMLURL    string `json:"htmlURL"`
}

type PRListTool struct {
	*baseTool
	tool.InvokableTool
}

func (t *PRListTool) Invoke(ctx context.Context, params *PRListParams) (result string, err error) {
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

	state := params.State
	if state == "" {
		state = "open"
	}

	perPage := params.PerPage
	if perPage <= 0 || perPage > 100 {
		perPage = 30
	}

	opts := &github.PullRequestListOptions{
		State: state,
		Head:  params.Head,
		Base:  params.Base,
		ListOptions: github.ListOptions{
			PerPage: perPage,
		},
	}

	prs, err := paginateList(func(page int) ([]*github.PullRequest, *github.Response, error) {
		opts.Page = page
		return c.PullRequests.List(ctx, params.Owner, params.Repo, opts)
	}, params.MaxPages)
	if err != nil {
		return "", errors.Wrap(err, "failed to list pull requests")
	}

	return filterMapMarshal(prs, re, func(item *github.PullRequest) PRListOutput {
		author := ""
		if item.User != nil {
			author = item.User.GetLogin()
		}
		headBranch := ""
		if item.Head != nil {
			headBranch = item.Head.GetRef()
		}
		baseBranch := ""
		if item.Base != nil {
			baseBranch = item.Base.GetRef()
		}
		return PRListOutput{
			Number:     item.GetNumber(),
			Title:      item.GetTitle(),
			State:      item.GetState(),
			Author:     author,
			BaseBranch: baseBranch,
			HeadBranch: headBranch,
			Draft:      item.GetDraft(),
			CreatedAt:  item.GetCreatedAt().String(),
			UpdatedAt:  item.GetUpdatedAt().String(),
			HTMLURL:    item.GetHTMLURL(),
		}
	})
}

func NewPRListTool(ctx context.Context, configs Configs) (*PRListTool, error) {
	base, err := newBaseTool(ctx, configs)
	if err != nil {
		return nil, err
	}
	return newPRListTool(ctx, base)
}

func newPRListTool(ctx context.Context, base *baseTool) (*PRListTool, error) {
	listTool := &PRListTool{baseTool: base}
	t, err := utils.InferTool("github_pr_list", fmt.Sprintf("%s\n%s", prListDescription, listOutputGuidance), listTool.Invoke, utils.WithSchemaModifier(base.instanceSchemaModifier()))
	if err != nil {
		return nil, err
	}
	listTool.InvokableTool = t

	return listTool, nil
}
