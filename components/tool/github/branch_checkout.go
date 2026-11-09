package github

import (
	"context"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/goccy/go-json"
)

const branchCheckoutDescription = `
** General Purpose **
It checks out an existing branch in a previously cloned GitHub repository, or
creates it from a base branch. This is a local-only operation: it does not call
the GitHub API and does not commit or push.

** Output **
It returns the branch name, the commit SHA it points at, and whether it was
created.

** Important **
- The repository must already be cloned under <CloneDir>/<session>/<owner>/<repo>.
- Set create=true to create the branch when it does not exist locally.
- baseBranch defaults to the current HEAD when creating a branch.
- Executes directly without confirmation. Set DryRun=true to preview.
`

// BranchCheckoutParams defines the parameters for checking out a local branch.
type BranchCheckoutParams struct {
	Instance   string `json:"instance"   validate:"required" jsonschema:"(required) The GitHub instance to connect to."`
	Owner      string `json:"owner"      validate:"required" jsonschema:"(required) Repository owner."`
	Repo       string `json:"repo"       validate:"required" jsonschema:"(required) Repository name."`
	Branch     string `json:"branch"     validate:"required" jsonschema:"(required) Branch to check out."`
	BaseBranch string `json:"baseBranch,omitempty" jsonschema:"(optional) Base branch or commit SHA used when creating the branch. Defaults to current HEAD."`
	Create     bool   `json:"create,omitempty" jsonschema:"(optional) Create the branch if it does not exist locally."`
	DryRun     bool   `json:"dryRun,omitempty" jsonschema:"(optional) If true, preview the checkout without changing the worktree."`
}

// BranchCheckoutOutput is the result of a branch checkout.
type BranchCheckoutOutput struct {
	Branch  string `json:"branch"`
	SHA     string `json:"sha"`
	Created bool   `json:"created"`
}

// BranchCheckoutTool is an eino tool for checking out local branches.
type BranchCheckoutTool struct {
	*baseTool
	tool.InvokableTool
}

// Invoke checks out or creates the requested branch.
func (t *BranchCheckoutTool) Invoke(ctx context.Context, params *BranchCheckoutParams) (string, error) {
	if err := validateParams(params); err != nil {
		return "", err
	}

	if params.DryRun {
		preview := map[string]any{
			"dryRun": true,
			"wouldCheckout": map[string]any{
				"branch":     params.Branch,
				"baseBranch": params.BaseBranch,
				"create":     params.Create,
			},
		}
		previewJSON, err := json.Marshal(preview)
		if err != nil {
			return "", errors.Wrap(err, "failed to marshal dry-run preview")
		}
		return string(previewJSON), nil
	}

	clonePath_, err := t.clonePathForSession(ctx, params.Owner, params.Repo)
	if err != nil {
		return "", err
	}
	if err := ensureCloneExists(clonePath_, params.Owner, params.Repo); err != nil {
		return "", err
	}

	if err := t.touchCloneSession(ctx); err != nil {
		return "", err
	}

	repo, err := git.PlainOpen(clonePath_)
	if err != nil {
		return "", errors.Wrapf(err, "failed to open cloned repository at %q; run github_repo_clone first", clonePath_)
	}
	wt, err := repo.Worktree()
	if err != nil {
		return "", errors.Wrap(err, "failed to get worktree")
	}

	created := false
	if _, err := repo.Reference(plumbing.NewBranchReferenceName(params.Branch), false); err != nil {
		if !errors.Is(err, plumbing.ErrReferenceNotFound) {
			return "", errors.Wrapf(err, "failed to resolve branch %q", params.Branch)
		}
		created = params.Create
	}

	hash, err := checkoutBranch(repo, wt, params.Branch, params.BaseBranch, params.Create)
	if err != nil {
		return "", err
	}

	output := &BranchCheckoutOutput{
		Branch:  params.Branch,
		SHA:     hash.String(),
		Created: created,
	}
	result, err := json.Marshal(output)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal output")
	}
	return string(result), nil
}

// NewBranchCheckoutTool creates a new BranchCheckoutTool.
func NewBranchCheckoutTool(ctx context.Context, configs Configs) (*BranchCheckoutTool, error) {
	base, err := newBaseTool(ctx, configs)
	if err != nil {
		return nil, err
	}
	return newBranchCheckoutTool(ctx, base)
}

func newBranchCheckoutTool(ctx context.Context, base *baseTool) (*BranchCheckoutTool, error) {
	checkoutTool := &BranchCheckoutTool{baseTool: base}
	t, err := utils.InferTool("github_branch_checkout", branchCheckoutDescription, checkoutTool.Invoke, utils.WithSchemaModifier(base.instanceSchemaModifier()))
	if err != nil {
		return nil, err
	}
	checkoutTool.InvokableTool = t
	return checkoutTool, nil
}
