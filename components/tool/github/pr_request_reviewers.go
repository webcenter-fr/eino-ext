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

const prRequestReviewersDescription = `
** General Purpose **
It requests reviewers on a GitHub pull request.

** Output **
It returns the PR details with the requested reviewers.
`

// PRRequestReviewersParams defines the parameters for requesting PR reviewers.
type PRRequestReviewersParams struct {
	Instance  string   `json:"instance" validate:"required" jsonschema:"(required) The GitHub instance to connect to."`
	Owner     string   `json:"owner" validate:"required" jsonschema:"(required) Repository owner."`
	Repo      string   `json:"repo" validate:"required" jsonschema:"(required) Repository name."`
	Number    int      `json:"number" validate:"required" jsonschema:"(required) PR number."`
	Reviewers []string `json:"reviewers" validate:"required,min=1" jsonschema:"(required) GitHub usernames to request as reviewers."`
	DryRun    bool     `json:"dryRun,omitempty" jsonschema:"(optional) If true, simulate the request without making changes."`
	Confirmed bool     `json:"confirmed,omitempty" jsonschema:"(optional) Must be true to actually request reviewers. Set this after the user has approved the dry-run result."`
}

// PRRequestReviewersTool is an eino tool for requesting PR reviewers.
type PRRequestReviewersTool struct {
	*baseTool
	tool.InvokableTool
}

// Invoke requests reviewers for a GitHub PR.
func (t *PRRequestReviewersTool) Invoke(ctx context.Context, params *PRRequestReviewersParams) (result string, err error) {
	if err := validateParams(params); err != nil {
		return "", err
	}

	if params.DryRun {
		return fmt.Sprintf(`{"dryRun": true, "wouldRequest": {"number": %d, "reviewers": %v}}`, params.Number, params.Reviewers), nil
	}

	if err := confirm.RequireConfirmationForActionCtx(ctx, "github_pr_request_reviewers", "request reviewers", params.Confirmed); err != nil {
		return "", err
	}

	c, err := t.client(params.Instance)
	if err != nil {
		return "", err
	}

	pr, _, err := c.PullRequests.RequestReviewers(ctx, params.Owner, params.Repo, params.Number, github.ReviewersRequest{
		Reviewers: params.Reviewers,
	})
	if err != nil {
		return "", errors.Wrap(err, "failed to request reviewers")
	}

	requested := make([]string, 0, len(pr.RequestedReviewers))
	for _, r := range pr.RequestedReviewers {
		requested = append(requested, r.GetLogin())
	}

	return fmt.Sprintf(`{"requested": true, "pr": {"number": %d, "requestedReviewers": %v}}`, pr.GetNumber(), requested), nil
}

// NewPRRequestReviewersTool creates a new PRRequestReviewersTool.
func NewPRRequestReviewersTool(ctx context.Context, configs Configs) (*PRRequestReviewersTool, error) {
	base, err := newBaseTool(ctx, configs)
	if err != nil {
		return nil, err
	}
	return newPRRequestReviewersTool(ctx, base)
}

func newPRRequestReviewersTool(ctx context.Context, base *baseTool) (*PRRequestReviewersTool, error) {
	reviewersTool := &PRRequestReviewersTool{baseTool: base}
	t, err := utils.InferTool("github_pr_request_reviewers", prRequestReviewersDescription, reviewersTool.Invoke, utils.WithSchemaModifier(base.instanceSchemaModifier()))
	if err != nil {
		return nil, err
	}
	reviewersTool.InvokableTool = t

	return reviewersTool, nil
}
