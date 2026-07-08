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

const prReviewDescription = `
** General Purpose **
It creates a pull request review (approve, request changes, or comment).

** Output **
It returns the created review details.

** Important **
- The event must be one of: APPROVE, REQUEST_CHANGES, COMMENT.
- Inline comments can be provided as an array of path/position/body objects.
`

type PRReviewParams struct {
	Instance string `json:"instance" validate:"required" jsonschema:"(required) The GitHub instance to connect to."`
	Owner    string `json:"owner" validate:"required" jsonschema:"(required) Repository owner."`
	Repo     string `json:"repo" validate:"required" jsonschema:"(required) Repository name."`
	Number   int    `json:"number" validate:"required" jsonschema:"(required) PR number."`
	Event    string `json:"event" validate:"required,oneof=APPROVE REQUEST_CHANGES COMMENT" jsonschema:"(required) Review event: APPROVE, REQUEST_CHANGES, or COMMENT."`
	Body     string `json:"body,omitempty" jsonschema:"(optional) Review comment body."`
	DryRun   bool   `json:"dryRun,omitempty" jsonschema:"(optional) If true, simulate the review without submitting."`
	Confirmed bool  `json:"confirmed,omitempty" jsonschema:"(optional) Must be true to actually submit the review. Set this after the user has approved the dry-run result."`
}

type PRReviewTool struct {
	*baseTool
	tool.InvokableTool
}

func (t *PRReviewTool) Invoke(ctx context.Context, params *PRReviewParams) (result string, err error) {
	if err := validateParams(params); err != nil {
		return "", err
	}

	if params.DryRun {
		return fmt.Sprintf(`{"dryRun": true, "wouldSubmit": {"number": %d, "event": %q, "bodyPreview": %q}}`, params.Number, params.Event, truncate(params.Body, 100)), nil
	}

	if err := confirm.RequireConfirmationForAction("submit review", params.Confirmed); err != nil {
		return "", err
	}

	c, err := t.client(params.Instance)
	if err != nil {
		return "", err
	}

	review, _, err := c.PullRequests.CreateReview(ctx, params.Owner, params.Repo, params.Number, &github.PullRequestReviewRequest{
		Event: github.Ptr(params.Event),
		Body:  stringPtr(params.Body),
	})
	if err != nil {
		return "", errors.Wrap(err, "failed to create PR review")
	}

	return fmt.Sprintf(`{"created": true, "review": {"id": %d, "event": %q, "htmlURL": %q}}`, review.GetID(), params.Event, review.GetHTMLURL()), nil
}

func NewPRReviewTool(ctx context.Context, configs Configs) (*PRReviewTool, error) {
	base, err := newBaseTool(ctx, configs)
	if err != nil {
		return nil, err
	}
	return newPRReviewTool(ctx, base)
}

func newPRReviewTool(ctx context.Context, base *baseTool) (*PRReviewTool, error) {
	reviewTool := &PRReviewTool{baseTool: base}
	t, err := utils.InferTool("github_pr_review", prReviewDescription, reviewTool.Invoke)
	if err != nil {
		return nil, err
	}
	reviewTool.InvokableTool = t

	return reviewTool, nil
}
