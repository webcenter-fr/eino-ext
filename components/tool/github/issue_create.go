package github

import (
	"context"
	"fmt"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/google/go-github/v71/github"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/confirm"
)

const issueCreateDescription = `
** General Purpose **
It creates a new issue in a GitHub repository.

** Output **
It returns the created issue details.
`

// IssueCreateParams defines the parameters for creating a GitHub issue.
type IssueCreateParams struct {
	Instance  string `json:"instance" validate:"required" jsonschema:"(required) The GitHub instance to connect to."`
	Owner     string `json:"owner" validate:"required" jsonschema:"(required) Repository owner."`
	Repo      string `json:"repo" validate:"required" jsonschema:"(required) Repository name."`
	Title     string `json:"title" validate:"required" jsonschema:"(required) The issue title."`
	Body      string `json:"body,omitempty" jsonschema:"(optional) The issue body."`
	Labels    string `json:"labels,omitempty" jsonschema:"(optional) Comma-separated labels."`
	Assignee  string `json:"assignee,omitempty" jsonschema:"(optional) Assignee login."`
	DryRun    bool   `json:"dryRun,omitempty" jsonschema:"(optional) If true, simulate the issue creation without making changes."`
	Confirmed bool   `json:"confirmed,omitempty" jsonschema:"(optional) Must be true to actually execute the issue creation. Set this after the user has approved the dry-run result."`
}

// IssueCreateTool is an eino tool for creating GitHub issues.
type IssueCreateTool struct {
	*baseTool
	tool.InvokableTool
}

// Invoke creates a GitHub issue and returns the result.
func (t *IssueCreateTool) Invoke(ctx context.Context, params *IssueCreateParams) (result string, err error) {
	if err := validateParams(params); err != nil {
		return "", err
	}

	if params.DryRun {
		return fmt.Sprintf(`{"dryRun": true, "wouldCreate": {"title": %q, "labels": %q, "assignee": %q}}`, params.Title, params.Labels, params.Assignee), nil
	}

	if err := confirm.RequireConfirmationForActionCtx(ctx, "github_issue_create", "create issue", params.Confirmed); err != nil {
		return "", err
	}

	c, err := t.client(params.Instance)
	if err != nil {
		return "", err
	}

	labels := labelList(params.Labels)
	issueReq := &github.IssueRequest{
		Title:    github.Ptr(params.Title),
		Body:     stringPtr(params.Body),
		Assignee: stringPtr(params.Assignee),
		Labels:   &labels,
	}

	issue, _, err := c.Issues.Create(ctx, params.Owner, params.Repo, issueReq)
	if err != nil {
		return "", errors.Wrap(err, "failed to create issue")
	}

	return fmt.Sprintf(`{"created": true, "issue": {"number": %d, "title": %q, "htmlURL": %q, "state": %q}}`,
		issue.GetNumber(), issue.GetTitle(), issue.GetHTMLURL(), issue.GetState()), nil
}

// NewIssueCreateTool creates a new IssueCreateTool.
func NewIssueCreateTool(ctx context.Context, configs Configs) (*IssueCreateTool, error) {
	base, err := newBaseTool(ctx, configs)
	if err != nil {
		return nil, err
	}
	return newIssueCreateTool(ctx, base)
}

func newIssueCreateTool(ctx context.Context, base *baseTool) (*IssueCreateTool, error) {
	createTool := &IssueCreateTool{baseTool: base}
	t, err := utils.InferTool("github_issue_create", issueCreateDescription, createTool.Invoke, utils.WithSchemaModifier(base.instanceSchemaModifier()))
	if err != nil {
		return nil, err
	}
	createTool.InvokableTool = t

	return createTool, nil
}
