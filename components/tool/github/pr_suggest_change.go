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

const prSuggestChangeDescription = `
** General Purpose **
It posts a review comment with a code suggestion on a specific file and line in a pull request.

** Output **
It returns the created review comment details.

** Important **
- This tool creates an inline review comment on a specific file path and line.
- The suggestion should be a diff patch or a clear description of the change.
- The comment will appear in the PR's "Files changed" tab.
`

type PRSuggestChangeParams struct {
	Instance   string `json:"instance" validate:"required" jsonschema:"(required) The GitHub instance to connect to."`
	Owner      string `json:"owner" validate:"required" jsonschema:"(required) Repository owner."`
	Repo       string `json:"repo" validate:"required" jsonschema:"(required) Repository name."`
	Number     int    `json:"number" validate:"required" jsonschema:"(required) PR number."`
	CommitID   string `json:"commitId" validate:"required" jsonschema:"(required) The SHA of the commit to comment on."`
	FilePath   string `json:"filePath" validate:"required" jsonschema:"(required) The relative path to the file being commented on."`
	Line       int    `json:"line" validate:"required,gte=0" jsonschema:"(required) The line number in the file to comment on."`
	Body       string `json:"body" validate:"required" jsonschema:"(required) The suggestion text. Use GitHub suggestion blocks for code changes."`
	DryRun     bool   `json:"dryRun,omitempty" jsonschema:"(optional) If true, simulate the suggestion without posting."`
	Confirmed  bool   `json:"confirmed,omitempty" jsonschema:"(optional) Must be true to actually post the suggestion. Set this after the user has approved the dry-run result."`
}

type PRSuggestChangeTool struct {
	*baseTool
	tool.InvokableTool
}

func (t *PRSuggestChangeTool) Invoke(ctx context.Context, params *PRSuggestChangeParams) (result string, err error) {
	if err := validateParams(params); err != nil {
		return "", err
	}

	if params.DryRun {
		return fmt.Sprintf(`{"dryRun": true, "wouldSuggest": {"filePath": %q, "line": %d, "bodyPreview": %q}}`, params.FilePath, params.Line, truncate(params.Body, 100)), nil
	}

	if err := confirm.RequireConfirmationForAction("post suggestion", params.Confirmed); err != nil {
		return "", err
	}

	c, err := t.client(params.Instance)
	if err != nil {
		return "", err
	}

	comment, _, err := c.PullRequests.CreateComment(ctx, params.Owner, params.Repo, params.Number, &github.PullRequestComment{
		CommitID: github.Ptr(params.CommitID),
		Path:     github.Ptr(params.FilePath),
		Line:     github.Ptr(params.Line),
		Body:     github.Ptr(params.Body),
	})
	if err != nil {
		return "", errors.Wrap(err, "failed to create PR review comment")
	}

	return fmt.Sprintf(`{"created": true, "comment": {"id": %d, "path": %q, "line": %d, "htmlURL": %q}}`, comment.GetID(), comment.GetPath(), comment.GetLine(), comment.GetHTMLURL()), nil
}

func NewPRSuggestChangeTool(ctx context.Context, configs Configs) (*PRSuggestChangeTool, error) {
	base, err := newBaseTool(ctx, configs)
	if err != nil {
		return nil, err
	}
	return newPRSuggestChangeTool(ctx, base)
}

func newPRSuggestChangeTool(ctx context.Context, base *baseTool) (*PRSuggestChangeTool, error) {
	suggestTool := &PRSuggestChangeTool{baseTool: base}
	t, err := utils.InferTool("github_pr_suggest_change", prSuggestChangeDescription, suggestTool.Invoke)
	if err != nil {
		return nil, err
	}
	suggestTool.InvokableTool = t

	return suggestTool, nil
}
