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

const issueListDescription = `
** General Purpose **
It lists issues in a GitHub repository.

** Output **
It returns a JSON array of objects, where each object represents an issue with the following fields:
- number: the issue number.
- title: the issue title.
- state: the issue state (open/closed).
- author: the issue author login.
- labels: the issue labels.
- createdAt: when the issue was created.
- updatedAt: when the issue was last updated.
- htmlURL: the issue URL on GitHub.
`

type IssueListParams struct {
	Instance string `json:"instance" validate:"required" jsonschema:"(required) The GitHub instance to connect to."`
	Owner    string `json:"owner" validate:"required" jsonschema:"(required) Repository owner."`
	Repo     string `json:"repo" validate:"required" jsonschema:"(required) Repository name."`
	State    string `json:"state,omitempty" jsonschema:"(optional) Filter by state: open, closed, all. Defaults to open."`
	Labels   string `json:"labels,omitempty" jsonschema:"(optional) Comma-separated label names."`
	Assignee string `json:"assignee,omitempty" jsonschema:"(optional) Filter by assignee login."`
	PerPage  int    `json:"perPage,omitempty" jsonschema:"(optional) Results per page. Defaults to 30, max 100."`
	Filter   string `json:"filter,omitempty" jsonschema:"(optional) Go RE2 regex applied on each issue JSON output."`
}

type IssueListOutput struct {
	Number    int      `json:"number"`
	Title     string   `json:"title"`
	State     string   `json:"state"`
	Author    string   `json:"author"`
	Labels    []string `json:"labels"`
	CreatedAt string   `json:"createdAt"`
	UpdatedAt string   `json:"updatedAt"`
	HTMLURL   string   `json:"htmlURL"`
}

type IssueListTool struct {
	*baseTool
	tool.InvokableTool
}

func (t *IssueListTool) Invoke(ctx context.Context, params *IssueListParams) (result string, err error) {
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

	opts := &github.IssueListByRepoOptions{
		State:    state,
		Labels:   labelList(params.Labels),
		Assignee: params.Assignee,
		ListOptions: github.ListOptions{
			PerPage: perPage,
		},
	}

	issues, err := paginateList(func(page int) ([]*github.Issue, *github.Response, error) {
		opts.Page = page
		return c.Issues.ListByRepo(ctx, params.Owner, params.Repo, opts)
	}, defaultMaxPages)
	if err != nil {
		return "", errors.Wrap(err, "failed to list issues")
	}

	return filterMapMarshal(issues, re, func(item *github.Issue) IssueListOutput {
		labels := make([]string, 0, len(item.Labels))
		for _, l := range item.Labels {
			labels = append(labels, l.GetName())
		}
		author := ""
		if item.User != nil {
			author = item.User.GetLogin()
		}
		return IssueListOutput{
			Number:    item.GetNumber(),
			Title:     item.GetTitle(),
			State:     item.GetState(),
			Author:    author,
			Labels:    labels,
			CreatedAt: item.GetCreatedAt().String(),
			UpdatedAt: item.GetUpdatedAt().String(),
			HTMLURL:   item.GetHTMLURL(),
		}
	})
}

func NewIssueListTool(ctx context.Context, configs Configs) (*IssueListTool, error) {
	base, err := newBaseTool(ctx, configs)
	if err != nil {
		return nil, err
	}
	return newIssueListTool(ctx, base)
}

func newIssueListTool(ctx context.Context, base *baseTool) (*IssueListTool, error) {
	listTool := &IssueListTool{baseTool: base}
	t, err := utils.InferTool("github_issue_list", fmt.Sprintf("%s\n%s", issueListDescription, listOutputGuidance), listTool.Invoke)
	if err != nil {
		return nil, err
	}
	listTool.InvokableTool = t

	return listTool, nil
}
