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

const repoSettingsUpdateDescription = `
** General Purpose **
It updates repository settings such as default branch, description, visibility, and features.

** Output **
It returns the updated repository details.
`

type RepoSettingsUpdateParams struct {
	Instance              string `json:"instance" validate:"required" jsonschema:"(required) The GitHub instance to connect to."`
	Owner                 string `json:"owner" validate:"required" jsonschema:"(required) Repository owner."`
	Repo                  string `json:"repo" validate:"required" jsonschema:"(required) Repository name."`
	Description           string `json:"description,omitempty" jsonschema:"(optional) Repository description."`
	Homepage              string `json:"homepage,omitempty" jsonschema:"(optional) Repository homepage URL."`
	Private               *bool  `json:"private,omitempty" jsonschema:"(optional) Set repository visibility: true=private, false=public."`
	HasIssues             *bool  `json:"hasIssues,omitempty" jsonschema:"(optional) Enable/disable issues."`
	HasProjects           *bool  `json:"hasProjects,omitempty" jsonschema:"(optional) Enable/disable projects."`
	HasWiki               *bool  `json:"hasWiki,omitempty" jsonschema:"(optional) Enable/disable wiki."`
	DefaultBranch         string `json:"defaultBranch,omitempty" jsonschema:"(optional) Default branch name."`
	AllowSquashMerge      *bool  `json:"allowSquashMerge,omitempty" jsonschema:"(optional) Allow squash merging."`
	AllowMergeCommit      *bool  `json:"allowMergeCommit,omitempty" jsonschema:"(optional) Allow merge commits."`
	AllowRebaseMerge      *bool  `json:"allowRebaseMerge,omitempty" jsonschema:"(optional) Allow rebase merging."`
	DeleteBranchOnMerge   *bool  `json:"deleteBranchOnMerge,omitempty" jsonschema:"(optional) Auto-delete head branch after merge."`
	DryRun                bool   `json:"dryRun,omitempty" jsonschema:"(optional) If true, simulate the update without making changes."`
	Confirmed             bool   `json:"confirmed,omitempty" jsonschema:"(optional) Must be true to actually execute the update. Set this after the user has approved the dry-run result."`
}

type RepoSettingsUpdateTool struct {
	*baseTool
	tool.InvokableTool
}

func (t *RepoSettingsUpdateTool) Invoke(ctx context.Context, params *RepoSettingsUpdateParams) (result string, err error) {
	if err := validateParams(params); err != nil {
		return "", err
	}

	if params.DryRun {
		return fmt.Sprintf(`{"dryRun": true, "wouldUpdate": {"owner": %q, "repo": %q}}`, params.Owner, params.Repo), nil
	}

	if err := confirm.RequireConfirmationForAction("update repository settings", params.Confirmed); err != nil {
		return "", err
	}

	c, err := t.client(params.Instance)
	if err != nil {
		return "", err
	}

	repo, _, err := c.Repositories.Get(ctx, params.Owner, params.Repo)
	if err != nil {
		return "", errors.Wrap(err, "failed to get repository")
	}

	if params.Description != "" {
		repo.Description = github.Ptr(params.Description)
	}
	if params.Homepage != "" {
		repo.Homepage = github.Ptr(params.Homepage)
	}
	if params.Private != nil {
		repo.Private = params.Private
	}
	if params.HasIssues != nil {
		repo.HasIssues = params.HasIssues
	}
	if params.HasProjects != nil {
		repo.HasProjects = params.HasProjects
	}
	if params.HasWiki != nil {
		repo.HasWiki = params.HasWiki
	}
	if params.DefaultBranch != "" {
		repo.DefaultBranch = github.Ptr(params.DefaultBranch)
	}
	if params.AllowSquashMerge != nil {
		repo.AllowSquashMerge = params.AllowSquashMerge
	}
	if params.AllowMergeCommit != nil {
		repo.AllowMergeCommit = params.AllowMergeCommit
	}
	if params.AllowRebaseMerge != nil {
		repo.AllowRebaseMerge = params.AllowRebaseMerge
	}
	if params.DeleteBranchOnMerge != nil {
		repo.DeleteBranchOnMerge = params.DeleteBranchOnMerge
	}

	updated, _, err := c.Repositories.Edit(ctx, params.Owner, params.Repo, repo)
	if err != nil {
		return "", errors.Wrap(err, "failed to update repository settings")
	}

	return fmt.Sprintf(`{"updated": true, "repo": {"name": %q, "fullName": %q, "private": %v, "defaultBranch": %q, "htmlURL": %q}}`,
		updated.GetName(), updated.GetFullName(), updated.GetPrivate(), updated.GetDefaultBranch(), updated.GetHTMLURL()), nil
}

func NewRepoSettingsUpdateTool(ctx context.Context, configs Configs) (*RepoSettingsUpdateTool, error) {
	base, err := newBaseTool(ctx, configs)
	if err != nil {
		return nil, err
	}
	return newRepoSettingsUpdateTool(ctx, base)
}

func newRepoSettingsUpdateTool(ctx context.Context, base *baseTool) (*RepoSettingsUpdateTool, error) {
	settingsTool := &RepoSettingsUpdateTool{baseTool: base}
	t, err := utils.InferTool("github_repo_settings_update", repoSettingsUpdateDescription, settingsTool.Invoke, utils.WithSchemaModifier(base.instanceSchemaModifier()))
	if err != nil {
		return nil, err
	}
	settingsTool.InvokableTool = t

	return settingsTool, nil
}
