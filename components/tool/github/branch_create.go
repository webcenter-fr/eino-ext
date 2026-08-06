package github

import (
	"context"
	"fmt"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/google/go-github/v71/github"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/confirm"
)

const branchCreateDescription = `
** General Purpose **
It creates a new branch in a GitHub repository, either locally (in an existing clone)
or remotely via the GitHub API.

** Output **
It returns the branch name and commit SHA.

** Important **
- For local branches, the repository must already be cloned under <CloneDir>/<owner>/<repo>.
- Use ` + "`remote=true`" + ` to create a remote branch via the GitHub API.
`

// BranchCreateParams defines the parameters for creating a branch.
type BranchCreateParams struct {
	Instance    string `json:"instance" validate:"required" jsonschema:"(required) The GitHub instance to connect to."`
	Owner       string `json:"owner" validate:"required" jsonschema:"(required) Repository owner."`
	Repo        string `json:"repo" validate:"required" jsonschema:"(required) Repository name."`
	BranchName  string `json:"branchName" validate:"required" jsonschema:"(required) Name of the branch to create."`
	BaseBranch  string `json:"baseBranch,omitempty" jsonschema:"(optional) Base branch or commit SHA. Defaults to the default branch."`
	Remote      bool   `json:"remote,omitempty" jsonschema:"(optional) If true, create the branch remotely via the GitHub API instead of locally."`
	DryRun      bool   `json:"dryRun,omitempty" jsonschema:"(optional) If true, simulate the branch creation without making changes."`
	Confirmed   bool   `json:"confirmed,omitempty" jsonschema:"(optional) Must be true to actually execute the branch creation. Set this after the user has approved the dry-run result."`
}

// BranchCreateTool is an eino tool for creating branches.
type BranchCreateTool struct {
	*baseTool
	tool.InvokableTool
}

// Invoke creates a branch in a GitHub repository.
func (t *BranchCreateTool) Invoke(ctx context.Context, params *BranchCreateParams) (result string, err error) {
	if err := validateParams(params); err != nil {
		return "", err
	}

	if params.DryRun {
		mode := "local"
		if params.Remote {
			mode = "remote"
		}
		return fmt.Sprintf(`{"dryRun": true, "wouldCreateBranch": %q, "mode": %q, "baseBranch": %q}`, params.BranchName, mode, params.BaseBranch), nil
	}

	if err := confirm.RequireConfirmationForAction("create branch", params.Confirmed); err != nil {
		return "", err
	}

	if params.Remote {
		return t.createRemoteBranch(ctx, params)
	}
	return t.createLocalBranch(ctx, params)
}

func (t *BranchCreateTool) createRemoteBranch(ctx context.Context, params *BranchCreateParams) (string, error) {
	c, err := t.client(params.Instance)
	if err != nil {
		return "", err
	}

	var baseRef string
	var targetCommitSHA string

	if params.BaseBranch != "" {
		baseRef = fmt.Sprintf("refs/heads/%s", params.BaseBranch)
	} else {
		repo, _, err := c.Repositories.Get(ctx, params.Owner, params.Repo)
		if err != nil {
			return "", errors.Wrap(err, "failed to get repository")
		}
		baseRef = fmt.Sprintf("refs/heads/%s", repo.GetDefaultBranch())
	}

	baseRefObj, _, err := c.Git.GetRef(ctx, params.Owner, params.Repo, baseRef)
	if err != nil {
		return "", errors.Wrap(err, "failed to get base ref")
	}
	targetCommitSHA = baseRefObj.GetObject().GetSHA()

	newRef := &github.Reference{
		Ref: github.Ptr(fmt.Sprintf("refs/heads/%s", params.BranchName)),
		Object: &github.GitObject{
			SHA: github.Ptr(targetCommitSHA),
		},
	}

	_, _, err = c.Git.CreateRef(ctx, params.Owner, params.Repo, newRef)
	if err != nil {
		return "", errors.Wrap(err, "failed to create remote branch")
	}

	return fmt.Sprintf(`{"created": true, "branch": %q, "sha": %q, "mode": "remote"}`, params.BranchName, targetCommitSHA), nil
}

func (t *BranchCreateTool) createLocalBranch(ctx context.Context, params *BranchCreateParams) (string, error) {
	clonePath_ := clonePath(t.cloneDir, params.Owner, params.Repo)

	repo, err := git.PlainOpen(clonePath_)
	if err != nil {
		return "", errors.Wrap(err, "failed to open cloned repository; clone it first")
	}

	wt, err := repo.Worktree()
	if err != nil {
		return "", errors.Wrap(err, "failed to get worktree")
	}

	branchRef := plumbing.NewBranchReferenceName(params.BranchName)
	hash, err := repo.ResolveRevision(plumbing.Revision(params.BaseBranch))
	if err != nil {
		return "", errors.Wrap(err, "failed to resolve base branch")
	}

	err = wt.Checkout(&git.CheckoutOptions{
		Hash:   plumbing.NewHash(hash.String()),
		Branch: branchRef,
		Create: true,
	})
	if err != nil {
		return "", errors.Wrap(err, "failed to create local branch")
	}

	return fmt.Sprintf(`{"created": true, "branch": %q, "sha": %q, "mode": "local"}`, params.BranchName, hash.String()), nil
}

// NewBranchCreateTool creates a new BranchCreateTool.
func NewBranchCreateTool(ctx context.Context, configs Configs) (*BranchCreateTool, error) {
	base, err := newBaseTool(ctx, configs)
	if err != nil {
		return nil, err
	}
	return newBranchCreateTool(ctx, base)
}

func newBranchCreateTool(ctx context.Context, base *baseTool) (*BranchCreateTool, error) {
	branchTool := &BranchCreateTool{baseTool: base}
	t, err := utils.InferTool("github_branch_create", branchCreateDescription, branchTool.Invoke, utils.WithSchemaModifier(base.instanceSchemaModifier()))
	if err != nil {
		return nil, err
	}
	branchTool.InvokableTool = t

	return branchTool, nil
}
