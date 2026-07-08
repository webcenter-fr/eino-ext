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

const prCommentDescription = `
** General Purpose **
It adds a comment to a GitHub pull request.

** Output **
It returns the created comment details.
`

type PRCommentParams struct {
	Instance  string `json:"instance" validate:"required" jsonschema:"(required) The GitHub instance to connect to."`
	Owner     string `json:"owner" validate:"required" jsonschema:"(required) Repository owner."`
	Repo      string `json:"repo" validate:"required" jsonschema:"(required) Repository name."`
	Number    int    `json:"number" validate:"required" jsonschema:"(required) PR number."`
	Body      string `json:"body" validate:"required" jsonschema:"(required) Comment body."`
	DryRun    bool   `json:"dryRun,omitempty" jsonschema:"(optional) If true, simulate the comment without posting."`
	Confirmed bool   `json:"confirmed,omitempty" jsonschema:"(optional) Must be true to actually post the comment. Set this after the user has approved the dry-run result."`
}

type PRCommentTool struct {
	*baseTool
	tool.InvokableTool
}

func (t *PRCommentTool) Invoke(ctx context.Context, params *PRCommentParams) (result string, err error) {
	if err := validateParams(params); err != nil {
		return "", err
	}

	if params.DryRun {
		return fmt.Sprintf(`{"dryRun": true, "wouldComment": {"number": %d, "bodyPreview": %q}}`, params.Number, truncate(params.Body, 100)), nil
	}

	if err := confirm.RequireConfirmationForAction("add comment", params.Confirmed); err != nil {
		return "", err
	}

	c, err := t.client(params.Instance)
	if err != nil {
		return "", err
	}

	comment, _, err := c.Issues.CreateComment(ctx, params.Owner, params.Repo, params.Number, &github.IssueComment{
		Body: github.Ptr(params.Body),
	})
	if err != nil {
		return "", errors.Wrap(err, "failed to create PR comment")
	}

	return fmt.Sprintf(`{"created": true, "comment": {"id": %d, "htmlURL": %q}}`, comment.GetID(), comment.GetHTMLURL()), nil
}

func NewPRCommentTool(ctx context.Context, configs Configs) (*PRCommentTool, error) {
	base, err := newBaseTool(ctx, configs)
	if err != nil {
		return nil, err
	}
	return newPRCommentTool(ctx, base)
}

func newPRCommentTool(ctx context.Context, base *baseTool) (*PRCommentTool, error) {
	commentTool := &PRCommentTool{baseTool: base}
	t, err := utils.InferTool("github_pr_comment", prCommentDescription, commentTool.Invoke)
	if err != nil {
		return nil, err
	}
	commentTool.InvokableTool = t

	return commentTool, nil
}
