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

const prCreateDescription = `
** General Purpose **
It creates a new pull request in a GitHub repository.

** Output **
It returns the created pull request details.
`

type PRCreateParams struct {
	Instance string `json:"instance" validate:"required" jsonschema:"(required) The GitHub instance to connect to."`
	Owner    string `json:"owner" validate:"required" jsonschema:"(required) Repository owner."`
	Repo     string `json:"repo" validate:"required" jsonschema:"(required) Repository name."`
	Title    string `json:"title" validate:"required" jsonschema:"(required) The PR title."`
	Head     string `json:"head" validate:"required" jsonschema:"(required) The name of the branch where your changes are implemented."`
	Base     string `json:"base,omitempty" jsonschema:"(optional) The name of the branch you want the changes pulled into. Defaults to the repository default branch."`
	Body     string `json:"body,omitempty" jsonschema:"(optional) The PR body."`
	Draft    bool   `json:"draft,omitempty" jsonschema:"(optional) Create as draft PR."`
	DryRun   bool   `json:"dryRun,omitempty" jsonschema:"(optional) If true, simulate the PR creation without making changes."`
	Confirmed bool  `json:"confirmed,omitempty" jsonschema:"(optional) Must be true to actually execute the PR creation. Set this after the user has approved the dry-run result."`
}

type PRCreateTool struct {
	*baseTool
	tool.InvokableTool
}

func (t *PRCreateTool) Invoke(ctx context.Context, params *PRCreateParams) (result string, err error) {
	if err := validateParams(params); err != nil {
		return "", err
	}

	if params.DryRun {
		return fmt.Sprintf(`{"dryRun": true, "wouldCreate": {"title": %q, "head": %q, "base": %q, "draft": %v}}`, params.Title, params.Head, params.Base, params.Draft), nil
	}

	if err := confirm.RequireConfirmationForAction("create pull request", params.Confirmed); err != nil {
		return "", err
	}

	c, err := t.client(params.Instance)
	if err != nil {
		return "", err
	}

	pr, _, err := c.PullRequests.Create(ctx, params.Owner, params.Repo, &github.NewPullRequest{
		Title: github.Ptr(params.Title),
		Head:  github.Ptr(params.Head),
		Base:  stringPtr(params.Base),
		Body:  stringPtr(params.Body),
		Draft: boolPtr(params.Draft),
	})
	if err != nil {
		return "", errors.Wrap(err, "failed to create pull request")
	}

	return fmt.Sprintf(`{"created": true, "pr": {"number": %d, "title": %q, "htmlURL": %q, "state": %q, "draft": %v}}`,
		pr.GetNumber(), pr.GetTitle(), pr.GetHTMLURL(), pr.GetState(), pr.GetDraft()), nil
}

func NewPRCreateTool(ctx context.Context, configs Configs) (*PRCreateTool, error) {
	base, err := newBaseTool(ctx, configs)
	if err != nil {
		return nil, err
	}
	return newPRCreateTool(ctx, base)
}

func newPRCreateTool(ctx context.Context, base *baseTool) (*PRCreateTool, error) {
	createTool := &PRCreateTool{baseTool: base}
	t, err := utils.InferTool("github_pr_create", prCreateDescription, createTool.Invoke, utils.WithSchemaModifier(base.instanceSchemaModifier()))
	if err != nil {
		return nil, err
	}
	createTool.InvokableTool = t

	return createTool, nil
}
