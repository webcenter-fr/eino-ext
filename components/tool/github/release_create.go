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

const releaseCreateDescription = `
** General Purpose **
It creates a new GitHub release with an optional tag.

** Output **
It returns the created release details.

** Important **
- If the tag does not exist, a lightweight tag will be created.
- The release can be marked as a draft or pre-release.
`

type ReleaseCreateParams struct {
	Instance       string `json:"instance" validate:"required" jsonschema:"(required) The GitHub instance to connect to."`
	Owner          string `json:"owner" validate:"required" jsonschema:"(required) Repository owner."`
	Repo           string `json:"repo" validate:"required" jsonschema:"(required) Repository name."`
	TagName        string `json:"tagName" validate:"required" jsonschema:"(required) Tag to create the release from."`
	TargetCommitish string `json:"targetCommitish,omitempty" jsonschema:"(optional) Commitish value that determines where the Git tag is created from. Defaults to the default branch."`
	Name           string `json:"name,omitempty" jsonschema:"(optional) Release name. Defaults to tag name."`
	Body           string `json:"body,omitempty" jsonschema:"(optional) Release body/notes."`
	Draft          bool   `json:"draft,omitempty" jsonschema:"(optional) Mark as draft release."`
	Prerelease     bool   `json:"prerelease,omitempty" jsonschema:"(optional) Mark as pre-release."`
	DryRun         bool   `json:"dryRun,omitempty" jsonschema:"(optional) If true, simulate the release creation without making changes."`
	Confirmed      bool   `json:"confirmed,omitempty" jsonschema:"(optional) Must be true to actually execute the release creation. Set this after the user has approved the dry-run result."`
}

type ReleaseCreateTool struct {
	*baseTool
	tool.InvokableTool
}

func (t *ReleaseCreateTool) Invoke(ctx context.Context, params *ReleaseCreateParams) (result string, err error) {
	if err := validateParams(params); err != nil {
		return "", err
	}

	if params.DryRun {
		name := params.Name
		if name == "" {
			name = params.TagName
		}
		return fmt.Sprintf(`{"dryRun": true, "wouldCreate": {"tagName": %q, "name": %q, "draft": %v, "prerelease": %v}}`, params.TagName, name, params.Draft, params.Prerelease), nil
	}

	if err := confirm.RequireConfirmationForAction("create release", params.Confirmed); err != nil {
		return "", err
	}

	c, err := t.client(params.Instance)
	if err != nil {
		return "", err
	}

	release := &github.RepositoryRelease{
		TagName:         github.Ptr(params.TagName),
		TargetCommitish: stringPtr(params.TargetCommitish),
		Name:            stringPtr(params.Name),
		Body:            stringPtr(params.Body),
		Draft:           boolPtr(params.Draft),
		Prerelease:      boolPtr(params.Prerelease),
	}

	rel, _, err := c.Repositories.CreateRelease(ctx, params.Owner, params.Repo, release)
	if err != nil {
		return "", errors.Wrap(err, "failed to create release")
	}

	return fmt.Sprintf(`{"created": true, "release": {"id": %d, "tagName": %q, "name": %q, "htmlURL": %q, "draft": %v, "prerelease": %v}}`,
		rel.GetID(), rel.GetTagName(), rel.GetName(), rel.GetHTMLURL(), rel.GetDraft(), rel.GetPrerelease()), nil
}

func NewReleaseCreateTool(ctx context.Context, configs Configs) (*ReleaseCreateTool, error) {
	base, err := newBaseTool(ctx, configs)
	if err != nil {
		return nil, err
	}
	return newReleaseCreateTool(ctx, base)
}

func newReleaseCreateTool(ctx context.Context, base *baseTool) (*ReleaseCreateTool, error) {
	releaseTool := &ReleaseCreateTool{baseTool: base}
	t, err := utils.InferTool("github_release_create", releaseCreateDescription, releaseTool.Invoke)
	if err != nil {
		return nil, err
	}
	releaseTool.InvokableTool = t

	return releaseTool, nil
}
